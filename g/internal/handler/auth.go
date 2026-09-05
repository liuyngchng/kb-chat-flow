package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// defaultTokenSecret 默认 HMAC 签名密钥（cfg.yml 未配置时使用）
var defaultTokenSecret = []byte("kb-chat-flow_secret_2026")

// token 有效期 2 小时
const tokenTTL = 2 * time.Hour

// 登录限流配置
const (
	loginMaxFailures     = 5                // IP 最多连续失败次数
	loginLockDuration    = 15 * time.Minute // 锁定时长
	loginFailuresCleanup = 15 * time.Minute // 过期失败纪录清理间隔
)

// Cookie 名称
const cookieAuthToken = "auth_token"

// authSourceKey context key 用于区分认证来源
const authSourceKey = "auth_source"

// authSource 常量
const (
	authSourceCookie = "cookie"
	authSourceBearer = "bearer"
)

// loginFailRecord 登录失败记录
type loginFailRecord struct {
	count       int
	lockedUntil time.Time
}

// getTokenSecret 获取当前 token 签名密钥
func (h *AuthHandler) getTokenSecret() []byte {
	if h.cfg.Server.TokenSecret != "" {
		return []byte(h.cfg.Server.TokenSecret)
	}
	return defaultTokenSecret
}

// AuthHandler 认证处理器
type AuthHandler struct {
	cfg            *model.Config
	store          store.MetaStore
	presence       PresenceStore
	tokenBlacklist sync.Map // key: tokenSignature (string), value: expiry (time.Time)
	loginFailures  sync.Map // key: clientIP (string), value: *loginFailRecord
	startCleanup   sync.Once
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(cfg *model.Config, metaStore store.MetaStore, presence PresenceStore) *AuthHandler {
	return &AuthHandler{
		cfg:      cfg,
		store:    metaStore,
		presence: presence,
	}
}

// startBlacklistCleanup 启动后台清理过期黑名单条目和登录失败记录的 goroutine
func (h *AuthHandler) startBlacklistCleanup() {
	h.startCleanup.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := time.Now()
				h.tokenBlacklist.Range(func(key, value interface{}) bool {
					expiry, ok := value.(time.Time)
					if ok && now.After(expiry) {
						h.tokenBlacklist.Delete(key)
					}
					return true
				})
				h.loginFailures.Range(func(key, value interface{}) bool {
					rec, ok := value.(*loginFailRecord)
					if ok && now.After(rec.lockedUntil) && rec.count >= loginMaxFailures {
						h.loginFailures.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

// OnlineAgent 在线座席信息
type OnlineAgent struct {
	UserName  string `json:"user_name"`
	LoginTime string `json:"login_time"`
	Note      string `json:"note"`
}

// LoginPage 登录页面
func (h *AuthHandler) LoginPage(c *gin.Context) {
	pageTitle := h.cfg.Sys.Name
	if h.cfg.Server.Role == model.SvcRoleAdmin {
		pageTitle = h.cfg.Sys.Name + "系统管理"
	}

	c.HTML(http.StatusOK, "login.html", gin.H{
		"page_title":       pageTitle,
		"error_msg":        "",
		"debug":            h.cfg.Server.Debug,
		"register_enabled": true,
	})
}

// RegisterPage 注册页面
func (h *AuthHandler) RegisterPage(c *gin.Context) {
	pageTitle := h.cfg.Sys.Name
	if h.cfg.Server.Role == model.SvcRoleAdmin {
		pageTitle = h.cfg.Sys.Name + "系统管理"
	}

	msg := c.Query("msg")
	c.HTML(http.StatusOK, "register.html", gin.H{
		"page_title": pageTitle,
		"msg":        msg,
	})
}

// generateToken 生成 HMAC 签名 token
// 格式: base64(user_name|expiry_timestamp|hmac_signature)
func generateToken(userName string, role int, expiry time.Time, secret []byte) string {
	expiryUnix := strconv.FormatInt(expiry.Unix(), 10)
	payload := fmt.Sprintf("%s|%d|%s", userName, role, expiryUnix)

	// HMAC-SHA256 签名
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	full := fmt.Sprintf("%s|%s", payload, sig)
	return base64.RawURLEncoding.EncodeToString([]byte(full))
}

// parseToken 解析并验证 token，返回 user 或 nil
func (h *AuthHandler) parseToken(tokenStr string) *model.User {
	// Base64 解码
	data, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil
	}

	parts := strings.SplitN(string(data), "|", 4)
	if len(parts) != 4 {
		return nil
	}

	userName := parts[0]
	role, _ := strconv.Atoi(parts[1])
	expiryUnix := parts[2]
	sig := parts[3]

	// 检查 token 是否在黑名单中（已注销）
	if _, blacklisted := h.tokenBlacklist.Load(sig); blacklisted {
		return nil
	}

	// 检查过期
	expiry, err := strconv.ParseInt(expiryUnix, 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return nil
	}

	// 验证签名
	payload := fmt.Sprintf("%s|%d|%s", userName, role, expiryUnix)
	mac := hmac.New(sha256.New, h.getTokenSecret())
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil
	}

	return &model.User{
		UserName: userName,
		Role:     role,
	}
}

// Login 处理登录请求（JSON）
func (h *AuthHandler) Login(c *gin.Context) {
	clientIP := clientIP(c)

	var req model.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Warn("auth_login_parse_failed", "ip", clientIP, "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if req.UserName == "" || req.UserPwd == "" {
		slog.Warn("auth_login_params_empty", "ip", clientIP)
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码不能为空"})
		return
	}

	// 登录限流：检查是否被锁定
	h.startBlacklistCleanup()
	if locked := h.isLoginLocked(clientIP); locked {
		slog.Warn("auth_login_rate_limited", "ip", clientIP, "user_name", req.UserName)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "登录失败次数过多，请稍后再试"})
		return
	}

	// 按用户名查询用户
	user, err := h.store.GetUserByLogin(req.UserName)
	if err != nil {
		slog.Error("auth_login_query_user_failed", "ip", clientIP, "user_name", req.UserName, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败: " + err.Error()})
		return
	}

	// 认证失败统一提示（防用户名枚举），但后端日志区分具体原因
	if user == nil {
		h.recordLoginFailure(clientIP)
		slog.Warn("auth_login_user_not_found", "ip", clientIP, "user_name", req.UserName)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	if !store.VerifyPassword(req.UserPwd, user.UserPwd) {
		h.recordLoginFailure(clientIP)
		slog.Warn("auth_login_wrong_password", "ip", clientIP, "user_name", req.UserName)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	// 登录成功，清除失败记录
	h.clearLoginFailures(clientIP)

	// 检查密码是否过期（仅 SQLite 单机版种子 admin 有此字段）
	if !user.PwdExpiresAt.IsZero() && time.Now().After(user.PwdExpiresAt) {
		slog.Warn("auth_login_password_expired", "ip", clientIP, "user_name", req.UserName)
		c.JSON(http.StatusForbidden, gin.H{"error": "密码已过期，请联系管理员重置"})
		return
	}
	mustChangePwd := !user.PwdExpiresAt.IsZero()

	// admin 实例：仅管理员可登录
	if h.cfg.Server.Role == model.SvcRoleAdmin && user.Role != model.RoleAdmin {
		slog.Warn("auth_login_non_admin_access", "ip", clientIP, "user_name", req.UserName, "role", user.Role)
		c.JSON(http.StatusForbidden, gin.H{"error": "此账号无法访问管理后台"})
		return
	}

	expiry := time.Now().Add(tokenTTL)
	token := generateToken(user.UserName, user.Role, expiry, h.getTokenSecret())

	// 设置 httpOnly + Secure + SameSite=Strict Cookie
	setAuthCookie(c, token, int(tokenTTL.Seconds()))

	// 如果是客服座席，加入在线列表
	if user.Role == model.RoleAgent {
		h.presence.SetPresence(user.UserName, time.Now())
	}

	slog.Info("auth_login_success", "ip", clientIP, "user_name", user.UserName, "role", user.Role)

	c.JSON(http.StatusOK, gin.H{
		"status":          "ok",
		"token":           token,
		"user_name":       user.UserName,
		"role":            user.Role,
		"must_change_pwd": mustChangePwd,
	})
}

// Logout 处理注销
func (h *AuthHandler) Logout(c *gin.Context) {
	// 解析 token 获取用户信息，然后加入黑名单
	if tokenStr := extractToken(c); tokenStr != "" {
		// 先解析 token 获取用户（此时还未加入黑名单）
		user := h.parseToken(tokenStr)
		if user != nil {
			h.presence.RemovePresence(user.UserName)
		}

		// 将 token 签名加入黑名单，使其立即失效
		data, err := base64.RawURLEncoding.DecodeString(tokenStr)
		if err == nil {
			parts := strings.SplitN(string(data), "|", 4)
			if len(parts) == 4 {
				expiryUnix, _ := strconv.ParseInt(parts[2], 10, 64)
				expiry := time.Unix(expiryUnix, 0)
				h.tokenBlacklist.Store(parts[3], expiry)
				h.startBlacklistCleanup()
			}
		}
	}

	// 清除 Cookie
	clearAuthCookie(c)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// setAuthCookie 设置 httpOnly + Secure(仅HTTPS) + SameSite=Strict 的认证 Cookie
func setAuthCookie(c *gin.Context, token string, maxAge int) {
	// 判断请求是否经过 HTTPS（直连 TLS 或通过 nginx 反代）
	secure := c.Request.TLS != nil ||
		c.GetHeader("X-Forwarded-Proto") == "https" ||
		c.GetHeader("X-Forwarded-Scheme") == "https"

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieAuthToken,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearAuthCookie 清除认证 Cookie
func clearAuthCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieAuthToken,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// 浏览器用户：仅从 httpOnly Cookie 读取 token
func extractTokenFromCookie(c *gin.Context) string {
	token, err := c.Cookie(cookieAuthToken)
	if err == nil && token != "" {
		return token
	}
	return ""
}

// 第三方调用：仅从 Authorization: Bearer xxx 读取 token
func extractTokenFromBearer(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// extractToken 兼容旧版：Cookie 优先，Bearer 兜底
// 用于登出、Me 等需要同时支持两种场景的接口
func extractToken(c *gin.Context) string {
	if token := extractTokenFromCookie(c); token != "" {
		return token
	}
	return extractTokenFromBearer(c)
}

// AuthMiddleware 认证中间件：验证 token
func (h *AuthHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		user := h.parseToken(tokenStr)
		if user == nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		c.Set("user", user)
		c.Set("token_str", tokenStr)
		c.Next()
	}
}

// GetTokenStr 从 context 提取原始 token 字符串
func GetTokenStr(c *gin.Context) string {
	if ts, exists := c.Get("token_str"); exists {
		if s, ok := ts.(string); ok {
			return s
		}
	}
	return ""
}

// AdminOnlyMiddleware 仅允许管理员访问
func (h *AuthHandler) AdminOnlyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userVal, exists := c.Get("user")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "禁止访问"})
			c.Abort()
			return
		}

		user, ok := userVal.(*model.User)
		if !ok || user.Role != model.RoleAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "仅管理员可访问"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// CookieApiAuthMiddleware /api/v1/ 接口认证中间件：仅从 httpOnly Cookie 读取 token
// 受 sys.api_auth 开关控制：关闭时跳过认证，开启时必须有有效 token
func (h *AuthHandler) CookieApiAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(authSourceKey, authSourceCookie)

		tokenStr := extractTokenFromCookie(c)
		if tokenStr != "" {
			if user := h.parseToken(tokenStr); user != nil {
				c.Set("user", user)
				c.Set("token_str", tokenStr)
			}
		}

		// 接口认证关闭时，跳过认证检查
		if !h.cfg.Sys.ApiAuth {
			c.Next()
			return
		}

		// 接口认证开启时，必须提供有效 token
		if _, exists := c.Get("user"); !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// BearerAuthMiddleware /open_api/ 接口认证中间件：仅从 Authorization: Bearer 头读取 token
// 作为产品级 API，始终要求有效 token，不受 sys.api_auth 开关影响
func (h *AuthHandler) BearerAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(authSourceKey, authSourceBearer)

		tokenStr := extractTokenFromBearer(c)
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供认证 token，请使用 Authorization: Bearer <token>"})
			c.Abort()
			return
		}

		user := h.parseToken(tokenStr)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "token 无效或已过期"})
			c.Abort()
			return
		}

		c.Set("user", user)
		c.Set("token_str", tokenStr)
		c.Next()
	}
}

// ApiAuthMiddleware 兼容旧版 API 认证中间件：Cookie 优先，Bearer 兜底
// 保留给 /api/ 路径（过渡期），受 sys.api_auth 开关控制
func (h *AuthHandler) ApiAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 始终尝试从请求中提取 token，设置 user（后续 AdminOnlyMiddleware 等依赖此值）
		tokenStr := extractToken(c)
		if tokenStr != "" {
			if user := h.parseToken(tokenStr); user != nil {
				c.Set("user", user)
				c.Set("token_str", tokenStr)
			}
		}

		// 接口认证关闭时，跳过认证检查（但 user 已设置）
		if !h.cfg.Sys.ApiAuth {
			c.Next()
			return
		}

		// 接口认证开启时，必须提供有效 token
		if _, exists := c.Get("user"); !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供认证 token"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetOnlineAgents 获取在线座席列表
func (h *AuthHandler) GetOnlineAgents(c *gin.Context) {
	agents := h.presence.GetOnlineAgents()
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

// Me 返回当前登录用户信息（从 Cookie 或 Authorization header 解析 token）
func (h *AuthHandler) Me(c *gin.Context) {
	tokenStr := extractToken(c)
	if tokenStr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	user := h.parseToken(tokenStr)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token 无效或已过期"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_name": user.UserName,
		"role":      user.Role,
	})
}

// ============================================================
// 登录限流（IP 级别）
// ============================================================

// clientIP 从请求中提取客户端 IP
func clientIP(c *gin.Context) string {
	// 优先 X-Forwarded-For（nginx 反代时设置）
	if fwd := c.GetHeader("X-Forwarded-For"); fwd != "" {
		ip := strings.SplitN(fwd, ",", 2)[0]
		return strings.TrimSpace(ip)
	}
	// 其次 X-Real-IP
	if realIP := c.GetHeader("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}
	// 回退到直连 IP
	host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	return host
}

// isLoginLocked 检查 IP 是否被锁定
func (h *AuthHandler) isLoginLocked(ip string) bool {
	val, ok := h.loginFailures.Load(ip)
	if !ok {
		return false
	}
	rec := val.(*loginFailRecord)
	if rec.count >= loginMaxFailures && time.Now().Before(rec.lockedUntil) {
		return true
	}
	return false
}

// recordLoginFailure 记录一次登录失败
func (h *AuthHandler) recordLoginFailure(ip string) {
	now := time.Now()
	val, _ := h.loginFailures.LoadOrStore(ip, &loginFailRecord{})
	rec := val.(*loginFailRecord)
	rec.count++
	if rec.count >= loginMaxFailures {
		rec.lockedUntil = now.Add(loginLockDuration)
	}
}

// clearLoginFailures 清除 IP 的登录失败记录
func (h *AuthHandler) clearLoginFailures(ip string) {
	h.loginFailures.Delete(ip)
}
