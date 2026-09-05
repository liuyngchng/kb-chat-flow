package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"kb-chat-flow/internal/engine"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// AgentHandler AI Agent 管理处理器
type AgentHandler struct {
	store store.MetaStore
}

// NewAgentHandler 创建 Agent 处理器
func NewAgentHandler(metaStore store.MetaStore) *AgentHandler {
	return &AgentHandler{store: metaStore}
}

// ListSystemVars 返回系统变量列表 GET /api/system-vars
func (h *AgentHandler) ListSystemVars(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": engine.GetSystemVars()})
}

// ListPublic 返回公开的 Agent 列表（仅 id + name，聊天页下拉用）
func (h *AgentHandler) ListPublic(c *gin.Context) {
	agents, err := h.store.ListAgents()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取智能体列表失败: " + err.Error()})
		return
	}

	type pubAgent struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	result := make([]pubAgent, 0, len(agents))
	for _, a := range agents {
		result = append(result, pubAgent{ID: a.ID, Name: a.Name})
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// List 管理员查看所有 Agent（完整信息）
func (h *AgentHandler) List(c *gin.Context) {
	agents, err := h.store.ListAgents()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取智能体列表失败: " + err.Error()})
		return
	}

	if agents == nil {
		agents = []model.AgentDef{}
	}

	c.JSON(http.StatusOK, gin.H{"data": agents})
}

// Get 获取单个 Agent 详情
func (h *AgentHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	agent, err := h.store.GetAgent(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取智能体失败: " + err.Error()})
		return
	}
	if agent == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "智能体不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": agent})
}

// validateSystemPrompt 校验提示词中的系统变量引用
func validateSystemPrompt(prompt string) error {
	if invalid := engine.ValidateTemplateVars(prompt); len(invalid) > 0 {
		slog.Warn("agent_system_prompt_invalid_vars", "invalid", invalid)
		return fmt.Errorf("system_prompt 包含非法的系统变量：%s", strings.Join(invalid, "、"))
	}
	return nil
}

// Create 创建 Agent
func (h *AgentHandler) Create(c *gin.Context) {
	var req model.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if err := validateSystemPrompt(req.SystemPrompt); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 序列化 VdbIDs 为 JSON 字符串
	vdbIDsJSON := "[]"
	if len(req.VdbIDs) > 0 {
		data, _ := json.Marshal(req.VdbIDs)
		vdbIDsJSON = string(data)
	}

	agent := &model.AgentDef{
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		ModelName:    req.ModelName,
		Temperature:  req.Temperature,
		TopP:         req.TopP,
		MaxTokens:    req.MaxTokens,
		VdbIDs:       vdbIDsJSON,
	}

	id, err := h.store.CreateAgent(agent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建智能体失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": id})
}

// Update 更新 Agent
func (h *AgentHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	// 确认存在
	existing, err := h.store.GetAgent(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取智能体失败: " + err.Error()})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "智能体不存在"})
		return
	}

	var req model.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if err := validateSystemPrompt(req.SystemPrompt); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	vdbIDsJSON := "[]"
	if len(req.VdbIDs) > 0 {
		data, _ := json.Marshal(req.VdbIDs)
		vdbIDsJSON = string(data)
	}

	agent := &model.AgentDef{
		ID:           id,
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		ModelName:    req.ModelName,
		Temperature:  req.Temperature,
		TopP:         req.TopP,
		MaxTokens:    req.MaxTokens,
		VdbIDs:       vdbIDsJSON,
	}

	if err := h.store.UpdateAgent(agent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新智能体失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Delete 删除 Agent
func (h *AgentHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return
	}

	if err := h.store.DeleteAgent(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除智能体失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
