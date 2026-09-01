package handler

import (
	"kb-chat-flow/internal/config"
	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/engine"
	"kb-chat-flow/internal/kb"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/session"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// getAuthUID 从认证上下文中获取用户名作为 uid
func getAuthUID(c *gin.Context) string {
	userVal, exists := c.Get("user")
	if !exists {
		return "default"
	}
	user, ok := userVal.(*model.User)
	if !ok {
		return "default"
	}
	return user.UserName
}

// isBearerAuth 判断当前请求是否来自 open_api (Bearer 认证)
func isBearerAuth(c *gin.Context) bool {
	source, exists := c.Get(authSourceKey)
	if !exists {
		return false
	}
	return source == "bearer"
}

// Handler 聚合所有处理器
type Handler struct {
	Page     *PageHandler
	Chat     *ChatHandler
	Vdb      *VdbHandler
	Config   *ConfigHandler
	Auth     *AuthHandler
	User     *UserHandler
	Agent    *AgentHandler
	Workflow *WorkflowHandler
	Faq      *FaqHandler
	engine   *engine.Engine
}

// GetEngine 返回工作流引擎（供 main.go 热加载使用）
func (h *Handler) GetEngine() *engine.Engine {
	return h.engine
}

// New 创建处理器
func New(cfg *model.Config, kbMgr *kb.Manager, sessionMgr *session.Manager, metaStore store.MetaStore, presence PresenceStore, notifier config.ChangeNotifier) *Handler {
	embClient := embedding.New(
		cfg.API.EmbeddingAPIURI,
		cfg.API.EmbeddingAPIKey,
		cfg.API.EmbeddingModelName,
	)

	faqHandler := NewFaqHandler(metaStore, embClient)

	// Token 密钥
	tokenSecret := getTokenSecretBytes(cfg)

	// 共享 engine 实例：聊天执行 + 知识库绑定热加载用同一个
	eng := engine.NewEngine(cfg, kbMgr, metaStore)

	vdbHandler := NewVdbHandler(cfg, kbMgr, metaStore, eng)
	vdbHandler.SetNotifier(notifier)

	return &Handler{
		Page:     NewPageHandler(cfg),
		Chat:     NewChatHandler(cfg, kbMgr, sessionMgr, metaStore, faqHandler, eng),
		Vdb:      vdbHandler,
		Config:   NewConfigHandler(cfg, metaStore, notifier),
		Auth:     NewAuthHandler(cfg, metaStore, presence),
		User:     NewUserHandler(metaStore, tokenSecret),
		Agent:    NewAgentHandler(metaStore),
		Workflow: NewWorkflowHandler(metaStore),
		Faq:      faqHandler,
		engine:   eng,
	}
}

// getTokenSecretBytes 从配置获取 token 密钥
func getTokenSecretBytes(cfg *model.Config) []byte {
	if cfg.Server.TokenSecret != "" {
		return []byte(cfg.Server.TokenSecret)
	}
	return []byte("kb-chat-flow_secret_2026")
}
