package engine

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"kb-chat-flow/internal/model"
)

// ============================================================
// csm.go — 硬编码客服问答逻辑（CSM = Customer Service Module）
// ============================================================
//
// 背景：动态工作流配置（cfg.db workflow_def）已能满足复杂编排，但日常
// 业务上配置成本高。本文件把"燃气客服"这一条问答逻辑用代码写死，
// 作为简单快速的业务实现，绕过数据库中的工作流配置。
//
// 逻辑与 cfg.db 中"燃气客服工作流"(workflow 1) 一致：
//   意图分类(emergency/billing/business/repair/faq)
//   → 按意图路由 → (紧急/业务直接回答，账单/维修/FAQ 先检索知识库)
//   → LLM 流式生成最终回答
//
// 动态配置逻辑（engine.go / classifier.go / template.go）不受影响。
//
// 关键节点日志（检索 key 便于链路追踪）：
//   csm_run_start        流程入口（uid / query）
//   csm_classify_done    意图分类完成（intent / 耗时）
//   csm_route            路由结果（intent → branch）
//   csm_kb_search_start  开始检索知识库（vdb_ids）
//   csm_kb_search_done   检索完成（耗时 / 上下文长度）
//   csm_kb_search_failed 单个知识库检索失败
//   csm_llm_start        开始请求 LLM（agent / model / 输入长度）
//   csm_llm_done         LLM 请求完成（耗时 / chunk 数 / 输出长度）
//   csm_llm_error        LLM 请求失败
//   csm_run_done         整条流程结束（总耗时）

// csmTotalStep 硬编码流程的总步骤数（0=意图分类, 1=检索, 2=回答）
// 供前端进度展示（EngineEvent.Total）使用
const csmTotalStep = 3

// csmClassifier 硬编码的意图分类器配置。
// 与 cfg.db workflow 1 的 classifier 完全一致。
var csmClassifier = &model.ClassifierDef{
	OutputVar: "intent",
	Prompt:    "你是一个燃气公司客服意图分类器。根据用户输入，判断其意图属于以下哪个类别。\n请只输出类别名称，不要输出任何其他内容。",
	Categories: []model.IntentCategory{
		{Name: model.IntentEmergency, Description: "燃气泄漏、燃气味、报警等紧急安全情况", Keywords: []string{"漏气", "燃气味", "煤气味", "报警", "爆炸", "火灾", "着火", "泄漏", "冒烟", "异味", "刺鼻"}},
		{Name: model.IntentBilling, Description: "账单查询、缴费、欠费、发票等财务问题", Keywords: []string{"账单", "缴费", "欠费", "余额", "发票", "价格", "费用", "多少钱", "扣费", "充值", "代扣", "阶梯价"}},
		{Name: model.IntentBusiness, Description: "开户、过户、改名、报装、停气等业务办理", Keywords: []string{"开户", "过户", "改名", "报装", "停气", "新装", "移表", "增容", "改管", "安装", "开通", "搬迁", "换表"}},
		{Name: model.IntentRepair, Description: "燃气设备维修、故障排查、保养、安检", Keywords: []string{"维修", "故障", "坏了", "打不着火", "点不着", "不着火", "保养", "安检", "检查", "熄火", "红火", "小火", "自动关", "打火"}},
		{Name: model.IntentFaq, Description: "常见综合咨询：营业时间、电话、地址、投诉建议等", Keywords: []string{"营业时间", "电话", "地址", "投诉", "建议", "表扬", "几点", "在哪", "怎么去", "客服", "人工", "工作时间"}},
	},
}

// csm 各分支知识库绑定的配置 key（sys_config 表）
const (
	cfgKeyBilling = "csm.billing_vdb_ids"
	cfgKeyRepair  = "csm.repair_vdb_ids"
	cfgKeyFaq     = "csm.faq_vdb_ids"
)

// ============================================================
// 各意图智能体系统提示词（与 cfg.db agent_def 表内容一致）
// ============================================================

// csmEmergencyPrompt 紧急调度：不检索，直接回答。
const csmEmergencyPrompt = `你是燃气公司紧急调度员。用户遇到了紧急情况，你必须优先处理。
请引导用户立即采取安全措施：关闭燃气阀门、开窗通风、禁止明火、撤离现场，
同时告知用户已安排紧急维修人员尽快到达。
语气要冷静、专业，给用户安全感。`

// csmBillingPrompt 账单客服：基于检索结果回答。
const csmBillingPrompt = `你是燃气公司账单客服。根据检索到的账单信息，帮助用户解决账单查询、缴费方式、欠费处理等问题。
用亲切专业的中文回答，引导用户完成缴费操作。`

// csmBusinessPrompt 业务办理：不检索，直接回答。
const csmBusinessPrompt = `你是燃气公司业务办理专员。帮助用户办理开户、过户、改名、报装、停气等业务。
请告知用户所需材料、办理流程和注意事项。
语气亲切、专业，一步步引导用户完成业务办理。`

// csmRepairPrompt 维修客服：基于检索结果回答。
const csmRepairPrompt = `你是燃气公司维修客服。根据检索到的维修信息，帮助用户进行故障诊断、保养指导、报修登记。
对于简单故障给出排查建议，无法解决的安排维修人员上门。
语气专业、耐心。`

// csmFaqPrompt 综合FAQ：基于检索结果回答。
const csmFaqPrompt = `你是燃气公司综合客服。根据检索到的FAQ信息，回答用户的各种常见问题，
如营业时间、服务电话、地址、投诉渠道等。
语气亲切、专业，解答清晰明了。`

// ExecuteStreamCSM 硬编码客服问答的流式执行入口。
//
// 签名与 ExecuteStream 保持一致，便于 handler 侧直接替换：
//
//	eventCh := h.engine.ExecuteStreamCSM(req.WorkflowID, req.Msg, uid, historyMsgs)
//
// workflowID 已不再用于加载数据库配置，仅保留参数以维持调用兼容。
// 事件协议完全复用 EngineEvent（progress / chunk / done / error），
// handler 的 SSE 循环无需任何改动。
func (e *Engine) ExecuteStreamCSM(
	workflowID int64,
	userQuery string,
	uid string,
	messages []ChatMsg,
) <-chan EngineEvent {
	eventCh := make(chan EngineEvent, 50)

	go func() {
		defer close(eventCh)
		e.csmRun(eventCh, userQuery, uid, len(messages))
	}()

	return eventCh
}

// csmRun 硬编码流程主逻辑。
func (e *Engine) csmRun(eventCh chan<- EngineEvent, userQuery, uid string, historyCount int) {
	runStart := time.Now()
	slog.Info("csm_run_start", "uid", uid, "query", truncateStr(userQuery, 80), "query_len", len(userQuery), "history", historyCount)

	// 1. 意图分类（复用 engine.classify，多级匹配：关键词 → fastText → 语义 → LLM → fallback）
	if err := e.ftPredictor.Train(csmClassifier.Categories, csmClassifier.Prompt); err != nil {
		slog.Warn("csm_fasttext_train_failed", "error", err)
	}
	classifyStart := time.Now()
	classified := classify(csmClassifier, userQuery, e.baseLLM, e.embClient, e.ftPredictor)
	if len(classified) == 0 {
		// 理论上 classify 至少会 fallback 到最后一个类别；此处兜底防御
		classified = []model.ClassifiedIntent{{Intent: model.IntentFaq, Confidence: confFallback, Source: model.SourceFallback}}
	}

	// ---- RULE 1: emergency 在任何位置都最高优先级（安全第一） ----
	primary := classified[0]
	if ci, ok := findIntent(classified, model.IntentEmergency); ok {
		primary = ci
	}

	// ---- RULE 2: 歧义检测（top-2 置信度差距 < 0.20，且不是双 keyword 多意图） ----
	if primary.Intent != model.IntentEmergency && len(classified) >= 2 &&
		classified[0].Confidence-classified[1].Confidence < ambiguityGap &&
		!(classified[0].Source == model.SourceKeyword && classified[1].Source == model.SourceKeyword) {

		slog.Info("csm_classify_done_ambiguous", "intents", classified, "ambiguous", true, "duration_ms", time.Since(classifyStart).Milliseconds(), "query", truncateStr(userQuery, 80))
		eventCh <- EngineEvent{Type: "progress", Step: 0, Total: csmTotalStep, Agent: "意图确认"}
		eventCh <- EngineEvent{Type: "chunk", Content: csmClarifyText(classified[:2]), Step: 2, Total: csmTotalStep}
		eventCh <- EngineEvent{Type: "done", Total: csmTotalStep}
		slog.Info("csm_run_done_ambiguous", "intent", "ambiguous", "total_ms", time.Since(runStart).Milliseconds())
		return
	}

	slog.Info("csm_classify_done", "intents", classified, "primary", string(primary.Intent),
		"primary_conf", primary.Confidence, "source", primary.Source, "duration_ms", time.Since(classifyStart).Milliseconds(), "query", truncateStr(userQuery, 80))
	eventCh <- EngineEvent{Type: "progress", Step: 0, Total: csmTotalStep, Agent: "意图分类: " + string(primary.Intent)}

	branch := csmBranchName(primary.Intent)
	slog.Info("csm_route", "intent", string(primary.Intent), "branch", branch, "candidates", classified)

	// ---- RULE 3: 正常路由（低置信度时软化提示） ----
	prompt := csmBranchPrompt(primary.Intent)
	if primary.Source == model.SourceFallback {
		prompt += csmLowConfidenceHint
	}

	switch primary.Intent {
	case model.IntentEmergency:
		e.csmAnswerDirect(eventCh, "紧急调度", prompt, userQuery)
	case model.IntentBilling:
		e.csmAnswerWithKB(eventCh, "账单检索", "账单客服", prompt, userQuery, uid, e.billingVdbIDsSnapshot())
	case model.IntentBusiness:
		e.csmAnswerDirect(eventCh, "业务办理", prompt, userQuery)
	case model.IntentRepair:
		e.csmAnswerWithKB(eventCh, "维修检索", "维修客服", prompt, userQuery, uid, e.repairVdbIDsSnapshot())
	default: // faq / 未识别
		e.csmAnswerWithKB(eventCh, "FAQ检索", "综合FAQ", prompt, userQuery, uid, e.faqVdbIDsSnapshot())
	}

	// ---- RULE 4: 次意图处理（primary 非 emergency 时） ----
	if primary.Intent != model.IntentEmergency {
		if secondary := selectSecondary(classified, primary); secondary != nil {
			if agentName, secPrompt, vdbIDs, ok := e.csmSecondaryBranch(secondary.Intent); ok {
				eventCh <- EngineEvent{Type: "chunk", Content: "\n\n—— 另外，关于您提到的" + csmIntentLabel(secondary.Intent) + " ——\n\n", Step: 2, Total: csmTotalStep}
				e.csmAnswerWithKB(eventCh, agentName, "次意图:"+csmIntentLabel(secondary.Intent), secPrompt, userQuery, uid, vdbIDs)
			} else {
				eventCh <- EngineEvent{Type: "chunk", Content: csmFollowUpText(secondary.Intent), Step: 2, Total: csmTotalStep}
			}
		}
	}

	// 3. 完成
	eventCh <- EngineEvent{Type: "done", Total: csmTotalStep}
	slog.Info("csm_run_done", "intent", string(primary.Intent), "total_ms", time.Since(runStart).Milliseconds())
}

// findIntent 在分类结果中查找指定意图，返回其完整信息（含置信度）。
func findIntent(classified []model.ClassifiedIntent, target model.IntentType) (model.ClassifiedIntent, bool) {
	for _, ci := range classified {
		if ci.Intent == target {
			return ci, true
		}
	}
	return model.ClassifiedIntent{}, false
}

// selectSecondary 返回置信度达标（≥0.40）的第一个非主意图；nil 表示无。
func selectSecondary(classified []model.ClassifiedIntent, primary model.ClassifiedIntent) *model.ClassifiedIntent {
	for i := range classified {
		if classified[i].Intent != primary.Intent && classified[i].Confidence >= secondaryMinConf {
			return &classified[i]
		}
	}
	return nil
}

// csmFollowUpText 生成次意图追问文本。
func csmFollowUpText(secondary model.IntentType) string {
	switch secondary {
	case model.IntentBilling:
		return "另外，您还提到了账单相关的问题，需要帮您查询吗？"
	case model.IntentRepair:
		return "另外，您还提到了维修相关的问题，需要帮您排查吗？"
	case model.IntentBusiness:
		return "另外，您还提到了业务办理相关的问题，需要帮您处理吗？"
	case model.IntentFaq:
		return "另外，您还有其他问题需要咨询吗？"
	default:
		return "您还有其他问题需要帮助吗？"
	}
}

// csmLowConfidenceHint 低置信度时追加到 prompt 末尾的软化提示。
const csmLowConfidenceHint = "\n\n注意：用户描述可能不够清晰。如以上信息无法解答，请礼貌引导用户换个方式描述或转人工客服。"

// csmClarifyText 歧义反问，生成 markdown 选项列表让用户选择。
func csmClarifyText(candidates []model.ClassifiedIntent) string {
	var b strings.Builder
	b.WriteString("您的问题可能涉及多个方面，我不太确定您具体想了解哪一个：\n\n")
	for _, ci := range candidates {
		label := csmIntentLabel(ci.Intent)
		desc := csmIntentDesc(ci.Intent)
		b.WriteString("- **" + label + "** — " + desc + "\n")
	}
	b.WriteString("\n请问您主要想了解哪一个？直接告诉我就可以，我来帮您详细解答。")
	return b.String()
}

// csmIntentLabel 返回意图的中文标签。
func csmIntentLabel(intent model.IntentType) string {
	switch intent {
	case model.IntentEmergency:
		return "紧急求助"
	case model.IntentBilling:
		return "账单查询"
	case model.IntentBusiness:
		return "业务办理"
	case model.IntentRepair:
		return "维修报修"
	case model.IntentFaq:
		return "综合咨询"
	default:
		return "其他问题"
	}
}

// csmIntentDesc 返回意图的简要描述（用于歧义反问）。
func csmIntentDesc(intent model.IntentType) string {
	switch intent {
	case model.IntentEmergency:
		return "燃气泄漏、异味、报警等紧急安全情况"
	case model.IntentBilling:
		return "账单、缴费、欠费、发票等财务问题"
	case model.IntentBusiness:
		return "开户、过户、报装、停气等业务办理"
	case model.IntentRepair:
		return "设备故障、打不着火、保养、安检等维修问题"
	case model.IntentFaq:
		return "营业时间、电话地址、投诉建议等常见咨询"
	default:
		return ""
	}
}

// csmBranchPrompt 返回意图对应的系统提示词。
func csmBranchPrompt(intent model.IntentType) string {
	switch intent {
	case model.IntentEmergency:
		return csmEmergencyPrompt
	case model.IntentBilling:
		return csmBillingPrompt
	case model.IntentBusiness:
		return csmBusinessPrompt
	case model.IntentRepair:
		return csmRepairPrompt
	default:
		return csmFaqPrompt
	}
}

// csmSecondaryBranch 返回次意图的分支信息（检索 agent 名 / prompt / 知识库 ids）。
// ok=false 表示该意图没有 KB 分支，仅用文本追问。
func (e *Engine) csmSecondaryBranch(intent model.IntentType) (agentName, prompt string, vdbIDs []int64, ok bool) {
	switch intent {
	case model.IntentBilling:
		return "账单检索", csmBillingPrompt, e.billingVdbIDsSnapshot(), true
	case model.IntentRepair:
		return "维修检索", csmRepairPrompt, e.repairVdbIDsSnapshot(), true
	case model.IntentFaq:
		return "FAQ检索", csmFaqPrompt, e.faqVdbIDsSnapshot(), true
	default:
		return "", "", nil, false
	}
}

// csmBranchName 返回意图对应的路由分支描述（仅用于日志）。
func csmBranchName(intent model.IntentType) string {
	switch intent {
	case model.IntentEmergency:
		return "emergency -> 紧急调度（直接回答）"
	case model.IntentBilling:
		return "billing -> 账单检索 + 账单客服"
	case model.IntentBusiness:
		return "business -> 业务办理（直接回答）"
	case model.IntentRepair:
		return "repair -> 维修检索 + 维修客服"
	default:
		return "faq -> FAQ检索 + 综合FAQ"
	}
}

// csmAnswerDirect 直接回答（不检索知识库），用于紧急调度 / 业务办理。
func (e *Engine) csmAnswerDirect(eventCh chan<- EngineEvent, agentName, systemPrompt, userQuery string) {
	eventCh <- EngineEvent{Type: "progress", Step: 2, Total: csmTotalStep, Agent: agentName}
	e.csmStream(eventCh, agentName, systemPrompt, userQuery)
}

// csmAnswerWithKB 先检索知识库，再基于检索结果回答，用于账单 / 维修 / FAQ。
// 检索步骤与回答步骤分别发送 progress，与动态工作流的节点展示一致。
// vdbIDs 指定该分支检索的知识库（各分支独立）。
func (e *Engine) csmAnswerWithKB(eventCh chan<- EngineEvent, retrieveAgent, answerAgent, systemPrompt, userQuery, uid string, vdbIDs []int64) {
	eventCh <- EngineEvent{Type: "progress", Step: 1, Total: csmTotalStep, Agent: retrieveAgent}

	kbContext := e.csmSearchKB(userQuery, uid, vdbIDs)

	// 与 workflow 节点 InputTemplate "用户问题：{{user_query}}\n检索信息：{{xx_ctx}}" 保持一致
	userMessage := "用户问题：" + userQuery + "\n检索信息：" + kbContext

	eventCh <- EngineEvent{Type: "progress", Step: 2, Total: csmTotalStep, Agent: answerAgent}
	e.csmStream(eventCh, answerAgent, systemPrompt, userMessage)
}

// csmSearchKB 在指定知识库列表中检索用户问题，拼接上下文。
func (e *Engine) csmSearchKB(userQuery, uid string, vdbIDs []int64) string {
	start := time.Now()
	slog.Info("csm_kb_search_start", "vdb_ids", vdbIDs, "query", truncateStr(userQuery, 80))

	var sb strings.Builder
	for _, vdbID := range vdbIDs {
		ctx, err := e.kbMgr.SearchInKB(userQuery, vdbID, uid, e.cfg.KB.TopK, e.cfg.KB.ScoreThreshold)
		if err != nil {
			slog.Warn("csm_kb_search_failed", "vdb_id", vdbID, "error", err)
			continue
		}
		if ctx != "" {
			sb.WriteString(ctx)
			sb.WriteString("\n")
		}
	}
	slog.Info("csm_kb_search_done", "duration_ms", time.Since(start).Milliseconds(), "context_len", len(sb.String()))
	return sb.String()
}

// csmStream 流式调用 LLM，将输出以 chunk 事件逐段发出。
func (e *Engine) csmStream(eventCh chan<- EngineEvent, agentName, systemPrompt, userMessage string) {
	start := time.Now()
	modelName := e.cfg.API.LLMModelName
	if e.baseLLM != nil && e.baseLLM.ModelName != "" {
		modelName = e.baseLLM.ModelName
	}
	slog.Info("csm_llm_start", "agent", agentName, "model", modelName, "prompt_len", len(systemPrompt), "input_len", len(userMessage))

	chunkCh, errCh := e.baseLLM.ChatStream(systemPrompt, userMessage)

	var output strings.Builder
	chunkCount := 0
	for chunk := range chunkCh {
		output.WriteString(chunk)
		chunkCount++
		eventCh <- EngineEvent{Type: "chunk", Content: chunk, Step: 2, Total: csmTotalStep, Agent: agentName}
	}

	// 检查错误（errCh 为缓冲通道，range 结束后读它不会阻塞）
	err := <-errCh
	if err != nil {
		slog.Error("csm_llm_error", "agent", agentName, "error", err, "duration_ms", time.Since(start).Milliseconds(), "chunks", chunkCount, "output_len", output.Len())
		eventCh <- EngineEvent{Type: "error", Content: "[错误] " + err.Error(), Error: err}
		return
	}

	slog.Info("csm_llm_done", "agent", agentName, "duration_ms", time.Since(start).Milliseconds(), "chunks", chunkCount, "output_len", output.Len(), "output_preview", truncateStr(output.String(), 80))
}

// truncateStr 截断字符串用于日志预览（按 rune 截断避免切坏中文）。
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// ============================================================
// csm 分支知识库绑定（sys_config 存储，可热加载）
// ============================================================

// defaultVdbIDs 默认绑定的知识库 id（无配置时使用）
var defaultVdbIDs = []int64{3}

// LoadVdbBindings 从 sys_config 加载 csm 各分支绑定的知识库 id。
// 配置不存在或解析失败时使用默认值 {3}。
func (e *Engine) LoadVdbBindings() {
	e.bindingLock.Lock()
	defer e.bindingLock.Unlock()

	e.billingVdbIDs = e.loadVdbIDs(cfgKeyBilling)
	e.repairVdbIDs = e.loadVdbIDs(cfgKeyRepair)
	e.faqVdbIDs = e.loadVdbIDs(cfgKeyFaq)

	slog.Info("csm vdb bindings loaded",
		"billing", e.billingVdbIDs,
		"repair", e.repairVdbIDs,
		"faq", e.faqVdbIDs)
}

// ReloadVdbBindings 重新加载绑定（配置更新后调用，热加载即时生效）。
func (e *Engine) ReloadVdbBindings() {
	e.LoadVdbBindings()
}

// loadVdbIDs 读取单个配置 key 并解析为 []int64。
// 配置为空或解析失败返回默认值。
func (e *Engine) loadVdbIDs(key string) []int64 {
	if e.store == nil {
		return defaultVdbIDs
	}
	val, err := e.store.GetConfig(key)
	if err != nil || strings.TrimSpace(val) == "" {
		return defaultVdbIDs
	}
	var ids []int64
	if err := json.Unmarshal([]byte(val), &ids); err != nil || len(ids) == 0 {
		slog.Warn("csm_vdb_binding_parse_failed", "key", key, "val", val, "error", err)
		return defaultVdbIDs
	}
	return ids
}

// billingVdbIDsSnapshot 返回账单分支绑定的知识库 id（读锁快照）。
func (e *Engine) billingVdbIDsSnapshot() []int64 {
	e.bindingLock.RLock()
	defer e.bindingLock.RUnlock()
	return e.billingVdbIDs
}

// repairVdbIDsSnapshot 返回维修分支绑定的知识库 id（读锁快照）。
func (e *Engine) repairVdbIDsSnapshot() []int64 {
	e.bindingLock.RLock()
	defer e.bindingLock.RUnlock()
	return e.repairVdbIDs
}

// faqVdbIDsSnapshot 返回 FAQ 分支绑定的知识库 id（读锁快照）。
func (e *Engine) faqVdbIDsSnapshot() []int64 {
	e.bindingLock.RLock()
	defer e.bindingLock.RUnlock()
	return e.faqVdbIDs
}

// BillingVdbIDs 返回账单分支当前绑定的知识库 id（供 handler 展示）。
func (e *Engine) BillingVdbIDs() []int64 {
	return e.billingVdbIDsSnapshot()
}

// RepairVdbIDs 返回维修分支当前绑定的知识库 id（供 handler 展示）。
func (e *Engine) RepairVdbIDs() []int64 {
	return e.repairVdbIDsSnapshot()
}

// FaqVdbIDs 返回 FAQ 分支当前绑定的知识库 id（供 handler 展示）。
func (e *Engine) FaqVdbIDs() []int64 {
	return e.faqVdbIDsSnapshot()
}
