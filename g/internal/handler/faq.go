package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// FaqHandler FAQ 管理 API 处理器
type FaqHandler struct {
	store     store.MetaStore
	embClient *embedding.Client
}

// NewFaqHandler 创建 FAQ 处理器
func NewFaqHandler(metaStore store.MetaStore, embClient *embedding.Client) *FaqHandler {
	return &FaqHandler{
		store:     metaStore,
		embClient: embClient,
	}
}

// List 获取所有 FAQ 条目 GET /api/faq
func (h *FaqHandler) List(c *gin.Context) {
	entries, err := h.store.GetFaqEntries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取 FAQ 列表失败: " + err.Error()})
		return
	}
	if entries == nil {
		entries = []model.FaqEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"data": entries})
}

// faqTemplateContent FAQ 模板文件内容
const faqTemplateContent = `# FAQ 模板文件
# 格式说明：Q: 开头为问题（可多个 Q 对应同一个答案），A: 开头为答案
# 空行分隔不同的 FAQ 条目
#
# 用法：修改此文件后，在管理后台 → FAQ 管理 → 上传 FAQ 文件

Q: 如何重置密码？
Q: 忘记密码怎么办？
Q: 密码忘了
A: 您好，您可以在登录页面点击"忘记密码"，按照提示输入注册邮箱，系统会发送重置链接到您的邮箱。链接有效期为 24 小时，请及时操作。

Q: 支持哪些支付方式？
Q: 可以用微信支付吗？
Q: 能用支付宝吗？
Q: 是否支持银行卡付款？
A: 目前支持微信支付、支付宝、银联银行卡（储蓄卡及信用卡）以及 Apple Pay。单笔限额根据不同支付渠道略有差异，微信/支付宝单笔限额 50000 元，银行卡单笔限额 200000 元。

Q: 如何联系人工客服？
Q: 转人工
Q: 找客服
A: 您好，人工客服工作时间为周一至周五 9:00-18:00。您可以在公众号内回复"转人工"，或者拨打客服热线 400-XXX-XXXX。当前非工作时间，您可以先留言，我们会在下一个工作日与您联系。
`

// Template 下载 FAQ 模板文件 GET /api/faq/template
func (h *FaqHandler) Template(c *gin.Context) {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=faq_template.txt")
	c.String(http.StatusOK, faqTemplateContent)
}

// Upload 上传 FAQ 文件 POST /api/faq/upload (multipart/form-data)
// 支持 txt/md 格式，Q: 开头为问题，A: 开头为答案
func (h *FaqHandler) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择文件"})
		return
	}

	// 检查文件类型
	name := strings.ToLower(file.Filename)
	if !strings.HasSuffix(name, ".txt") && !strings.HasSuffix(name, ".md") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 txt/md 格式的 FAQ 文件"})
		return
	}

	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "打开文件失败"})
		return
	}
	defer f.Close()

	// 读取文件内容
	buf := make([]byte, file.Size)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败: " + err.Error()})
		return
	}
	content := string(buf[:n])

	// 解析 FAQ 条目
	entries := parseFaqContent(content)
	if len(entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "FAQ 文件内容为空或格式不正确"})
		return
	}

	// 批量入库 + 向量化
	created := 0
	for _, entry := range entries {
		if err := h.createFaqEntry(entry.questions, entry.answer, file.Filename); err != nil {
			slog.Error("faq_upload_create_entry_failed", "error", err)
			continue
		}
		created++
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"created": created,
		"total":   len(entries),
	})
}

// Create 创建单个 FAQ 条目 POST /api/faq
func (h *FaqHandler) Create(c *gin.Context) {
	var req model.CreateFaqRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if len(req.Questions) == 0 || req.Answer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "问题和答案不能为空"})
		return
	}

	if err := h.createFaqEntry(req.Questions, req.Answer, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建 FAQ 条目失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Update 更新 FAQ 条目 PUT /api/faq/:id
func (h *FaqHandler) Update(c *gin.Context) {
	id := getPathIntParam(c, "id")
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	var req model.UpdateFaqRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if len(req.Questions) == 0 || req.Answer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "问题和答案不能为空"})
		return
	}

	// 更新答案
	if err := h.store.UpdateFaqEntry(id, req.Answer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新 FAQ 答案失败: " + err.Error()})
		return
	}

	// 删除旧问题，重新向量化
	if err := h.store.DeleteFaqQuestionsByEntryID(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清除旧问题失败: " + err.Error()})
		return
	}

	// 为新问题计算向量并入库
	for _, q := range req.Questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		emb, err := h.embClient.EmbedSingle(q)
		if err != nil {
			slog.Warn("faq_create_question_embed_failed", "question", q[:min(30, len(q))], "error", err)
			continue
		}
		embJSON, err := json.Marshal(emb)
		if err != nil {
			continue
		}
		if _, err := h.store.CreateFaqQuestion(id, q, string(embJSON)); err != nil {
			slog.Error("faq_create_save_question_failed", "error", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Delete 删除 FAQ 条目 DELETE /api/faq/:id
func (h *FaqHandler) Delete(c *gin.Context) {
	id := getPathIntParam(c, "id")
	if id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	if err := h.store.DeleteFaqEntry(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除 FAQ 失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ClearAll 清空所有 FAQ DELETE /api/faq
func (h *FaqHandler) ClearAll(c *gin.Context) {
	if err := h.store.ClearAllFaq(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清空 FAQ 失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ============================================================
// FAQ 匹配（供 chat.go 调用）
// ============================================================

// Match FAQ 独立匹配 API POST /api/faq/match
func (h *FaqHandler) Match(c *gin.Context) {
	var req model.FaqMatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	// 从配置中读取阈值，使用默认值 0.85
	threshold := 0.85
	cfgVal, err := h.store.GetConfig("faq.match_threshold")
	if err == nil && cfgVal != "" {
		var t float64
		if _, err := fmt.Sscanf(cfgVal, "%f", &t); err == nil {
			threshold = t
		}
	}

	answer, score, err := h.MatchFaq(req.Query, threshold)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FAQ 匹配失败: " + err.Error()})
		return
	}

	matched := score >= threshold
	c.JSON(http.StatusOK, gin.H{
		"answer":  answer,
		"score":   score,
		"matched": matched,
	})
}

// MatchFaq 匹配用户问题到 FAQ，返回匹配的答案和分数
// 返回值: (答案, 匹配分数, 错误)
// 如果没有匹配（分数不够），返回 ("", 0, nil)
func (h *FaqHandler) MatchFaq(query string, threshold float64) (string, float64, error) {
	// 获取所有 FAQ 问题及其向量
	questions, err := h.store.GetAllFaqQuestionsWithEmbedding()
	if err != nil {
		return "", 0, err
	}
	if len(questions) == 0 {
		return "", 0, nil
	}

	// 计算查询向量
	queryVec, err := h.embClient.EmbedSingle(query)
	if err != nil {
		return "", 0, fmt.Errorf("FAQ 匹配: query embedding 失败: %w", err)
	}

	// 遍历所有 FAQ 问题，计算余弦相似度
	var bestScore float64
	var bestEntryID int64

	for _, q := range questions {
		if len(q.Embedding) == 0 {
			continue
		}
		score := cosineSimilarity(queryVec, q.Embedding)
		if score > bestScore {
			bestScore = score
			bestEntryID = q.EntryID
		}
	}

	if bestScore < threshold {
		return "", bestScore, nil
	}

	// 根据 entry_id 获取答案
	entries, err := h.store.GetFaqEntries()
	if err != nil {
		return "", 0, err
	}
	for _, e := range entries {
		if e.ID == bestEntryID {
			return e.Answer, bestScore, nil
		}
	}

	return "", 0, nil
}

// ============================================================
// 辅助函数
// ============================================================

// faqPair 解析 FAQ 文件时的中间结构
type faqPair struct {
	questions []string
	answer    string
}

// parseFaqContent 解析 FAQ 文件内容
func parseFaqContent(content string) []faqPair {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var pairs []faqPair
	var current *faqPair

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			// 空行结束当前条目的 Q 收集
			if current != nil && len(current.questions) > 0 && current.answer != "" {
				pairs = append(pairs, *current)
				current = nil
			}
			continue
		}

		upperLine := strings.ToUpper(trimmed)
		if strings.HasPrefix(upperLine, "Q:") || strings.HasPrefix(upperLine, "Q：") {
			q := strings.TrimSpace(trimmed[2:])
			if q == "" {
				continue
			}
			if current == nil {
				current = &faqPair{}
			}
			// 如果当前条目已有 answer，说明这是一个新的 FAQ 条目
			if current.answer != "" {
				pairs = append(pairs, *current)
				current = &faqPair{}
			}
			current.questions = append(current.questions, q)
		} else if strings.HasPrefix(upperLine, "A:") || strings.HasPrefix(upperLine, "A：") {
			a := strings.TrimSpace(trimmed[2:])
			if current == nil {
				current = &faqPair{}
			}
			current.answer = a
		}
	}

	// 最后一个未闭合的条目
	if current != nil && len(current.questions) > 0 && current.answer != "" {
		pairs = append(pairs, *current)
	}

	return pairs
}

// createFaqEntry 创建 FAQ 条目（含向量化）
func (h *FaqHandler) createFaqEntry(questions []string, answer, sourceFile string) error {
	// 创建条目
	entryID, err := h.store.CreateFaqEntry(answer, sourceFile)
	if err != nil {
		return err
	}

	// 为每个问题计算向量并入库
	for _, q := range questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}

		emb, err := h.embClient.EmbedSingle(q)
		if err != nil {
			slog.Warn("faq_update_question_embed_failed", "question", q[:min(30, len(q))], "error", err)
			continue
		}

		embJSON, err := json.Marshal(emb)
		if err != nil {
			slog.Warn("faq_embed_serialize_failed", "error", err)
			continue
		}

		if _, err := h.store.CreateFaqQuestion(entryID, q, string(embJSON)); err != nil {
			slog.Error("faq_update_save_question_failed", "error", err)
		}
	}

	return nil
}

// cosineSimilarity 计算两个向量的余弦相似度
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProd, normA, normB float64
	for i := range a {
		dotProd += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProd / (sqrtFloat(normA) * sqrtFloat(normB))
}

// sqrtFloat 简单的平方根（避免引入 math 包）
func sqrtFloat(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

// GetFaqCount 返回 FAQ 条目数量
func (h *FaqHandler) GetFaqCount() int {
	entries, err := h.store.GetFaqEntries()
	if err != nil {
		return 0
	}
	return len(entries)
}
