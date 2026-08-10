package handler

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeaders 添加安全相关的 HTTP 响应头
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// HSTS: 强制浏览器使用 HTTPS（max-age=1年）
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// 禁止 MIME 类型嗅探
		c.Header("X-Content-Type-Options", "nosniff")
		// 禁止被嵌入 iframe（防 clickjacking）
		c.Header("X-Frame-Options", "DENY")
		// 限制浏览器对 XSS 过滤器的行为
		c.Header("X-XSS-Protection", "0")
		// Referrer 策略：仅同源发送完整 Referer
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 基本 CSP：允许本站 + 内联样式/脚本（模板渲染需要）
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com; script-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")

		c.Next()
	}
}
