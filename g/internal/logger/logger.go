package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

var (
	// Logger 全局 logger 实例
	Logger *slog.Logger
)

// SourceWidth 源码位置显示宽度（右对齐用）。
// 按最坏情况计算：5 层缩写目录(9) + 文件名≤10 + ":"+4位行号(5) = 24
const SourceWidth = 24

// modulePrefix 模块名前缀，从完整函数名中剥离
const modulePrefix = "kb-chat-flow/"

// customHandler 自定义日志格式
// 格式: 2026-07-25 09:39:40.638 INFO [manager.go:431] 消息内容 key=value ...
type customHandler struct {
	w      io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

func (h *customHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *customHandler) Handle(_ context.Context, r slog.Record) error {
	// 1. 时间
	buf := []byte(r.Time.Format("2006-01-02 15:04:05.000"))

	// 2. 级别（补齐到 5 字符，保证对齐）
	buf = append(buf, ' ')
	buf = append(buf, padLevel(r.Level.String())...)

	// 3. 源码位置（包路径/文件名:行号，固定宽度右对齐）
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			src := &slog.Source{Function: f.Function, File: f.File, Line: f.Line}
			loc := sourceLocation(src)
			buf = append(buf, ' ')
			buf = append(buf, '[')
			// 固定宽度右对齐，超长不截断（避免丢失分包信息）
			if len(loc) < SourceWidth {
				buf = append(buf, bytes.Repeat([]byte{' '}, SourceWidth-len(loc))...)
			}
			buf = append(buf, loc...)
			buf = append(buf, ']')
		}
	}

	// 4. 消息
	buf = append(buf, ' ')
	buf = append(buf, r.Message...)

	// 5. 属性 (key=value)
	r.Attrs(func(a slog.Attr) bool {
		buf = append(buf, ' ')
		buf = append(buf, a.Key...)
		buf = append(buf, '=')
		buf = append(buf, a.Value.String()...)
		return true
	})

	buf = append(buf, '\n')

	_, err := h.w.Write(buf)
	return err
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{
		w:     h.w,
		level: h.level,
		attrs: append(h.attrs, attrs...),
	}
}

// sourceLocation 从 slog.Source 生成 "包路径/文件名:行号"。
// 例如 Source{Function: "kb-chat-flow/internal/kb.(*Manager).handleFile", File: ".../manager.go", Line: 399}
// 输出: "internal/kb/manager.go:399"
func sourceLocation(src *slog.Source) string {
	// 去掉模块前缀 "kb-chat-flow/"
	pkg := strings.TrimPrefix(src.Function, modulePrefix)

	// 提取包路径：
	//   - 方法：pkg 形如 "internal/kb.(*Manager).handleFile"，接收者括号 '(' 前的部分是包路径（含尾点）
	//   - 函数：pkg 形如 "main.classify"，无括号，取最后一个 '.' 前的部分
	if i := strings.Index(pkg, "("); i > 0 {
		pkg = pkg[:i]
	} else if i := strings.LastIndex(pkg, "."); i > 0 {
		pkg = pkg[:i]
	}
	// 去掉包路径尾部的 '.'（如 "internal/kb." → "internal/kb"）
	pkg = strings.TrimSuffix(pkg, ".")

	// 包路径首字母缩写（每段取首字母）+ 文件名（basename）+ 行号
	// 例如 "internal/kb" → "i/k"，输出 "i/k/manager.go:399"
	return abbreviatePath(pkg) + "/" + filepath.Base(src.File) + ":" + strconv.Itoa(src.Line)
}

// abbreviatePath 将包路径每段缩写为首字母，用 '/' 连接。
// 例如 "internal/kb" → "i/k"；"main" → "m"。
func abbreviatePath(pkg string) string {
	segs := strings.Split(pkg, "/")
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		// 取每个 UTF-8 字符的首字符（避免中文包名被截坏）
		for _, r := range seg {
			segs[i] = string(r)
			break
		}
	}
	return strings.Join(segs, "/")
}

// padLevel 将日志级别补齐到 5 字符，保证对齐
func padLevel(level string) string {
	if len(level) < 5 {
		return level + strings.Repeat(" ", 5-len(level))
	}
	return level
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{
		w:      h.w,
		level:  h.level,
		attrs:  h.attrs,
		groups: append(h.groups, name),
	}
}

// Init 初始化日志，同时输出到控制台和按小时滚动的日志文件
func Init(debug bool) error {
	// 按小时滚动的日志 writer
	// 生成文件: app.2026-07-30-14.log, app.log 为当前文件的软链接
	fileWriter, err := rotatelogs.New(
		"log/app.%Y-%m-%d-%H.log",
		rotatelogs.WithLinkName("log/app.log"),
		rotatelogs.WithRotationTime(time.Hour),
		rotatelogs.WithMaxAge(7*24*time.Hour), // 保留 7 天
	)
	if err != nil {
		return err
	}

	// 控制台 + 文件双输出
	multiWriter := io.MultiWriter(os.Stdout, fileWriter)

	// 日志级别
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	// 使用自定义格式 handler
	handler := &customHandler{w: multiWriter, level: level}

	Logger = slog.New(handler)

	// 设置为默认 logger
	slog.SetDefault(Logger)

	// 将 Gin 的日志输出重定向到统一 writer
	gin.DefaultWriter = multiWriter
	gin.DefaultErrorWriter = multiWriter

	return nil
}

// GinLogger 返回使用 slog 的 Gin 请求日志中间件
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		statusCode := c.Writer.Status()
		// 只记录 4xx/5xx 错误
		if statusCode < 400 {
			return
		}

		latency := time.Since(start)
		method := c.Request.Method
		clientIP := c.ClientIP()

		level := slog.LevelWarn
		if statusCode >= 500 {
			level = slog.LevelError
		}

		slog.LogAttrs(c.Request.Context(), level,
			"GIN",
			slog.Int("status", statusCode),
			slog.String("method", method),
			slog.String("path", path),
			slog.String("query", query),
			slog.String("ip", clientIP),
			slog.Duration("latency", latency),
			slog.Int("size", c.Writer.Size()),
		)
	}
}
