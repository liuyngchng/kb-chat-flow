package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"kb-chat-flow/internal/config"
	"kb-chat-flow/internal/handler"
	"kb-chat-flow/internal/kb"
	"kb-chat-flow/internal/logger"
	"kb-chat-flow/internal/model"
	redisclient "kb-chat-flow/internal/redis"
	"kb-chat-flow/internal/session"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

//go:embed web
var webFS embed.FS

func main() {
	// 加载配置（server + milvus 从 YAML 文件）
	cfg, err := config.Load("cfg.yml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置文件失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	if err := logger.Init(cfg.Server.Debug); err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}

	// 角色便捷判断
	isAdmin := cfg.Server.Role == model.SvcRoleAdmin || cfg.Server.Role == model.SvcRoleAll
	isChat := cfg.Server.Role == model.SvcRoleChat || cfg.Server.Role == model.SvcRoleAll

	slog.Info("启动对话机器人...", "mode", cfg.Server.Mode, "role", cfg.Server.Role)

	// Token 签名密钥校验
	if cfg.Server.Mode == model.ModeCluster && cfg.Server.TokenSecret == "" {
		fmt.Fprintf(os.Stderr, "错误: cluster 模式下必须配置 server.token_secret（多节点共享 HMAC 签名密钥）\n")
		fmt.Fprintf(os.Stderr, "请在 cfg.yml 的 server 段中添加 token_secret 字段，例如:\n")
		fmt.Fprintf(os.Stderr, "  server:\n")
		fmt.Fprintf(os.Stderr, "    token_secret: \"请使用 openssl rand -hex 32 生成随机密钥\"\n")
		os.Exit(1)
	}
	if cfg.Server.TokenSecret == "" {
		slog.Warn("未配置 server.token_secret，使用默认密钥。生产环境请务必设置自定义密钥！")
	}

	// 初始化元数据存储（根据 store.backend 配置选择后端）
	var metaStore store.MetaStore
	switch cfg.Store.Backend {
	case "mysql":
		if cfg.MySQL.DSN == "" {
			fmt.Fprintf(os.Stderr, "错误: store.backend=mysql 但 mysql.dsn 为空\n")
			os.Exit(1)
		}
		slog.Info("使用 MySQL 存储")
		ms, err := store.NewMySQLStore(cfg.MySQL.DSN)
		if err != nil {
			slog.Error("初始化 MySQL 失败", "error", err)
			os.Exit(1)
		}
		metaStore = ms
	default:
		// "sqlite" 或空
		if _, err := os.Stat("cfg.db"); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: cfg.db 不存在，请将 cfg.db.template 复制为 cfg.db 后重新启动\n")
			os.Exit(1)
		}
		slog.Info("使用 SQLite 存储")
		ss, err := store.NewSQLiteStore("cfg.db")
		if err != nil {
			slog.Error("初始化 SQLite 失败", "error", err)
			os.Exit(1)
		}
		metaStore = ss
	}
	defer metaStore.Close()

	// 从 SQLite 加载运行时配置（sys、api），YAML 值作为种子
	if err := config.LoadRuntimeConfig(metaStore, cfg); err != nil {
		slog.Error("加载运行时配置失败", "error", err)
		os.Exit(1)
	}

	// ============================================================
	// 初始化会话管理器 + 在线状态 + 文件存储
	// 单例模式：进程内存 + 本地文件系统
	// 集群模式：Redis + S3/MinIO 共享存储
	// ============================================================
	var sessionMgr *session.Manager
	var presenceStore handler.PresenceStore
	var redisClient *redisclient.Client
	var fileStore kb.FileStore
	var notifier config.ChangeNotifier

	if cfg.Server.Mode == model.ModeCluster {
		slog.Info("启动模式: 集群 (cluster)，初始化 Redis 和对象存储...")
		var err error
		redisClient, err = redisclient.New(cfg)
		if err != nil {
			slog.Error("集群模式初始化 Redis 失败", "error", err)
			os.Exit(1)
		}
		defer redisClient.Close()

		// 会话：Redis 存储
		sessionMgr = session.NewManagerWithStore(session.NewRedisStore(redisClient))

		// 在线座席：Redis 存储
		presenceStore = handler.NewRedisPresence(redisClient, metaStore)

		// 文件存储：S3/MinIO
		fileStore, err = kb.NewS3FileStore(cfg)
		if err != nil {
			slog.Error("集群模式初始化对象存储失败", "error", err)
			os.Exit(1)
		}

		// 配置变更通知：Redis Pub/Sub
		notifier = config.NewRedisNotifier(redisClient)
		defer notifier.Stop()
	} else {
		slog.Info("启动模式: 单例 (singleton)，使用进程内存 + 本地文件系统")
		// 会话：进程内存 + DB 落盘
		sessionMgr = session.NewManager(metaStore)

		// 在线座席：进程内存
		presenceStore = handler.NewMemoryPresence(metaStore)

		// 文件存储：本地文件系统
		fileStore = kb.NewLocalFileStore()

		// 配置变更通知：空操作
		notifier = &config.NoopNotifier{}
	}

	// 初始化知识库管理器
	kbManager := kb.NewManagerWithStore(cfg, metaStore, fileStore, redisClient)

	// 启动后台文档处理 worker（仅 admin 或 all 角色）
	if isAdmin {
		go kbManager.StartFileWorker()
		slog.Info("文件处理 worker 已启动 (admin role)")
	}

	// 初始化 HTTP 处理器
	h := handler.New(cfg, kbManager, sessionMgr, metaStore, presenceStore, notifier)

	// 集群模式 + chat 角色：启动配置热加载监听（admin 修改配置后自动同步）
	if isChat && !isAdmin {
		if ch := notifier.SubscribeChanges(); ch != nil {
			go func() {
				slog.Info("配置热加载监听已启动")
				for range ch {
					slog.Info("收到配置变更通知，重新加载配置...")
					if err := config.ReloadRuntimeConfig(metaStore, cfg); err != nil {
						slog.Warn("热加载运行时配置失败", "error", err)
					}
					if eng := h.GetEngine(); eng != nil {
						eng.ReloadVdbBindings()
					}
				}
				slog.Info("配置热加载监听已停止")
			}()
		}
	}

	// 设置 Gin 路由
	if !cfg.Server.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(logger.GinLogger(), gin.Recovery())
	r.Use(handler.SecurityHeaders())

	// API 调用日志中间件（记录携带 Authorization 头的请求）
	r.Use(handler.ApiCallLogMiddleware(metaStore))

	// 加载 HTML 模板（从 embed.FS）
	tmpl, err := template.ParseFS(webFS, "web/templates/*")
	if err != nil {
		slog.Error("加载模板失败", "error", err)
		os.Exit(1)
	}
	r.SetHTMLTemplate(tmpl)

	// 静态文件（从 embed.FS）
	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		slog.Error("加载静态文件失败", "error", err)
		os.Exit(1)
	}
	r.StaticFS("/static", http.FS(staticFS))

	// 免认证路由
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/login", h.Auth.LoginPage)
	r.GET("/register", h.Auth.RegisterPage)

	// 公开 API（无需认证）
	r.POST("/api/login", h.Auth.Login)
	r.POST("/api/logout", h.Auth.Logout)
	r.POST("/api/register", h.User.Register)

	// admin 实例：根路径重定向到系统管理页（保留 token 等查询参数）
	if isAdmin && !isChat {
		r.GET("/", func(c *gin.Context) {
			target := "/admin/config"
			if q := c.Request.URL.RawQuery; q != "" {
				target += "?" + q
			}
			c.Redirect(http.StatusFound, target)
		})
	}

	// ============================================================
	// 路由注册（按 server.role 区分）
	// ============================================================

	// --- 聊天页面（仅 chat 角色） ---
	if isChat {
		chatPage := r.Group("/")
		chatPage.Use(h.Auth.AuthMiddleware())
		{
			chatPage.GET("/", h.Page.Index)
			chatPage.GET("/user/api", h.Page.UserApiIndex)
		}
	}

	// --- 需要认证的 API 路由（读操作：所有角色共享，包括 admin SPA 所需） ---
	authAPI := r.Group("/api")
	authAPI.Use(h.Auth.ApiAuthMiddleware())
	{
		// 当前用户信息
		authAPI.GET("/me", h.Auth.Me)

		// 在线座席
		authAPI.GET("/agents", h.Auth.GetOnlineAgents)

		// 工作流（所有认证用户可读）
		authAPI.GET("/workflows", h.Workflow.ListPublic)
		authAPI.GET("/workflows/:id", h.Workflow.Get)

		// 系统配置（读取）
		authAPI.GET("/config", h.Config.GetConfig)

		// 服务信息
		authAPI.GET("/info", h.Config.Info)

		// 意图分类测试（所有认证用户可用）
		authAPI.POST("/classifier/test", h.Chat.TestClassifier)

		// AI Agent（所有认证用户可读写）
		authAPI.GET("/ai-agents", h.Agent.List)
		authAPI.GET("/ai-agents/public", h.Agent.ListPublic)
		authAPI.GET("/ai-agents/:id", h.Agent.Get)

		// 系统变量列表（供创建 Agent 时参考可用变量）
		authAPI.GET("/system-vars", h.Agent.ListSystemVars)

		// FAQ（所有认证用户可读）
		authAPI.GET("/faq", h.Faq.List)
		authAPI.GET("/faq/template", h.Faq.Template)
		authAPI.POST("/faq/match", h.Faq.Match)

		// 知识库（VDB）—— 读操作
		authAPI.GET("/vdb", h.Vdb.MyList)
		authAPI.GET("/vdb/pub", h.Vdb.PubList)
		authAPI.POST("/vdb/search", h.Vdb.Search)
	}

	// --- 需要认证的页面路由（所有角色共享） ---
	sharedPage := r.Group("/")
	sharedPage.Use(h.Auth.AuthMiddleware())
	{
		sharedPage.GET("/vdb/idx", h.Page.VdbIndex)
	}

	// --- 聊天 API（仅 chat 角色） ---
	if isChat {
		chatAPI := r.Group("/api")
		chatAPI.Use(h.Auth.ApiAuthMiddleware())
		{
			chatAPI.POST("/chat", h.Chat.Chat)
			chatAPI.POST("/chat/sync", h.Chat.ChatSync)
			chatAPI.GET("/chat/history", h.Chat.History)
			chatAPI.POST("/chat/clear", h.Chat.Clear)

			// Agent 写操作（读操作已在共享段）
			chatAPI.POST("/ai-agents", h.Agent.Create)
			chatAPI.PUT("/ai-agents/:id", h.Agent.Update)
			chatAPI.DELETE("/ai-agents/:id", h.Agent.Delete)
		}
	}

	// --- 管理员页面（仅 admin 角色） ---
	if isAdmin {
		adminPage := r.Group("/admin")
		adminPage.Use(h.Auth.AuthMiddleware(), h.Auth.AdminOnlyMiddleware())
		{
			adminPage.GET("/config", h.Page.ConfigIndex)
			adminPage.GET("/vdb/bind", h.Page.VdbBindIndex)
		}
	}

	// --- 管理员写 API（仅 admin 角色） ---
	if isAdmin {
		adminAPI := r.Group("/api")
		adminAPI.Use(h.Auth.ApiAuthMiddleware(), h.Auth.AdminOnlyMiddleware())
		{
			adminAPI.PUT("/config", h.Config.UpdateConfig)
			adminAPI.POST("/config/test-models", h.Config.TestModels)

			// csm 业务知识库绑定
			adminAPI.GET("/vdb/bindings", h.Vdb.BindingGet)
			adminAPI.PUT("/vdb/bindings", h.Vdb.BindingPut)

			// FAQ 管理（写操作）
			adminAPI.POST("/faq", h.Faq.Create)
			adminAPI.POST("/faq/upload", h.Faq.Upload)
			adminAPI.PUT("/faq/:id", h.Faq.Update)
			adminAPI.DELETE("/faq/:id", h.Faq.Delete)
			adminAPI.DELETE("/faq", h.Faq.ClearAll)

			// 用户管理
			adminAPI.GET("/users", h.User.ListUsers)
			adminAPI.POST("/users", h.User.CreateUser)
			adminAPI.DELETE("/users/:name", h.User.DeleteUser)
			adminAPI.PUT("/users/:name/reset-pwd", h.User.ResetUserPwd)

			// 工作流管理（写操作）
			adminAPI.POST("/workflows", h.Workflow.Create)
			adminAPI.PUT("/workflows/:id", h.Workflow.Update)
			adminAPI.DELETE("/workflows/:id", h.Workflow.Delete)

			// 知识库（VDB）—— 写操作
			adminAPI.POST("/vdb", h.Vdb.Create)
			adminAPI.DELETE("/vdb/:id", h.Vdb.Delete)
			adminAPI.PUT("/vdb/:id/default", h.Vdb.SetDefault)
			adminAPI.GET("/vdb/:id/files", h.Vdb.FileList)
			adminAPI.POST("/vdb/:id/upload", h.Vdb.Upload)
			adminAPI.GET("/vdb/file/:id/progress", h.Vdb.ProcessInfo)
			adminAPI.GET("/vdb/file/:id/chunks", h.Vdb.Chunks)
			adminAPI.GET("/vdb/file/:id/download", h.Vdb.Download)
			adminAPI.DELETE("/vdb/file/:id", h.Vdb.FileDelete)
		}
	}

	// --- 用户自助 API（所有角色共享） ---
	userAPI := r.Group("/api/user")
	userAPI.Use(h.Auth.ApiAuthMiddleware())
	{
		userAPI.PUT("/password", h.User.ChangePassword)
		userAPI.GET("/tokens", h.User.ListMyTokens)
		userAPI.POST("/token", h.User.GenerateToken)
		userAPI.GET("/call-logs", h.User.MyCallLogs)
	}

	// 优雅退出
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("正在关闭服务...")
		kbManager.StopFileWorker()
		sessionMgr.Stop()
		if redisClient != nil {
			redisClient.Close()
		}
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("服务启动", "addr", addr, "url", fmt.Sprintf("http://localhost%s", addr))
	if err := r.Run(addr); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}
