package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kb-chat-flow/internal/engine"
	"kb-chat-flow/internal/kb"
	"kb-chat-flow/internal/llm"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/session"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// ChatHandler 聊天处理器
type ChatHandler struct {
	cfg        *model.Config
	kbMgr      *kb.Manager
	sessionMgr *session.Manager
	llmClient  *llm.Client
	store      store.MetaStore
	engine     *engine.Engine
	faqHandler *FaqHandler
}

// NewChatHandler 创建聊天处理器
func NewChatHandler(cfg *model.Config, kbMgr *kb.Manager, sessionMgr *session.Manager, metaStore store.MetaStore, faqHandler *FaqHandler, eng *engine.Engine) *ChatHandler {
	llmClient := llm.New(
		cfg.API.LLMAPIURI,
		cfg.API.LLMAPIKey,
		cfg.API.LLMModelName,
	)
	llmClient.SetParams(cfg.LLM.Temperature, cfg.LLM.TopP, cfg.LLM.MaxTokens)

	return &ChatHandler{
		cfg:        cfg,
		kbMgr:      kbMgr,
		sessionMgr: sessionMgr,
		llmClient:  llmClient,
		store:      metaStore,
		engine:     eng,
		faqHandler: faqHandler,
	}
}

// resolveUID 根据认证来源决定 UID
// open_api (Bearer): 强制使用 token 中的用户名，不接受请求体中的 uid
// 前端 (Cookie): api_auth 开启时强制使用 token uid，关闭时优先使用请求体中的 uid
func (h *ChatHandler) resolveUID(c *gin.Context, reqUID string) string {
	if isBearerAuth(c) {
		// open_api: 始终使用 token 中的用户名
		return getAuthUID(c)
	}
	if h.cfg.Sys.ApiAuth {
		// API 认证开启时，强制使用 token 解析的 uid
		return getAuthUID(c)
	}
	// API 认证关闭时，优先使用请求中的 uid，fallback 为 token uid
	if reqUID != "" {
		return reqUID
	}
	return getAuthUID(c)
}

// ChatSync 非流式聊天接口 POST /api/chat/sync
func (h *ChatHandler) ChatSync(c *gin.Context) {
	var req model.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	uid := h.resolveUID(c, req.UID)

	switch h.cfg.Sys.WorkMode {
	case model.WorkModeCSM:
		h.chatSyncWithCSM(c, &req, uid)
	case model.WorkModeDynamic:
		h.chatSyncWithDynamic(c, &req, uid)
	default:
		h.chatSyncWithKB(c, &req, uid)
	}
}

// chatSyncWithKB 知识库问答模式同步版本
func (h *ChatHandler) chatSyncWithKB(c *gin.Context, req *model.ChatRequest, uid string) {
	history := h.sessionMgr.GetHistory(uid)
	historyStr := session.FormatHistory(history)

	faqThreshold := h.cfg.Faq.MatchThreshold
	if h.faqHandler != nil && h.faqHandler.GetFaqCount() > 0 {
		faqAnswer, faqScore, err := h.faqHandler.MatchFaq(req.Msg, faqThreshold)
		if err == nil && faqAnswer != "" {
			h.sessionMgr.AddMessage(uid, "user", req.Msg)
			h.sessionMgr.AddMessage(uid, "assistant", faqAnswer)
			c.JSON(http.StatusOK, gin.H{
				"answer": faqAnswer,
				"source": "faq",
				"score":  faqScore,
			})
			return
		}
	}

	curDate := time.Now().Format("2006-01-02")
	curWeek := getWeekdayCN(time.Now().Weekday())

	contextStr := h.kbMgr.SearchAllKBs(req.Msg, uid, h.cfg.KB.TopK, h.cfg.KB.ScoreThreshold)

	promptTemplate := h.getPromptTemplate()
	systemPrompt := buildPrompt(promptTemplate, contextStr, historyStr, req.Msg, curDate, curWeek)

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	answer, err := h.llmClient.Chat(systemPrompt, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "LLM 调用失败: " + err.Error()})
		return
	}

	h.sessionMgr.AddMessage(uid, "assistant", answer)
	c.JSON(http.StatusOK, gin.H{"answer": answer, "source": "kb"})
}

// chatSyncWithCSM CSM 工作流同步版本
func (h *ChatHandler) chatSyncWithCSM(c *gin.Context, req *model.ChatRequest, uid string) {
	history := h.sessionMgr.GetHistory(uid)
	historyMsgs := make([]engine.ChatMsg, len(history))
	for i, msg := range history {
		historyMsgs[i] = engine.ChatMsg{Role: msg.Role, Content: msg.Content}
	}

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	answer, err := h.engine.Execute(0, req.Msg, uid, historyMsgs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工作流执行失败: " + err.Error()})
		return
	}

	h.sessionMgr.AddMessage(uid, "assistant", answer)
	c.JSON(http.StatusOK, gin.H{"answer": answer, "source": "csm"})
}

// chatSyncWithDynamic 动态工作流同步版本
func (h *ChatHandler) chatSyncWithDynamic(c *gin.Context, req *model.ChatRequest, uid string) {
	history := h.sessionMgr.GetHistory(uid)
	historyMsgs := make([]engine.ChatMsg, len(history))
	for i, msg := range history {
		historyMsgs[i] = engine.ChatMsg{Role: msg.Role, Content: msg.Content}
	}

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	workflowID := h.cfg.Sys.DefaultWorkflowID
	answer, err := h.engine.Execute(workflowID, req.Msg, uid, historyMsgs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "工作流执行失败: " + err.Error()})
		return
	}

	h.sessionMgr.AddMessage(uid, "assistant", answer)
	c.JSON(http.StatusOK, gin.H{"answer": answer, "source": "dynamic"})
}

// Chat 处理聊天请求，SSE 流式返回
func (h *ChatHandler) Chat(c *gin.Context) {
	var req model.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	uid := h.resolveUID(c, req.UID)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "不支持流式传输"})
		return
	}

	switch h.cfg.Sys.WorkMode {
	case model.WorkModeCSM:
		h.chatWithCSMWorkflow(c, &req, uid, flusher)
	case model.WorkModeDynamic:
		h.chatWithDynamicWorkflow(c, &req, uid, flusher)
	default:
		h.chatWithKB(c, &req, uid, flusher)
	}
}

// chatWithKB 知识库问答模式（FAQ 匹配 → 知识库检索 → LLM 对话）
func (h *ChatHandler) chatWithKB(c *gin.Context, req *model.ChatRequest, uid string, flusher http.Flusher) {
	history := h.sessionMgr.GetHistory(uid)
	historyStr := session.FormatHistory(history)

	faqThreshold := h.cfg.Faq.MatchThreshold
	if h.faqHandler != nil && h.faqHandler.GetFaqCount() > 0 {
		faqAnswer, faqScore, err := h.faqHandler.MatchFaq(req.Msg, faqThreshold)
		if err == nil && faqAnswer != "" {
			slog.Info("chat_faq_matched", "uid", uid, "query", req.Msg[:min(50, len(req.Msg))], "score", faqScore)
			h.sessionMgr.AddMessage(uid, "user", req.Msg)
			fmt.Fprintf(c.Writer, "data: \n\n")
			flusher.Flush()
			fmt.Fprintf(c.Writer, "data: %s\n\n", faqAnswer)
			flusher.Flush()
			fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
			flusher.Flush()
			h.sessionMgr.AddMessage(uid, "assistant", faqAnswer)
			return
		}
	}

	curDate := time.Now().Format("2006-01-02")
	curWeek := getWeekdayCN(time.Now().Weekday())

	contextStr := h.kbMgr.SearchAllKBs(req.Msg, uid, h.cfg.KB.TopK, h.cfg.KB.ScoreThreshold)

	promptTemplate := h.getPromptTemplate()
	systemPrompt := buildPrompt(promptTemplate, contextStr, historyStr, req.Msg, curDate, curWeek)

	slog.Info("chat_kb_start", "uid", uid, "query", req.Msg[:min(50, len(req.Msg))], "context_len", len(contextStr))

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	chunkCh, errCh := h.llmClient.ChatStream(systemPrompt, "")

	var fullResponse strings.Builder

	fmt.Fprintf(c.Writer, "data: \n\n")
	flusher.Flush()

	for chunk := range chunkCh {
		fullResponse.WriteString(chunk)
		fmt.Fprintf(c.Writer, "data: %s\n\n", chunk)
		flusher.Flush()
	}

	select {
	case err := <-errCh:
		if err != nil {
			slog.Error("chat_llm_error", "error", err)
			fmt.Fprintf(c.Writer, "data: [错误] %v\n\n", err)
			flusher.Flush()
		}
	default:
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	responseText := fullResponse.String()
	if responseText != "" {
		h.sessionMgr.AddMessage(uid, "assistant", responseText)
	}
}

// chatWithCSMWorkflow CSM 硬编码工作流模式
func (h *ChatHandler) chatWithCSMWorkflow(c *gin.Context, req *model.ChatRequest, uid string, flusher http.Flusher) {
	history := h.sessionMgr.GetHistory(uid)
	historyMsgs := make([]engine.ChatMsg, len(history))
	for i, msg := range history {
		historyMsgs[i] = engine.ChatMsg{Role: msg.Role, Content: msg.Content}
	}

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	slog.Info("chat_workflow_csm_start", "uid", uid, "query", req.Msg[:min(50, len(req.Msg))])

	fmt.Fprintf(c.Writer, "data: \n\n")
	flusher.Flush()

	eventCh := h.engine.ExecuteStreamCSM(0, req.Msg, uid, historyMsgs)

	var fullResponse strings.Builder
	for evt := range eventCh {
		switch evt.Type {
		case "progress":
			fmt.Fprintf(c.Writer, "data: [步骤 %d/%d] %s\n\n", evt.Step, evt.Total, evt.Agent)
			flusher.Flush()
		case "chunk":
			fullResponse.WriteString(evt.Content)
			fmt.Fprintf(c.Writer, "data: %s\n\n", evt.Content)
			flusher.Flush()
		case "error":
			slog.Error("chat_workflow_csm_error", "error", evt.Content)
			fmt.Fprintf(c.Writer, "data: [错误] %s\n\n", evt.Content)
			flusher.Flush()
		case "done":
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	responseText := fullResponse.String()
	if responseText != "" {
		h.sessionMgr.AddMessage(uid, "assistant", responseText)
	}
}

// chatWithDynamicWorkflow 动态加载数据库工作流配置模式
func (h *ChatHandler) chatWithDynamicWorkflow(c *gin.Context, req *model.ChatRequest, uid string, flusher http.Flusher) {
	history := h.sessionMgr.GetHistory(uid)
	historyMsgs := make([]engine.ChatMsg, len(history))
	for i, msg := range history {
		historyMsgs[i] = engine.ChatMsg{Role: msg.Role, Content: msg.Content}
	}

	h.sessionMgr.AddMessage(uid, "user", req.Msg)

	workflowID := h.cfg.Sys.DefaultWorkflowID
	slog.Info("chat_workflow_dynamic_start", "uid", uid, "workflow", workflowID, "query", req.Msg[:min(50, len(req.Msg))])

	fmt.Fprintf(c.Writer, "data: \n\n")
	flusher.Flush()

	eventCh := h.engine.ExecuteStream(workflowID, req.Msg, uid, historyMsgs)

	var fullResponse strings.Builder
	for evt := range eventCh {
		switch evt.Type {
		case "progress":
			fmt.Fprintf(c.Writer, "data: [步骤 %d/%d] %s\n\n", evt.Step, evt.Total, evt.Agent)
			flusher.Flush()
		case "chunk":
			fullResponse.WriteString(evt.Content)
			fmt.Fprintf(c.Writer, "data: %s\n\n", evt.Content)
			flusher.Flush()
		case "error":
			slog.Error("chat_workflow_dynamic_error", "error", evt.Content)
			fmt.Fprintf(c.Writer, "data: [错误] %s\n\n", evt.Content)
			flusher.Flush()
		case "done":
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	responseText := fullResponse.String()
	if responseText != "" {
		h.sessionMgr.AddMessage(uid, "assistant", responseText)
	}
}

// TestClassifier 意图分类测试接口 POST /api/classifier/test
func (h *ChatHandler) TestClassifier(c *gin.Context) {
	var req struct {
		Text       string `json:"text" binding:"required"`
		WorkflowID int64  `json:"workflow_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if req.WorkflowID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workflow_id 不能为空"})
		return
	}

	workflow, err := h.store.GetWorkflow(req.WorkflowID)
	if err != nil || workflow == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "工作流不存在"})
		return
	}

	if workflow.Classifier == nil || len(workflow.Classifier.Categories) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该工作流没有配置意图分类器"})
		return
	}

	tiers, final := engine.ClassifyWithDetails(workflow.Classifier, req.Text, h.llmClient, h.engine.EmbClient(), h.engine.FtPredictor())

	var totalMS int64
	for _, t := range tiers {
		totalMS += t.Elapsed
	}

	c.JSON(http.StatusOK, gin.H{
		"tiers":    tiers,
		"final":    final,
		"total_ms": totalMS,
	})
}

// History 获取当前用户的历史消息 GET /api/chat/history
func (h *ChatHandler) History(c *gin.Context) {
	uid := getAuthUID(c)
	history := h.sessionMgr.GetHistory(uid)
	c.JSON(http.StatusOK, gin.H{"data": history})
}

// Clear 清空会话
func (h *ChatHandler) Clear(c *gin.Context) {
	uid := getAuthUID(c)
	h.sessionMgr.Clear(uid)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ============================================================
// 辅助函数
// ============================================================

func buildPrompt(template, context, history, question, curDate, curWeek string) string {
	result := template
	result = strings.ReplaceAll(result, "{context}", context)
	result = strings.ReplaceAll(result, "{history}", history)
	result = strings.ReplaceAll(result, "{question}", question)
	result = strings.ReplaceAll(result, "{cur_date}", curDate)
	result = strings.ReplaceAll(result, "{cur_week}", curWeek)
	return result
}

func getWeekdayCN(d time.Weekday) string {
	days := []string{"日", "一", "二", "三", "四", "五", "六"}
	return days[d]
}

func (h *ChatHandler) getPromptTemplate() string {
	if h.store != nil {
		prompt, err := h.store.GetPrompt("chat_msg")
		if err == nil && prompt != "" {
			return prompt
		}
	}
	return store.DefaultChatPrompt()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
