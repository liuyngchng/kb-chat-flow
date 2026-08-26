package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/fasttext"
	"kb-chat-flow/internal/kb"
	"kb-chat-flow/internal/llm"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"
)

// EngineEvent 工作流执行事件
type EngineEvent struct {
	Type          string `json:"type"` // "progress" | "chunk" | "done" | "error"
	Step          int    `json:"step"`
	Total         int    `json:"total"`
	Agent         string `json:"agent"`
	Content       string `json:"content"`                  // chunk 或 error 内容
	NodeID        string `json:"node_id,omitempty"`        // 节点 ID（DAG 模式）
	ParallelGroup string `json:"parallel_group,omitempty"` // 并行组名（DAG 模式）
	Error         error  `json:"-"`                        // 内部使用
}

// dagNode DAG 调度内部节点
type dagNode struct {
	Node     model.WorkflowNode
	InDegree int
	OutEdges []string // 下游节点 ID
	Level    int      // 拓扑层级
}

// Engine 工作流执行引擎
type Engine struct {
	cfg         *model.Config
	kbMgr       *kb.Manager
	store       store.MetaStore
	baseLLM     *llm.Client
	embClient   *embedding.Client
	ftPredictor *fasttext.Predictor

	// csm 各分支检索的知识库 id（从 sys_config 加载，可热更新）
	bindingLock   sync.RWMutex
	billingVdbIDs []int64
	repairVdbIDs  []int64
	faqVdbIDs     []int64
}

// NewEngine 创建引擎
func NewEngine(cfg *model.Config, kbMgr *kb.Manager, metaStore store.MetaStore) *Engine {
	llmClient := llm.New(
		cfg.API.LLMAPIURI,
		cfg.API.LLMAPIKey,
		cfg.API.LLMModelName,
	)
	llmClient.SetParams(cfg.LLM.Temperature, cfg.LLM.TopP, cfg.LLM.MaxTokens)

	embClient := embedding.New(
		cfg.API.EmbeddingAPIURI,
		cfg.API.EmbeddingAPIKey,
		cfg.API.EmbeddingModelName,
	)

	e := &Engine{
		cfg:         cfg,
		kbMgr:       kbMgr,
		store:       metaStore,
		baseLLM:     llmClient,
		embClient:   embClient,
		ftPredictor: fasttext.New(),
	}

	// 启动时从配置加载 csm 分支绑定的知识库
	e.LoadVdbBindings()

	return e
}

// ExecuteStream 执行工作流，返回事件通道。
// 非最终节点：同步执行，发 progress 事件。
// 最终节点：流式执行，发 progress + chunk 事件。
// 最后发 done 事件。
func (e *Engine) ExecuteStream(
	workflowID int64,
	userQuery string,
	uid string,
	messages []ChatMsg,
) <-chan EngineEvent {
	eventCh := make(chan EngineEvent, 50)

	go func() {
		defer close(eventCh)

		// 1. 加载工作流
		workflow, err := e.store.GetWorkflow(workflowID)
		if err != nil {
			slog.Error("load workflow failed", "workflow_id", workflowID, "error", err)
			eventCh <- EngineEvent{Type: "error", Content: "加载工作流失败: " + err.Error(), Error: err}
			return
		}
		if workflow == nil {
			slog.Error("workflow not found", "workflow_id", workflowID)
			eventCh <- EngineEvent{Type: "error", Content: "工作流不存在", Error: fmt.Errorf("workflow %d not found", workflowID)}
			return
		}
		if len(workflow.Nodes) == 0 {
			slog.Error("workflow has no nodes", "workflow", workflow.Name)
			eventCh <- EngineEvent{Type: "error", Content: "工作流没有节点", Error: fmt.Errorf("empty workflow")}
			return
		}

		slog.Info("workflow loaded", "workflow", workflow.Name, "id", workflowID, "nodes", len(workflow.Nodes))

		// 2. 排序节点（按 OrderIndex）
		nodes := workflow.Nodes
		total := len(nodes)

		// 3. 初始化变量池
		curDate := time.Now().Format("2006-01-02")
		curWeek := getWeekdayCN(time.Now().Weekday())
		vars := map[string]string{
			// 新版命名（sys. 前缀）
			"sys.user_query": userQuery,
			"sys.history":    FormatHistory(messages),
			"sys.cur_date":   curDate,
			"sys.cur_week":   curWeek,
			"sys.kb_context": "", // 知识库检索结果，由节点执行时填充
			// 兼容旧版变量名
			"user_query": userQuery,
			"history":    FormatHistory(messages),
			"cur_date":   curDate,
			"cur_week":   curWeek,
		}
		classifierOutputVar := "intent" // 默认变量名，分类器可能覆盖

		// 4. 意图分类（如果工作流配置了 Classifier）
		if workflow.Classifier != nil {

			// 确保 fastText 模型已训练（从类别关键词自动生成训练数据）
			if err := e.ftPredictor.Train(workflow.Classifier.Categories, workflow.Classifier.Prompt); err != nil {
				slog.Warn("fastText train failed, will skip fastText tier", "error", err)
			}
			slog.Info("classifier start", "workflow", workflow.Name)
			classifyStart := time.Now()
			intents := classify(workflow.Classifier, userQuery, e.baseLLM, e.embClient, e.ftPredictor)
			classifyElapsed := time.Since(classifyStart)

			// 兜底：classify 理论上至少会 fallback 到最后一个类别
			if len(intents) == 0 {
				intents = []model.ClassifiedIntent{{Intent: model.IntentFaq, Confidence: confFallback, Source: model.SourceFallback}}
			}

			classifierOutputVar = workflow.Classifier.OutputVar
			if classifierOutputVar == "" {
				classifierOutputVar = "intent"
			}
			vars[classifierOutputVar] = string(intents[0].Intent)
			// sys. 前缀版本（供模板引用）
			vars["sys."+classifierOutputVar] = string(intents[0].Intent)

			eventCh <- EngineEvent{
				Type:  "progress",
				Step:  0,
				Total: total,
				Agent: "意图分类: " + string(intents[0].Intent),
			}

			slog.Info("classifier done", "workflow", workflow.Name, "intent", intents[0].Intent, "confidence", intents[0].Confidence, "source", intents[0].Source, "duration_ms", classifyElapsed.Milliseconds(), "query", userQuery[:min(50, len(userQuery))])
		}

		// 5. 执行节点（DAG 或线性模式）
		if hasNextNodes(nodes) {
			e.executeDAG(eventCh, workflow, nodes, vars, classifierOutputVar, uid, userQuery)
		} else {
			e.executeLinear(eventCh, nodes, vars, classifierOutputVar, uid, userQuery, total)
		}

		// 发送完成事件
		slog.Info("workflow nodes done", "workflow", workflow.Name, "total_nodes", total)
		eventCh <- EngineEvent{Type: "done", Total: total}
	}()

	return eventCh
}

// ============================================================
// DAG 执行引擎
// ============================================================

// hasNextNodes 判断是否使用 DAG 模式（任一节点有 NextNodes 即为 DAG）
func hasNextNodes(nodes []model.WorkflowNode) bool {
	for _, n := range nodes {
		if len(n.NextNodes) > 0 {
			return true
		}
	}
	return false
}

// buildDAG 从节点列表构建 DAG 邻接表
func buildDAG(nodes []model.WorkflowNode) (map[string]*dagNode, error) {
	dag := make(map[string]*dagNode, len(nodes))

	// 初始化所有节点
	for _, n := range nodes {
		dag[n.ID] = &dagNode{
			Node:     n,
			InDegree: 0,
			OutEdges: n.NextNodes,
		}
	}

	// 计算入度
	for _, dn := range dag {
		for _, targetID := range dn.OutEdges {
			target, ok := dag[targetID]
			if !ok {
				return nil, fmt.Errorf("节点 %s 引用了不存在的下游节点 %s", dn.Node.ID, targetID)
			}
			target.InDegree++
		}
	}

	return dag, nil
}

// topologicalLevels Kahn 算法分层，返回按层级分组的节点
func topologicalLevels(dag map[string]*dagNode) ([][]model.WorkflowNode, error) {
	// 收集入度为 0 的节点
	queue := make([]*dagNode, 0)
	for _, dn := range dag {
		if dn.InDegree == 0 {
			queue = append(queue, dn)
		}
	}

	var levels [][]model.WorkflowNode
	processed := 0
	total := len(dag)

	for len(queue) > 0 {
		levelSize := len(queue)
		level := make([]model.WorkflowNode, 0, levelSize)
		nextQueue := make([]*dagNode, 0)

		for i := 0; i < levelSize; i++ {
			dn := queue[i]
			dn.Level = len(levels)
			level = append(level, dn.Node)
			processed++

			for _, targetID := range dn.OutEdges {
				target := dag[targetID]
				target.InDegree--
				if target.InDegree == 0 {
					nextQueue = append(nextQueue, target)
				}
			}
		}

		levels = append(levels, level)
		queue = nextQueue
	}

	if processed != total {
		return nil, fmt.Errorf("工作流图中存在循环依赖，无法执行")
	}

	return levels, nil
}

// ValidateWorkflowGraph 验证工作流图的有效性
func ValidateWorkflowGraph(nodes []model.WorkflowNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("工作流至少需要一个节点")
	}

	// 检查所有 NextNodes 引用是否存在
	nodeIDs := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.ID] = true
	}
	for _, n := range nodes {
		for _, targetID := range n.NextNodes {
			if !nodeIDs[targetID] {
				return fmt.Errorf("节点 %s 引用了不存在的下游节点 %s", n.ID, targetID)
			}
			// 检查自循环
			if targetID == n.ID {
				return fmt.Errorf("节点 %s 不能引用自身", n.ID)
			}
		}
	}

	// 检查循环依赖
	_, err := buildDAG(nodes)
	if err != nil {
		return err
	}
	dag, _ := buildDAG(nodes)
	_, err = topologicalLevels(dag)
	if err != nil {
		return err
	}

	// 检查至少有一个 sink 节点（没有下游节点）
	hasSink := false
	for _, n := range nodes {
		if len(n.NextNodes) == 0 {
			hasSink = true
			break
		}
	}
	if !hasSink {
		return fmt.Errorf("工作流图必须至少有一个终点节点（无下游节点）")
	}

	return nil
}

// executeLinear 线性执行模式（保持向后兼容）
func (e *Engine) executeLinear(
	eventCh chan<- EngineEvent,
	nodes []model.WorkflowNode,
	vars map[string]string,
	classifierOutputVar string,
	uid string,
	userQuery string,
	total int,
) {
	slog.Info("workflow nodes start (linear)", "total_nodes", total)
	for i, node := range nodes {
		// 条件路由：有 Condition 但不匹配 → 跳过
		if node.Condition != "" {
			if model.IntentType(vars[classifierOutputVar]) != node.Condition {
				slog.Info("skip node by condition", "node", node.ID, "agent_name", node.AgentName, "condition", node.Condition, "current_intent", vars[classifierOutputVar])
				continue
			}
		}

		evt := e.executeNode(node, i+1, total, vars, uid, userQuery, classifierOutputVar)
		if evt != nil {
			eventCh <- *evt
			if evt.Type == "error" {
				return
			}
		}
	}
}

// executeDAG DAG 模式执行
func (e *Engine) executeDAG(
	eventCh chan<- EngineEvent,
	workflow *model.WorkflowDef,
	nodes []model.WorkflowNode,
	vars map[string]string,
	classifierOutputVar string,
	uid string,
	userQuery string,
) {
	slog.Info("workflow nodes start (DAG)", "total_nodes", len(nodes))

	dag, err := buildDAG(nodes)
	if err != nil {
		eventCh <- EngineEvent{Type: "error", Content: "构建执行图失败: " + err.Error(), Error: err}
		return
	}

	levels, err := topologicalLevels(dag)
	if err != nil {
		eventCh <- EngineEvent{Type: "error", Content: "工作流图拓扑排序失败: " + err.Error(), Error: err}
		return
	}

	slog.Info("dag levels computed", "levels", len(levels))
	for li, level := range levels {
		slog.Info("dag level start", "level", li+1, "nodes", len(level), "total_levels", len(levels))
		if len(level) == 1 {
			// 单节点：同步执行
			node := level[0]
			// 条件路由检查
			if node.Condition != "" {
				if model.IntentType(vars[classifierOutputVar]) != node.Condition {
					slog.Info("skip node by condition (DAG)", "node", node.ID, "agent_name", node.AgentName, "condition", node.Condition)
					// 跳过此节点及其下游
					continue
				}
			}
			evt := e.executeNode(node, li+1, len(levels), vars, uid, userQuery, classifierOutputVar)
			if evt != nil {
				eventCh <- *evt
				if evt.Type == "error" {
					return
				}
			}
		} else {
			// 多节点：并行执行
			e.executeParallelLevel(eventCh, level, li+1, len(levels), vars, uid, userQuery, classifierOutputVar)
		}
	}
}

// executeParallelLevel 并行执行同一层级的多个节点
func (e *Engine) executeParallelLevel(
	eventCh chan<- EngineEvent,
	level []model.WorkflowNode,
	step int,
	total int,
	vars map[string]string,
	uid string,
	userQuery string,
	classifierOutputVar string,
) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	hasError := false

	for _, node := range level {
		wg.Add(1)
		go func(n model.WorkflowNode) {
			defer wg.Done()

			// 条件路由
			if n.Condition != "" {
				mu.Lock()
				intent := vars[classifierOutputVar]
				mu.Unlock()
				if model.IntentType(intent) != n.Condition {
					slog.Info("skip node by condition (DAG parallel)", "node", n.ID, "agent_name", n.AgentName, "condition", n.Condition)
					mu.Lock()
					eventCh <- EngineEvent{
						Type:          "progress",
						Step:          step,
						Total:         total,
						Agent:         n.AgentName + " (已跳过)",
						NodeID:        n.ID,
						ParallelGroup: n.ParallelGroup,
					}
					mu.Unlock()
					return
				}
			}

			// 发送进度事件
			progressLabel := n.AgentName
			if n.ParallelGroup != "" {
				progressLabel = "[并行:" + n.ParallelGroup + "] " + n.AgentName
			} else {
				progressLabel = "[并行] " + n.AgentName
			}
			mu.Lock()
			eventCh <- EngineEvent{
				Type:          "progress",
				Step:          step,
				Total:         total,
				Agent:         progressLabel,
				NodeID:        n.ID,
				ParallelGroup: n.ParallelGroup,
			}
			mu.Unlock()

			evt := e.executeNodeInternal(n, step, total, vars, uid, userQuery, classifierOutputVar, &mu)

			if evt != nil {
				mu.Lock()
				if evt.Type == "error" {
					hasError = true
				}
				eventCh <- *evt
				mu.Unlock()
			}
		}(node)
	}

	wg.Wait()

	if hasError {
		slog.Warn("parallel level had errors, continuing")
	}
}

// executeNode DAG 模式的单节点执行（包装 executeNodeInternal，带 mutex）
func (e *Engine) executeNode(
	node model.WorkflowNode,
	step int,
	total int,
	vars map[string]string,
	uid string,
	userQuery string,
	classifierOutputVar string,
) *EngineEvent {
	return e.executeNodeInternal(node, step, total, vars, uid, userQuery, classifierOutputVar, nil)
}

// executeNodeInternal 执行单个节点（核心逻辑），mu 非 nil 时加锁访问 vars
func (e *Engine) executeNodeInternal(
	node model.WorkflowNode,
	step int,
	total int,
	vars map[string]string,
	uid string,
	userQuery string,
	classifierOutputVar string,
	mu *sync.Mutex,
) *EngineEvent {
	// 加载 Agent
	agent, err := e.store.GetAgent(node.AgentID)
	if err != nil || agent == nil {
		slog.Error("agent not found", "node", node.ID, "agent_id", node.AgentID, "error", err)
		return &EngineEvent{
			Type:    "error",
			Content: fmt.Sprintf("节点 %s 引用的智能体 (ID: %d) 不存在", node.ID, node.AgentID),
			Error:   fmt.Errorf("agent %d not found", node.AgentID),
		}
	}

	// 发送进度事件（非并行模式由调用者发，这里做 fallback）
	if mu == nil {
		slog.Info("node start", "node", node.ID, "agent", agent.Name, "step", step, "total", total)
	}

	// 渲染输入模板（读 vars 需加锁）
	if mu != nil {
		mu.Lock()
	}
	input := ResolveTemplate(node.InputTemplate, vars)
	if mu != nil {
		mu.Unlock()
	}
	slog.Info("node input ready", "node", node.ID, "agent", agent.Name, "input_len", len(input))

	// 知识库检索
	if agent.VdbIDs != "" && agent.VdbIDs != "[]" {
		var vdbIDs []int64
		if err := json.Unmarshal([]byte(agent.VdbIDs), &vdbIDs); err == nil && len(vdbIDs) > 0 {
			slog.Info("kb search start", "node", node.ID, "agent", agent.Name, "vdb_ids", vdbIDs)
			kbStart := time.Now()
			var kbContext strings.Builder
			for _, vdbID := range vdbIDs {
				ctx, err := e.kbMgr.SearchInKB(userQuery, vdbID, uid, e.cfg.KB.TopK, e.cfg.KB.ScoreThreshold)
				if err == nil && ctx != "" {
					kbContext.WriteString(ctx)
					kbContext.WriteString("\n")
				}
			}
			if mu != nil {
				mu.Lock()
			}
			vars["sys.kb_context"] = kbContext.String()
			if mu != nil {
				mu.Unlock()
			}
			kbElapsed := time.Since(kbStart)
			slog.Info("kb search done", "node", node.ID, "agent", agent.Name, "kb_context_len", len(kbContext.String()), "duration_ms", kbElapsed.Milliseconds())
		}
	}

	// 构建 system prompt（读 vars 需加锁）
	if mu != nil {
		mu.Lock()
	}
	systemPrompt := ResolveTemplate(agent.SystemPrompt, vars)
	if mu != nil {
		mu.Unlock()
	}

	// LLM 调用
	llmClient := e.getLLMClient(agent)
	slog.Info("llm call start", "node", node.ID, "agent", agent.Name, "model", llmClient.ModelName, "system_prompt_len", len(systemPrompt))
	llmStart := time.Now()

	// 判断是否为最终节点（无下游节点）
	noOutEdges := len(node.NextNodes) == 0
	isFinal := node.IsFinal || noOutEdges

	if isFinal {
		// 最终节点：同步调用（DAG 模式下流式输出在全并行场景下会冲突）
		fullOutput, err := llmClient.Chat(systemPrompt, input)
		llmElapsed := time.Since(llmStart)

		if err != nil {
			slog.Error("node error", "node", node.ID, "agent", agent.Name, "error", err, "duration_ms", llmElapsed.Milliseconds())
			return &EngineEvent{
				Type:    "chunk",
				Content: fmt.Sprintf("[错误] %v", err),
				Step:    step,
				Total:   total,
				Agent:   agent.Name,
				NodeID:  node.ID,
			}
		}
		slog.Info("node done", "node", node.ID, "agent", agent.Name, "type", "sync", "duration_ms", llmElapsed.Milliseconds(), "output_len", len(fullOutput))

		// 存储输出 + 发送 chunk
		if mu != nil {
			mu.Lock()
		}
		vars[node.OutputVar] = fullOutput
		vars[node.ID] = fullOutput
		if mu != nil {
			mu.Unlock()
		}
		return &EngineEvent{
			Type:    "chunk",
			Content: fullOutput,
			Step:    step,
			Total:   total,
			Agent:   agent.Name,
			NodeID:  node.ID,
		}
	}

	// 非最终节点：同步调用
	fullOutput, err := llmClient.Chat(systemPrompt, input)
	llmElapsed := time.Since(llmStart)

	if err != nil {
		slog.Error("node error", "node", node.ID, "agent", agent.Name, "error", err, "duration_ms", llmElapsed.Milliseconds())
		fullOutput = fmt.Sprintf("[错误] %v", err)
	} else {
		outputPreview := fullOutput
		if len(outputPreview) > 80 {
			outputPreview = outputPreview[:80]
		}
		slog.Info("node done", "node", node.ID, "agent", agent.Name, "type", "sync", "duration_ms", llmElapsed.Milliseconds(), "output_len", len(fullOutput), "output_preview", outputPreview)
	}

	// 存储输出到变量池
	if mu != nil {
		mu.Lock()
	}
	vars[node.OutputVar] = fullOutput
	vars[node.ID] = fullOutput
	if mu != nil {
		mu.Unlock()
	}
	return nil
}

// getLLMClient 获取 LLM 客户端（使用 Agent 特定参数或默认）
func (e *Engine) getLLMClient(agent *model.AgentDef) *llm.Client {
	modelName := e.cfg.API.LLMModelName
	apiURI := e.cfg.API.LLMAPIURI
	apiKey := e.cfg.API.LLMAPIKey

	if agent.ModelName != "" {
		modelName = agent.ModelName
	}

	client := llm.New(apiURI, apiKey, modelName)

	// 使用 Agent 特定参数或全局默认
	temp := e.cfg.LLM.Temperature
	topP := e.cfg.LLM.TopP
	maxTok := e.cfg.LLM.MaxTokens

	if agent.Temperature != nil {
		temp = *agent.Temperature
	}
	if agent.TopP != nil {
		topP = *agent.TopP
	}
	if agent.MaxTokens != nil {
		maxTok = *agent.MaxTokens
	}

	client.SetParams(temp, topP, maxTok)
	return client
}

// Execute 非流式执行工作流，返回最终结果
func (e *Engine) Execute(
	workflowID int64,
	userQuery string,
	uid string,
	messages []ChatMsg,
) (string, error) {
	var result strings.Builder
	var lastErr error

	for evt := range e.ExecuteStream(workflowID, userQuery, uid, messages) {
		switch evt.Type {
		case "chunk":
			result.WriteString(evt.Content)
		case "error":
			lastErr = evt.Error
			if result.Len() == 0 {
				return "", evt.Error
			}
		case "done":
			return result.String(), lastErr
		}
	}

	return result.String(), lastErr
}

// EmbClient 返回 embedding 客户端（供外部调试用）
func (e *Engine) EmbClient() *embedding.Client {
	return e.embClient
}

// FtPredictor 返回 fastText 预测器（供外部调试用）
func (e *Engine) FtPredictor() *fasttext.Predictor {
	return e.ftPredictor
}
