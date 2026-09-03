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

	slog.Info("main_startup_info", "mode", cfg.Server.Mode, "role", cfg.Server.Role)

	// Token 签名密钥校验
	if cfg.Server.Mode == model.ModeCluster && cfg.Server.TokenSecret == "" {
		fmt.Fprintf(os.Stderr, "错误: cluster 模式下必须配置 server.token_secret（多节点共享 HMAC 签名密钥）\n")
		fmt.Fprintf(os.Stderr, "请在 cfg.yml 的 server 段中添加 token_secret 字段，例如:\n")
		fmt.Fprintf(os.Stderr, "  server:\n")
		fmt.Fprintf(os.Stderr, "    token_secret: \"请使用 openssl rand -hex 32 生成随机密钥\"\n")
		os.Exit(1)
	}
	if cfg.Server.TokenSecret == "" {
		slog.Warn("main_token_secret_default_warning")
	}

	// 初始化元数据存储（根据 store.backend 配置选择后端）
	var metaStore store.MetaStore
	switch cfg.Store.Backend {
	case "mysql":
		if cfg.MySQL.DSN == "" {
			fmt.Fprintf(os.Stderr, "错误: store.backend=mysql 但 mysql.dsn 为空\n")
			os.Exit(1)
		}
		slog.Info("main_using_mysql_store")
		ms, err := store.NewMySQLStore(cfg.MySQL.DSN)
		if err != nil {
			slog.Error("main_mysql_init_failed", "error", err)
			os.Exit(1)
		}
		metaStore = ms
	default:
		// "sqlite" 或空
		if _, err := os.Stat("cfg.db"); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "错误: cfg.db 不存在，请将 cfg.db.template 复制为 cfg.db 后重新启动\n")
			os.Exit(1)
		}
		slog.Info("main_using_sqlite_store")
		ss, err := store.NewSQLiteStore("cfg.db")
		if err != nil {
			slog.Error("main_sqlite_init_failed", "error", err)
			os.Exit(1)
		}
		metaStore = ss
	}
	defer metaStore.Close()

	// 从 SQLite 加载运行时配置（sys、api），YAML 值作为种子
	if err := config.LoadRuntimeConfig(metaStore, cfg); err != nil {
		slog.Error("main_runtime_config_load_failed", "error", err)
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
		slog.Info("main_cluster_mode_init")
		var err error
		redisClient, err = redisclient.New(cfg)
		if err != nil {
			slog.Error("main_cluster_redis_init_failed", "error", err)
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
			slog.Error("main_cluster_object_store_init_failed", "error", err)
			os.Exit(1)
		}

		// 配置变更通知：Redis Pub/Sub
		notifier = config.NewRedisNotifier(redisClient)
		defer notifier.Stop()
	} else {
		slog.Info("main_singleton_mode_init")
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
		slog.Info("main_file_worker_started")
	}

	// 初始化 HTTP 处理器
	h := handler.New(cfg, kbManager, sessionMgr, metaStore, presenceStore, notifier)

	// 集群模式 + chat 角色：启动配置热加载监听（admin 修改配置后自动同步）
	if isChat && !isAdmin {
		if ch := notifier.SubscribeChanges(); ch != nil {
			go func() {
				slog.Info("main_config_hot_reload_started")
				for range ch {
					slog.Info("main_config_change_received")
					if err := config.ReloadRuntimeConfig(metaStore, cfg); err != nil {
						slog.Warn("main_config_hot_reload_failed", "error", err)
					}
					if eng := h.GetEngine(); eng != nil {
						eng.ReloadVdbBindings()
					}
				}
				slog.Info("main_config_hot_reload_stopped")
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
		slog.Error("main_template_load_failed", "error", err)
		os.Exit(1)
	}
	r.SetHTMLTemplate(tmpl)

	// 静态文件（从 embed.FS）
	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		slog.Error("main_static_files_load_failed", "error", err)
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

	// ============================================================
	// 前端页面路由（Cookie 认证，无 Cookie 重定向到 /login）
	// ============================================================

	// 聊天页面（仅 chat 角色）
	if isChat {
		chatPage := r.Group("/")
		chatPage.Use(h.Auth.AuthMiddleware())
		{
			chatPage.GET("/", h.Page.Index)
			chatPage.GET("/user/api", h.Page.UserApiIndex)
		}
	}

	// 知识库页面（所有角色共享）
	sharedPage := r.Group("/")
	sharedPage.Use(h.Auth.AuthMiddleware())
	{
		sharedPage.GET("/vdb/idx", h.Page.VdbIndex)
	}

	// 管理员页面（仅 admin 角色）
	if isAdmin {
		adminPage := r.Group("/admin")
		adminPage.Use(h.Auth.AuthMiddleware(), h.Auth.AdminOnlyMiddleware())
		{
			adminPage.GET("/config", h.Page.ConfigIndex)
			adminPage.GET("/vdb/bind", h.Page.VdbBindIndex)
		}
	}

	// ============================================================
	// /api/v1/ — 前端专用（仅 httpOnly Cookie 认证）
	// 受 sys.api_auth 开关控制，按 server.role 区分路由
	// ============================================================
	v1 := r.Group("/api/v1")
	v1.Use(h.Auth.CookieApiAuthMiddleware())
	{
		// --- 共享读操作（所有角色） ---
		v1.GET("/me", h.Auth.Me)
		v1.GET("/agents", h.Auth.GetOnlineAgents)

		// 工作流（所有认证用户可读）
		v1.GET("/workflows", h.Workflow.ListPublic)
		v1.GET("/workflows/:id", h.Workflow.Get)

		// 系统配置（读取）
		v1.GET("/config", h.Config.GetConfig)

		// 服务信息
		v1.GET("/info", h.Config.Info)

		// 意图分类测试（所有认证用户可用）
		v1.POST("/classifier/test", h.Chat.TestClassifier)

		// AI Agent（读）
		v1.GET("/ai-agents", h.Agent.List)
		v1.GET("/ai-agents/public", h.Agent.ListPublic)
		v1.GET("/ai-agents/:id", h.Agent.Get)

		// 系统变量列表
		v1.GET("/system-vars", h.Agent.ListSystemVars)

		// FAQ（读）
		v1.GET("/faq", h.Faq.List)
		v1.GET("/faq/template", h.Faq.Template)
		v1.POST("/faq/match", h.Faq.Match)

		// 知识库（VDB）—— 读操作
		v1.GET("/vdb", h.Vdb.MyList)
		v1.GET("/vdb/pub", h.Vdb.PubList)
		v1.POST("/vdb/search", h.Vdb.Search)
	}

	// --- 聊天 API（仅 chat 角色） ---
	if isChat {
		chatAPI := v1.Group("")
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

	// --- 管理员写 API（仅 admin 角色） ---
	if isAdmin {
		adminAPI := v1.Group("")
		adminAPI.Use(h.Auth.AdminOnlyMiddleware())
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

	// 用户自助 API（所有角色共享）
	v1User := v1.Group("/user")
	{
		v1User.PUT("/password", h.User.ChangePassword)
		v1User.GET("/tokens", h.User.ListMyTokens)
		v1User.POST("/token", h.User.GenerateToken)
		v1User.GET("/call-logs", h.User.MyCallLogs)
	}

	// ============================================================
	// /open_api/ — 第三方专用（Authorization: Bearer <token> 认证）
	// 始终要求有效 token，不受 sys.api_auth 开关影响
	// 按 server.role 区分路由（与 /api/v1/ 保持一致）
	// ============================================================
	openAPI := r.Group("/open_api")
	openAPI.Use(h.Auth.BearerAuthMiddleware())
	{
		// --- 共享读操作（所有角色） ---
		openAPI.GET("/me", h.Auth.Me)
		openAPI.GET("/agents", h.Auth.GetOnlineAgents)
		openAPI.GET("/workflows", h.Workflow.ListPublic)
		openAPI.GET("/workflows/:id", h.Workflow.Get)
		openAPI.GET("/config", h.Config.GetConfig)
		openAPI.GET("/info", h.Config.Info)
		openAPI.POST("/classifier/test", h.Chat.TestClassifier)
		openAPI.GET("/ai-agents", h.Agent.List)
		openAPI.GET("/ai-agents/public", h.Agent.ListPublic)
		openAPI.GET("/ai-agents/:id", h.Agent.Get)
		openAPI.GET("/system-vars", h.Agent.ListSystemVars)
		openAPI.GET("/faq", h.Faq.List)
		openAPI.GET("/faq/template", h.Faq.Template)
		openAPI.POST("/faq/match", h.Faq.Match)
		openAPI.GET("/vdb", h.Vdb.MyList)
		openAPI.GET("/vdb/pub", h.Vdb.PubList)
		openAPI.POST("/vdb/search", h.Vdb.Search)
	}

	// --- open_api 聊天 API（仅 chat 角色） ---
	if isChat {
		openAPIChat := openAPI.Group("")
		{
			openAPIChat.POST("/chat", h.Chat.Chat)
			openAPIChat.POST("/chat/sync", h.Chat.ChatSync)
			openAPIChat.GET("/chat/history", h.Chat.History)
			openAPIChat.POST("/chat/clear", h.Chat.Clear)

			// Agent 写操作
			openAPIChat.POST("/ai-agents", h.Agent.Create)
			openAPIChat.PUT("/ai-agents/:id", h.Agent.Update)
			openAPIChat.DELETE("/ai-agents/:id", h.Agent.Delete)
		}
	}

	// --- open_api 管理员 API（仅 admin 角色） ---
	if isAdmin {
		openAPIAdmin := openAPI.Group("")
		openAPIAdmin.Use(h.Auth.AdminOnlyMiddleware())
		{
			openAPIAdmin.PUT("/config", h.Config.UpdateConfig)
			openAPIAdmin.POST("/config/test-models", h.Config.TestModels)
			openAPIAdmin.GET("/vdb/bindings", h.Vdb.BindingGet)
			openAPIAdmin.PUT("/vdb/bindings", h.Vdb.BindingPut)
			openAPIAdmin.POST("/faq", h.Faq.Create)
			openAPIAdmin.POST("/faq/upload", h.Faq.Upload)
			openAPIAdmin.PUT("/faq/:id", h.Faq.Update)
			openAPIAdmin.DELETE("/faq/:id", h.Faq.Delete)
			openAPIAdmin.DELETE("/faq", h.Faq.ClearAll)
			openAPIAdmin.GET("/users", h.User.ListUsers)
			openAPIAdmin.POST("/users", h.User.CreateUser)
			openAPIAdmin.DELETE("/users/:name", h.User.DeleteUser)
			openAPIAdmin.PUT("/users/:name/reset-pwd", h.User.ResetUserPwd)
			openAPIAdmin.POST("/workflows", h.Workflow.Create)
			openAPIAdmin.PUT("/workflows/:id", h.Workflow.Update)
			openAPIAdmin.DELETE("/workflows/:id", h.Workflow.Delete)
			openAPIAdmin.POST("/vdb", h.Vdb.Create)
			openAPIAdmin.DELETE("/vdb/:id", h.Vdb.Delete)
			openAPIAdmin.PUT("/vdb/:id/default", h.Vdb.SetDefault)
			openAPIAdmin.GET("/vdb/:id/files", h.Vdb.FileList)
			openAPIAdmin.POST("/vdb/:id/upload", h.Vdb.Upload)
			openAPIAdmin.GET("/vdb/file/:id/progress", h.Vdb.ProcessInfo)
			openAPIAdmin.GET("/vdb/file/:id/chunks", h.Vdb.Chunks)
			openAPIAdmin.GET("/vdb/file/:id/download", h.Vdb.Download)
			openAPIAdmin.DELETE("/vdb/file/:id", h.Vdb.FileDelete)
		}
	}

	// open_api 用户自助 API（所有角色共享）
	openAPIUser := openAPI.Group("/user")
	{
		openAPIUser.PUT("/password", h.User.ChangePassword)
		openAPIUser.GET("/tokens", h.User.ListMyTokens)
		openAPIUser.POST("/token", h.User.GenerateToken)
		openAPIUser.GET("/call-logs", h.User.MyCallLogs)
	}

	// 优雅退出
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("main_shutting_down")
		kbManager.StopFileWorker()
		sessionMgr.Stop()
		if redisClient != nil {
			redisClient.Close()
		}
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("main_server_started", "addr", addr, "url", fmt.Sprintf("http://localhost%s", addr))
	if err := r.Run(addr); err != nil {
		slog.Error("main_server_start_failed", "error", err)
		os.Exit(1)
	}
}
