package handler

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// tokenTTLAPI API token 有效期 2 小时
const tokenTTLAPI = 2 * time.Hour

// UserHandler 用户管理处理器
type UserHandler struct {
	store       store.MetaStore
	tokenSecret []byte
}

// NewUserHandler 创建用户管理处理器
func NewUserHandler(s store.MetaStore, tokenSecret []byte) *UserHandler {
	return &UserHandler{store: s, tokenSecret: tokenSecret}
}

// ============================================================
// 用户管理（admin only）
// ============================================================

// ListUsers 获取所有用户 GET /api/users
func (h *UserHandler) ListUsers(c *gin.Context) {
	users, err := h.store.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if users == nil {
		users = []model.User{}
	}
	// 不返回密码
	type userResp struct {
		UID      int64  `json:"uid"`
		UserName string `json:"user_name"`
		Role     int    `json:"role"`
		Note     string `json:"note"`
	}
	resp := make([]userResp, len(users))
	for i, u := range users {
		resp[i] = userResp{u.UID, u.UserName, u.Role, u.Note}
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// CreateUser 创建用户 POST /api/users
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req model.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if err := store.ValidatePassword(req.UserPwd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pwdHash, err := store.HashPassword(req.UserPwd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码哈希失败"})
		return
	}
	if err := h.store.CreateUser(req.UserName, pwdHash, req.Role, req.Note); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DeleteUser 删除用户 DELETE /api/users/:name
func (h *UserHandler) DeleteUser(c *gin.Context) {
	userName := c.Param("name")
	if userName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名不能为空"})
		return
	}

	// 不允许删除自己
	currentUser := getAuthUID(c)
	if userName == currentUser {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除自己"})
		return
	}

	if err := h.store.DeleteUserByName(userName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败: " + err.Error()})
		return
	}

	// 同时删除其 API tokens
	// (由外键级联或手动清理，SQLite 无外键所以忽略即可，tokens 按过期时间自动失效)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ResetUserPwd 重置用户密码 PUT /api/users/:name/reset-pwd
func (h *UserHandler) ResetUserPwd(c *gin.Context) {
	userName := c.Param("name")

	var req model.ResetPwdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if err := store.ValidatePassword(req.UserPwd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pwdHash, err := store.HashPassword(req.UserPwd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码哈希失败"})
		return
	}
	if err := h.store.ResetPassword(userName, pwdHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重置密码失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ============================================================
// 修改密码（所有用户）
// ============================================================

// ChangePassword 修改自己的密码 PUT /api/user/password
func (h *UserHandler) ChangePassword(c *gin.Context) {
	userName := getAuthUID(c)

	var req model.ChangePwdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if req.NewPwd == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能为空"})
		return
	}

	if err := store.ValidatePassword(req.NewPwd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 先从数据库取得当前用户，验证旧密码
	user, err := h.store.GetUserByName(userName)
	if err != nil || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取用户信息失败"})
		return
	}
	if !store.VerifyPassword(req.OldPwd, user.UserPwd) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "旧密码不正确"})
		return
	}

	newPwdHash, err := store.HashPassword(req.NewPwd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码哈希失败"})
		return
	}

	if err := h.store.UpdatePassword(userName, newPwdHash); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "修改密码失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ============================================================
// API Token 管理（RoleAPI 用户）
// ============================================================

// ListMyTokens 查看我的 API token GET /api/user/tokens
func (h *UserHandler) ListMyTokens(c *gin.Context) {
	userName := getAuthUID(c)
	tokens, err := h.store.GetUserApiTokens(userName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tokens == nil {
		tokens = []model.ApiToken{}
	}

	// 标记快过期的 token（10 分钟内）
	type tokenResp struct {
		ID           int64  `json:"id"`
		TokenPreview string `json:"token_preview"`
		ExpiresAt    string `json:"expires_at"`
		ExpiringSoon bool   `json:"expiring_soon"`
		CreateTime   string `json:"create_time"`
	}
	resp := make([]tokenResp, len(tokens))
	now := time.Now()
	for i, t := range tokens {
		resp[i] = tokenResp{
			ID:           t.ID,
			TokenPreview: t.TokenPreview,
			ExpiresAt:    t.ExpiresAt.Format("2006-01-02 15:04:05"),
			ExpiringSoon: t.ExpiresAt.Sub(now) < 10*time.Minute,
			CreateTime:   t.CreateTime.Format("2006-01-02 15:04:05"),
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GenerateToken 生成新的 API token POST /api/user/token
func (h *UserHandler) GenerateToken(c *gin.Context) {
	userName := getAuthUID(c)

	// 从 context 获取 role
	userVal, _ := c.Get("user")
	user, _ := userVal.(*model.User)
	role := model.RoleAPI
	if user != nil {
		role = user.Role
	}

	expiry := time.Now().Add(tokenTTLAPI)
	token := generateToken(userName, role, expiry, h.tokenSecret)

	// 保存到数据库
	preview := token[:16]
	if len(token) > 16 {
		preview = token[:16]
	}
	if err := h.store.SaveApiToken(userName, preview, expiry); err != nil {
		slog.Error("保存 API token 失败", "error", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"token":      token,
		"expires_at": expiry.Format("2006-01-02 15:04:05"),
	})
}

// ============================================================
// API 调用日志（RoleAPI 用户）
// ============================================================

// MyCallLogs 查看 API 调用记录 GET /api/user/call-logs
func (h *UserHandler) MyCallLogs(c *gin.Context) {
	userName := getAuthUID(c)
	logs, err := h.store.GetUserApiCallLogs(userName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if logs == nil {
		logs = []model.ApiCallLog{}
	}
	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// ============================================================
// API 调用日志中间件
// ============================================================

// ApiCallLogMiddleware 记录 API 调用的中间件
func ApiCallLogMiddleware(store store.MetaStore) gin.HandlerFunc {
	// 敏感路径：请求体不应记录到日志
	sensitivePaths := map[string]bool{
		"/api/login":         true,
		"/api/user/password": true,
		"/api/users":         true, // 创建/重置密码也在此路径
	}

	return func(c *gin.Context) {
		// 仅记录携带 API token 的请求
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.Next()
			return
		}

		userName := ""
		if userVal, exists := c.Get("user"); exists {
			if u, ok := userVal.(*model.User); ok {
				userName = u.UserName
			}
		}
		if userName == "" {
			c.Next()
			return
		}

		// 读取请求体
		reqBody := ""
		if c.Request.Body != nil {
			bodyBytes, _ := io.ReadAll(c.Request.Body)
			reqBody = string(bodyBytes)
			// 恢复 body 供后续 handler 读取
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
		// 敏感路径不记录请求体
		if sensitivePaths[c.Request.URL.Path] {
			reqBody = "[敏感数据已脱敏]"
		} else if len(reqBody) > 1000 {
			reqBody = reqBody[:1000] + "..."
		}

		// 包装 ResponseWriter 以捕获响应
		rw := &responseCapturer{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = rw

		c.Next()

		// 记录日志（异步）
		respBody := rw.body.String()
		if len(respBody) > 1000 {
			respBody = respBody[:1000] + "..."
		}
		statusCode := c.Writer.Status()
		errMsg := ""
		if statusCode >= 400 {
			errMsg = respBody
		}

		go func() {
			if err := store.SaveApiCallLog(userName, c.Request.URL.Path, c.Request.Method,
				reqBody, respBody, statusCode, errMsg); err != nil {
				slog.Error("保存 API 调用日志失败", "error", err)
			}
		}()
	}
}

// responseCapturer 包装 ResponseWriter 捕获响应体
type responseCapturer struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseCapturer) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseCapturer) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}
