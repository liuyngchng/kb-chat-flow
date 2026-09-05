package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"kb-chat-flow/internal/config"
	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/llm"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/rerank"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// ConfigHandler 系统配置 API 处理器
type ConfigHandler struct {
	cfg      *model.Config
	store    store.MetaStore
	notifier config.ChangeNotifier
}

// MaxChunkOverlapRatio overlap 允许占 chunkSize 的最大比例（百分比）
// overlap 必须严格小于 chunkSize，否则 splitText 切分步长为 0 会死循环。
// 这里进一步限制为 chunkSize 的一定比例，避免过度重叠浪费 embedding 计算。
const MaxChunkOverlapRatio = 30

// NewConfigHandler 创建配置处理器
func NewConfigHandler(cfg *model.Config, metaStore store.MetaStore, notifier config.ChangeNotifier) *ConfigHandler {
	return &ConfigHandler{
		cfg:      cfg,
		store:    metaStore,
		notifier: notifier,
	}
}

// ConfigResponse 配置响应结构
type ConfigResponse struct {
	Sys    SysConfigResp    `json:"sys"`
	API    APIConfigResp    `json:"api"`
	Prompt PromptConfigResp `json:"prompt"`
	KB     KBConfigResp     `json:"kb"`
	LLM    LLMParamsResp    `json:"llm"`
	Faq    FaqConfigResp    `json:"faq"`
}

type SysConfigResp struct {
	Name              string `json:"name"`
	Auth              string `json:"auth"`
	ApiAuth           string `json:"api_auth"`
	WorkMode          int    `json:"work_mode"`
	DefaultWorkflowID int64  `json:"default_workflow_id"`
}

type APIConfigResp struct {
	LLMAPIURI          string `json:"llm_api_uri"`
	LLMAPIKey          string `json:"llm_api_key"`
	LLMModelName       string `json:"llm_model_name"`
	EmbeddingAPIURI    string `json:"embedding_api_uri"`
	EmbeddingAPIKey    string `json:"embedding_api_key"`
	EmbeddingModelName string `json:"embedding_model_name"`
	RerankAPIURI       string `json:"rerank_api_uri"`
	RerankAPIKey       string `json:"rerank_api_key"`
	RerankModelName    string `json:"rerank_model_name"`
}

type PromptConfigResp struct {
	ChatMsg string `json:"chat_msg"`
}

type KBConfigResp struct {
	ChunkSize       int     `json:"chunk_size"`
	ChunkOverlap    int     `json:"chunk_overlap"`
	TopK            int     `json:"top_k"`
	ScoreThreshold  float64 `json:"score_threshold"`
	RerankEnabled   bool    `json:"rerank_enabled"`
	RerankRetrieveN int     `json:"rerank_retrieve_n"`
}

type LLMParamsResp struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`
}

type FaqConfigResp struct {
	MatchThreshold float64 `json:"match_threshold"`
}

// GetConfig 获取所有配置
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	resp := ConfigResponse{
		Sys: SysConfigResp{
			Name:              h.cfg.Sys.Name,
			Auth:              boolToStr(h.cfg.Sys.Auth),
			ApiAuth:           boolToStr(h.cfg.Sys.ApiAuth),
			WorkMode:          h.cfg.Sys.WorkMode,
			DefaultWorkflowID: h.cfg.Sys.DefaultWorkflowID,
		},
		API: APIConfigResp{
			LLMAPIURI:          h.cfg.API.LLMAPIURI,
			LLMAPIKey:          h.cfg.API.LLMAPIKey,
			LLMModelName:       h.cfg.API.LLMModelName,
			EmbeddingAPIURI:    h.cfg.API.EmbeddingAPIURI,
			EmbeddingAPIKey:    h.cfg.API.EmbeddingAPIKey,
			EmbeddingModelName: h.cfg.API.EmbeddingModelName,
			RerankAPIURI:       h.cfg.API.RerankAPIURI,
			RerankAPIKey:       h.cfg.API.RerankAPIKey,
			RerankModelName:    h.cfg.API.RerankModelName,
		},
		Prompt: PromptConfigResp{
			ChatMsg: h.getPrompt(),
		},
		KB: KBConfigResp{
			ChunkSize:       h.cfg.KB.ChunkSize,
			ChunkOverlap:    h.cfg.KB.ChunkOverlap,
			TopK:            h.cfg.KB.TopK,
			ScoreThreshold:  h.cfg.KB.ScoreThreshold,
			RerankEnabled:   h.cfg.KB.RerankEnabled,
			RerankRetrieveN: h.cfg.KB.RerankRetrieveN,
		},
		LLM: LLMParamsResp{
			Temperature: h.cfg.LLM.Temperature,
			TopP:        h.cfg.LLM.TopP,
			MaxTokens:   h.cfg.LLM.MaxTokens,
		},
		Faq: FaqConfigResp{
			MatchThreshold: h.cfg.Faq.MatchThreshold,
		},
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// UpdateConfig 更新配置
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	var req ConfigResponse
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	// 更新系统配置
	if req.Sys.Name != "" {
		if err := h.store.SetConfig("sys.name", req.Sys.Name, "系统名称"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存系统名称失败: " + err.Error()})
			return
		}
	}
	// 工作模式（始终保存）
	if err := h.store.SetConfig("sys.work_mode", fmt.Sprintf("%d", req.Sys.WorkMode), "工作模式: 0=KB, 1=CSM, 2=动态工作流"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存工作模式失败: " + err.Error()})
		return
	}
	// 动态工作流 ID
	if err := h.store.SetConfig("sys.default_workflow_id", fmt.Sprintf("%d", req.Sys.DefaultWorkflowID), "动态工作流 ID"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存默认工作流失败: " + err.Error()})
		return
	}
	// sys.auth 只从 cfg.yml 读取，不允许在页面上修改
	if req.Sys.ApiAuth != "" {
		if err := h.store.SetConfig("sys.api_auth", req.Sys.ApiAuth, "是否启用接口认证"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存接口认证配置失败: " + err.Error()})
			return
		}
	}

	// 更新大模型 API 配置
	apiUpdates := map[string]string{
		"api.llm_api_uri":          req.API.LLMAPIURI,
		"api.llm_api_key":          req.API.LLMAPIKey,
		"api.llm_model_name":       req.API.LLMModelName,
		"api.embedding_api_uri":    req.API.EmbeddingAPIURI,
		"api.embedding_api_key":    req.API.EmbeddingAPIKey,
		"api.embedding_model_name": req.API.EmbeddingModelName,
		"api.rerank_api_uri":       req.API.RerankAPIURI,
		"api.rerank_api_key":       req.API.RerankAPIKey,
		"api.rerank_model_name":    req.API.RerankModelName,
	}
	for key, value := range apiUpdates {
		if value != "" {
			if err := h.store.SetConfig(key, value, ""); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置 " + key + " 失败: " + err.Error()})
				return
			}
		}
	}

	// 更新提示词
	if req.Prompt.ChatMsg != "" {
		if err := h.store.UpsertPrompt("chat_msg", req.Prompt.ChatMsg, 0); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存提示词失败: " + err.Error()})
			return
		}
	}

	// 更新知识库参数
	if req.KB.ChunkSize > 0 {
		if err := h.store.SetConfig("kb.chunk_size", fmt.Sprintf("%d", req.KB.ChunkSize), "文本分片大小"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存分片大小失败: " + err.Error()})
			return
		}
	}
	if req.KB.ChunkOverlap > 0 {
		// 校验 overlap 必须严格小于 chunkSize 的一定比例，否则文本切分可能死循环
		chunkSize := h.cfg.KB.ChunkSize
		if req.KB.ChunkSize > 0 {
			chunkSize = req.KB.ChunkSize
		}
		maxOverlap := chunkSize * MaxChunkOverlapRatio / 100
		if req.KB.ChunkOverlap >= chunkSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("分片重叠必须小于分片大小（当前 %d ≥ %d）", req.KB.ChunkOverlap, chunkSize)})
			return
		}
		if req.KB.ChunkOverlap > maxOverlap {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("分片重叠过大，最多为分片大小的 %d%%（%d），当前 %d", MaxChunkOverlapRatio, maxOverlap, req.KB.ChunkOverlap)})
			return
		}
		if err := h.store.SetConfig("kb.chunk_overlap", fmt.Sprintf("%d", req.KB.ChunkOverlap), "分片重叠大小"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存分片重叠失败: " + err.Error()})
			return
		}
	}
	if req.KB.TopK > 0 {
		if err := h.store.SetConfig("kb.top_k", fmt.Sprintf("%d", req.KB.TopK), "检索返回条数"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存返回条数失败: " + err.Error()})
			return
		}
	}
	if req.KB.ScoreThreshold > 0 {
		if err := h.store.SetConfig("kb.score_threshold", fmt.Sprintf("%.3f", req.KB.ScoreThreshold), "相似度阈值"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存阈值失败: " + err.Error()})
			return
		}
	}
	// Rerank 开关（布尔值始终保存）
	if err := h.store.SetConfig("kb.rerank_enabled", fmt.Sprintf("%v", req.KB.RerankEnabled), "是否启用 Rerank 重排序"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 rerank 开关失败: " + err.Error()})
		return
	}
	if req.KB.RerankRetrieveN > 0 {
		if err := h.store.SetConfig("kb.rerank_retrieve_n", fmt.Sprintf("%d", req.KB.RerankRetrieveN), "Rerank 预检索条数"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 rerank 预检索条数失败: " + err.Error()})
			return
		}
	}

	// 更新 LLM 参数
	if req.LLM.Temperature > 0 {
		if err := h.store.SetConfig("llm.temperature", fmt.Sprintf("%.2f", req.LLM.Temperature), "LLM 温度"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存温度参数失败: " + err.Error()})
			return
		}
	}
	if req.LLM.TopP > 0 {
		if err := h.store.SetConfig("llm.top_p", fmt.Sprintf("%.2f", req.LLM.TopP), "LLM Top-P"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Top-P 失败: " + err.Error()})
			return
		}
	}
	if req.LLM.MaxTokens > 0 {
		if err := h.store.SetConfig("llm.max_tokens", fmt.Sprintf("%d", req.LLM.MaxTokens), "LLM 最大 Token 数"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存最大 Token 数失败: " + err.Error()})
			return
		}
	}

	// 更新 FAQ 匹配阈值
	if req.Faq.MatchThreshold > 0 {
		if err := h.store.SetConfig("faq.match_threshold", fmt.Sprintf("%.3f", req.Faq.MatchThreshold), "FAQ 匹配阈值 (0~1)"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 FAQ 匹配阈值失败: " + err.Error()})
			return
		}
	}

	// 重新加载运行时配置到内存
	if err := config.ReloadRuntimeConfig(h.store, h.cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重新加载配置失败: " + err.Error()})
		return
	}

	// 通知其他节点配置已变更（集群模式下通过 Redis Pub/Sub 广播）
	if err := h.notifier.NotifyChange(); err != nil {
		slog.Warn("config_handler_notify_change_failed", "error", err)
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// TestModels 测试模型 API 连接 POST /api/config/test-models
func (h *ConfigHandler) TestModels(c *gin.Context) {
	var req APIConfigResp
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	type ModelTestResult struct {
		Name    string `json:"name"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		Elapsed int64  `json:"elapsed_ms"`
	}
	var results []ModelTestResult

	// 1. 测试 LLM 对话模型
	if req.LLMAPIURI != "" {
		t0 := time.Now()
		client := llm.New(req.LLMAPIURI, req.LLMAPIKey, req.LLMModelName)
		_, err := client.Chat("你是一个助手，请回复 OK。", "hi")
		elapsed := time.Since(t0).Milliseconds()
		if err != nil {
			slog.Warn("config_handler_model_test_llm_failed", "error", err)
			results = append(results, ModelTestResult{Name: "LLM 对话模型", OK: false, Message: err.Error(), Elapsed: elapsed})
		} else {
			results = append(results, ModelTestResult{Name: "LLM 对话模型", OK: true, Message: "连接成功", Elapsed: elapsed})
		}
	} else {
		results = append(results, ModelTestResult{Name: "LLM 对话模型", OK: false, Message: "未配置 API 地址"})
	}

	// 2. 测试 Embedding 向量模型
	if req.EmbeddingAPIURI != "" {
		t0 := time.Now()
		client := embedding.New(req.EmbeddingAPIURI, req.EmbeddingAPIKey, req.EmbeddingModelName)
		dim, err := client.Dimension()
		elapsed := time.Since(t0).Milliseconds()
		if err != nil {
			slog.Warn("config_handler_model_test_embedding_failed", "error", err)
			results = append(results, ModelTestResult{Name: "Embedding 向量模型", OK: false, Message: err.Error(), Elapsed: elapsed})
		} else {
			results = append(results, ModelTestResult{Name: "Embedding 向量模型", OK: true, Message: fmt.Sprintf("连接成功 (dim=%d)", dim), Elapsed: elapsed})
		}
	} else {
		results = append(results, ModelTestResult{Name: "Embedding 向量模型", OK: false, Message: "未配置 API 地址"})
	}

	// 3. 测试 Rerank 重排序模型
	if req.RerankAPIURI != "" {
		t0 := time.Now()
		client := rerank.New(req.RerankAPIURI, req.RerankAPIKey, req.RerankModelName)
		_, err := client.Rerank("test", []string{"hello world", "goodbye"}, 1)
		elapsed := time.Since(t0).Milliseconds()
		if err != nil {
			slog.Warn("config_handler_model_test_rerank_failed", "error", err)
			results = append(results, ModelTestResult{Name: "Rerank 重排序模型", OK: false, Message: err.Error(), Elapsed: elapsed})
		} else {
			results = append(results, ModelTestResult{Name: "Rerank 重排序模型", OK: true, Message: "连接成功", Elapsed: elapsed})
		}
	} else {
		results = append(results, ModelTestResult{Name: "Rerank 重排序模型", OK: false, Message: "未配置 API 地址"})
	}

	allOK := true
	for _, r := range results {
		if !r.OK {
			allOK = false
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results, "all_ok": allOK})
}

// getPrompt 从数据库获取提示词模板
func (h *ConfigHandler) getPrompt() string {
	if h.store != nil {
		prompt, err := h.store.GetPrompt("chat_msg")
		if err == nil && prompt != "" {
			return prompt
		}
	}
	return store.DefaultChatPrompt()
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Info 返回服务信息 GET /api/info
func (h *ConfigHandler) Info(c *gin.Context) {
	supportedFileTypes := []string{"txt", "md", "pdf", "docx", "xlsx"}

	c.JSON(http.StatusOK, gin.H{
		"name":                 h.cfg.Sys.Name,
		"version":              "1.0.0",
		"work_mode":            h.cfg.Sys.WorkMode,
		"vector_backend":       h.cfg.Vector.Backend,
		"store_backend":        h.cfg.Store.Backend,
		"supported_file_types": supportedFileTypes,
		"api_auth_enabled":     h.cfg.Sys.ApiAuth,
	})
}
