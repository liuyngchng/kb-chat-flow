package handler

import (
	"net/http"

	"kb-chat-flow/internal/model"

	"github.com/gin-gonic/gin"
)

// PageHandler 页面处理器
type PageHandler struct {
	cfg *model.Config
}

// NewPageHandler 创建页面处理器
func NewPageHandler(cfg *model.Config) *PageHandler {
	return &PageHandler{cfg: cfg}
}

// pageTitle 根据服务角色生成页面标题
func (h *PageHandler) pageTitle() string {
	name := h.cfg.Sys.Name
	if h.cfg.Server.Role == model.SvcRoleAdmin {
		return name + "系统管理"
	}
	return name
}

// getUserInfo 从 context 中提取用户信息
func getUserInfo(c *gin.Context) (string, int) {
	userVal, exists := c.Get("user")
	if !exists {
		return "default", model.RoleNormal
	}
	user, ok := userVal.(*model.User)
	if !ok {
		return "default", model.RoleNormal
	}
	return user.UserName, user.Role
}

// Index 聊天主页面
func (h *PageHandler) Index(c *gin.Context) {
	uid, role := getUserInfo(c)
	token := GetTokenStr(c)

	c.HTML(http.StatusOK, "index.html", gin.H{
		"sys_name":   h.cfg.Sys.Name,
		"page_title": h.pageTitle(),
		"uid":        uid,
		"role":       role,
		"token":      token,
	})
}

// VdbIndex 知识库管理页面
func (h *PageHandler) VdbIndex(c *gin.Context) {
	uid, role := getUserInfo(c)
	token := GetTokenStr(c)

	c.HTML(http.StatusOK, "vdb.html", gin.H{
		"sys_name":   h.cfg.Sys.Name,
		"page_title": h.pageTitle(),
		"uid":        uid,
		"role":       role,
		"token":      token,
	})
}

// VdbBindIndex 业务知识库绑定页面
func (h *PageHandler) VdbBindIndex(c *gin.Context) {
	uid, role := getUserInfo(c)
	token := GetTokenStr(c)

	c.HTML(http.StatusOK, "vdb_bind.html", gin.H{
		"sys_name":   h.cfg.Sys.Name,
		"page_title": h.pageTitle(),
		"uid":        uid,
		"role":       role,
		"token":      token,
	})
}

// UserApiIndex API用户管理页面
func (h *PageHandler) UserApiIndex(c *gin.Context) {
	uid, _ := getUserInfo(c)
	token := GetTokenStr(c)

	c.HTML(http.StatusOK, "user_api.html", gin.H{
		"sys_name":   h.cfg.Sys.Name,
		"page_title": h.pageTitle(),
		"uid":        uid,
		"token":      token,
	})
}

// ConfigIndex 系统配置页面
func (h *PageHandler) ConfigIndex(c *gin.Context) {
	uid, role := getUserInfo(c)
	token := GetTokenStr(c)

	c.HTML(http.StatusOK, "config.html", gin.H{
		"sys_name":   h.cfg.Sys.Name,
		"page_title": h.pageTitle(),
		"uid":        uid,
		"role":       role,
		"token":      token,
	})
}
