package engine

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/fasttext"
	"kb-chat-flow/internal/llm"
	"kb-chat-flow/internal/model"
)

// TierResult 单层分类结果
type TierResult struct {
	Name    string  `json:"name"`
	Matched bool    `json:"matched"`
	Result  string  `json:"result"`
	Score   float64 `json:"score,omitempty"`
	Elapsed int64   `json:"elapsed_ms"`
	Skipped bool    `json:"skipped,omitempty"`
}

// ClassifyWithDetails 意图分类（调试用），返回各层详细结果和最终意图。
func ClassifyWithDetails(cfg *model.ClassifierDef, userQuery string, llmClient *llm.Client, embClient *embedding.Client, ftPredictor *fasttext.Predictor) ([]TierResult, string) {
	var tiers []TierResult

	if cfg == nil || len(cfg.Categories) == 0 {
		return tiers, ""
	}

	// 1. 关键词匹配
	t0 := time.Now()
	if ci := matchKeyword(userQuery, cfg.Categories); ci.Intent != "" {
		tiers = append(tiers, TierResult{Name: "keyword", Matched: true, Result: string(ci.Intent), Score: ci.Confidence, Elapsed: time.Since(t0).Milliseconds()})
		return tiers, string(ci.Intent)
	}
	tiers = append(tiers, TierResult{Name: "keyword", Matched: false, Elapsed: time.Since(t0).Milliseconds()})

	// 2. fastText —— 多意图：取所有候选
	if ftPredictor != nil {
		t0 = time.Now()
		if !ftPredictor.IsTrained() {
			tiers = append(tiers, TierResult{Name: "fasttext", Skipped: true, Elapsed: time.Since(t0).Milliseconds()})
		} else if results := ftPredictor.Predict(userQuery); len(results) > 0 {
			elapsed := time.Since(t0).Milliseconds()
			top := results[0]
			tiers = append(tiers, TierResult{Name: "fasttext", Matched: true, Result: string(top.Label), Score: top.Confidence, Elapsed: elapsed})
			return tiers, string(top.Label)
		} else {
			tiers = append(tiers, TierResult{Name: "fasttext", Skipped: true, Elapsed: time.Since(t0).Milliseconds()})
		}
	}

	// 3. Embedding 语义匹配
	if embClient != nil {
		t0 = time.Now()
		if name, score := matchSemantic(cfg, userQuery, embClient); name != "" {
			tiers = append(tiers, TierResult{Name: "embedding", Matched: true, Result: string(name), Score: score, Elapsed: time.Since(t0).Milliseconds()})
			return tiers, string(name)
		}
		tiers = append(tiers, TierResult{Name: "embedding", Matched: false, Elapsed: time.Since(t0).Milliseconds()})
	}

	// 4. LLM 分类
	if llmClient != nil {
		t0 = time.Now()
		if name := llmClassify(cfg, userQuery, llmClient); name != "" {
			tiers = append(tiers, TierResult{Name: "llm", Matched: true, Result: string(name), Elapsed: time.Since(t0).Milliseconds()})
			return tiers, string(name)
		}
		tiers = append(tiers, TierResult{Name: "llm", Matched: false, Elapsed: time.Since(t0).Milliseconds()})
	}

	// 5. Fallback
	if len(cfg.Categories) > 0 {
		fallback := string(cfg.Categories[len(cfg.Categories)-1].Name)
		tiers = append(tiers, TierResult{Name: "fallback", Matched: true, Result: fallback})
		return tiers, fallback
	}

	return tiers, ""
}

// classify 意图分类：关键词 → 本地模型 → 语义匹配 → LLM 兜底。
// 返回带置信度的意图列表（按置信度降序），全都没命中返回空切片。
func classify(cfg *model.ClassifierDef, userQuery string, llmClient *llm.Client, embClient *embedding.Client, ftPredictor *fasttext.Predictor) []model.ClassifiedIntent {
	if cfg == nil || len(cfg.Categories) == 0 {
		return nil
	}

	// 1. 关键词匹配（最快，0ms）—— 收集所有命中的类别，按命中数降序
	if intents := matchKeywords(userQuery, cfg.Categories); len(intents) > 0 {
		return intents
	}

	// 2. 本地模型（~5ms，比关键词准）—— 返回 top-k 候选，保留置信度
	if ftPredictor != nil {
		if !ftPredictor.IsTrained() {
			slog.Warn("classifier_fasttext_model_not_found", "path", "dt/ft/model.ftz")
		} else if results := ftPredictor.Predict(userQuery); len(results) > 0 {
			intents := make([]model.ClassifiedIntent, 0, len(results))
			for _, r := range results {
				intents = append(intents, model.ClassifiedIntent{
					Intent:     r.Label,
					Confidence: r.Confidence,
					Source:     model.SourceFastText,
				})
			}
			slog.Info("classifier_fasttext_matched", "intents", intents, "query", userQuery[:min(50, len(userQuery))])
			return intents
		}
	}

	// 3. 语义匹配（~100ms，embedding 向量相似度）—— 单意图，携带余弦相似度
	if embClient != nil {
		if name, score := matchSemantic(cfg, userQuery, embClient); name != "" {
			return []model.ClassifiedIntent{{Intent: name, Confidence: score, Source: model.SourceSemantic}}
		}
	}

	// 4. LLM 分类（慢但最准，兜底）—— 单意图，固定置信度
	if llmClient != nil {
		if name := llmClassify(cfg, userQuery, llmClient); name != "" {
			return []model.ClassifiedIntent{{Intent: name, Confidence: confLLM, Source: model.SourceLLM}}
		}
	}

	// 5. 最终 fallback：返回最后一个类别（通常是一般咨询类）
	if len(cfg.Categories) > 0 {
		fallback := cfg.Categories[len(cfg.Categories)-1].Name
		slog.Info("classifier_fallback", "intent", fallback, "query", userQuery[:min(50, len(userQuery))])
		return []model.ClassifiedIntent{{Intent: fallback, Confidence: confFallback, Source: model.SourceFallback}}
	}

	return nil
}

// matchKeyword 用关键词字典做匹配，返回命中的 category。
func matchKeyword(query string, categories []model.IntentCategory) model.ClassifiedIntent {
	query = strings.ToLower(query)
	var bestMatch model.IntentType
	var bestLen int

	for _, cat := range categories {
		for _, kw := range cat.Keywords {
			if strings.Contains(query, strings.ToLower(kw)) {
				if len(cat.Keywords) > bestLen {
					bestMatch = cat.Name
					bestLen = len(cat.Keywords)
				}
				break // 命中一个 keyword 就够了，跳出内层循环
			}
		}
	}

	if bestMatch == "" {
		return model.ClassifiedIntent{}
	}
	return model.ClassifiedIntent{Intent: bestMatch, Confidence: confKeyword, Source: model.SourceKeyword}
}

// matchKeywords 收集 query 中命中的所有意图类别（去重），按命中关键词数降序。
func matchKeywords(query string, categories []model.IntentCategory) []model.ClassifiedIntent {
	query = strings.ToLower(query)
	type hit struct {
		intent model.IntentType
		count  int
	}
	var hits []hit

	for _, cat := range categories {
		count := 0
		for _, kw := range cat.Keywords {
			if strings.Contains(query, strings.ToLower(kw)) {
				count++
			}
		}
		if count > 0 {
			hits = append(hits, hit{intent: cat.Name, count: count})
		}
	}

	// 按命中关键词数量降序
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].count > hits[j].count
	})

	result := make([]model.ClassifiedIntent, len(hits))
	for i, h := range hits {
		result[i] = model.ClassifiedIntent{Intent: h.intent, Confidence: confKeyword, Source: model.SourceKeyword}
	}
	return result
}

// ============================================================
// 语义匹配（embedding 向量相似度）
// ============================================================

// 语义匹配的相似度阈值（0~1），低于此值视为不匹配
const semanticThreshold = 0.6

// 各分类层级的默认置信度（0~1）
const (
	confKeyword  = 0.95 // 关键词精准匹配
	confLLM      = 0.75 // LLM 分类
	confFallback = 0.10 // 兜底，几乎不可信
)

// ambiguityGap 歧义检测阈值：top-2 置信度差距 < 此值视为歧义
const ambiguityGap = 0.20

// secondaryMinConf 次意图最低置信度：低于此值不做独立检索，仅文本追问
const secondaryMinConf = 0.40

// catEmbeddingCache 缓存每个分类器的类别向量
// key: 分类器各 category 的 name+description+keywords 拼接后的 hash
var catEmbeddingCache = struct {
	sync.RWMutex
	cache map[string][]catVector // key -> 按 categories 顺序的向量
}{cache: make(map[string][]catVector)}

type catVector struct {
	name   model.IntentType
	vector []float64
}

// matchSemantic 用 embedding 向量相似度做意图匹配。
// 返回最佳匹配类别和余弦相似度，未命中返回 ("", score)。
func matchSemantic(cfg *model.ClassifierDef, userQuery string, embClient *embedding.Client) (model.IntentType, float64) {
	// 获取分类别的归一化向量
	catVecs, err := getCategoryVectors(cfg, embClient)
	if err != nil {
		slog.Warn("classifier_semantic_get_vectors_failed", "error", err)
		return "", 0
	}
	if len(catVecs) == 0 {
		return "", 0
	}

	// 计算用户 query 的向量
	queryVec, err := embClient.EmbedSingle(userQuery)
	if err != nil {
		slog.Warn("classifier_semantic_embed_query_failed", "error", err)
		return "", 0
	}

	// 计算相似度，找到最佳匹配
	var bestScore float64
	var bestName model.IntentType
	for _, cv := range catVecs {
		score := cosineSimilarity(queryVec, cv.vector)
		if score > bestScore {
			bestScore = score
			bestName = cv.name
		}
	}

	if bestScore >= semanticThreshold {
		slog.Info("classifier_semantic_matched", "intent", bestName, "score", bestScore, "query", userQuery[:min(50, len(userQuery))])
		return bestName, bestScore
	}

	slog.Info("classifier_semantic_no_match", "best_score", bestScore, "threshold", semanticThreshold)
	return "", bestScore
}

// getCategoryVectors 获取分类器的各类别向量（带缓存）。
func getCategoryVectors(cfg *model.ClassifierDef, embClient *embedding.Client) ([]catVector, error) {
	// 基于类别定义生成缓存 key
	cacheKey := buildCategoryCacheKey(cfg)

	// 先查缓存
	catEmbeddingCache.RLock()
	if cached, ok := catEmbeddingCache.cache[cacheKey]; ok {
		catEmbeddingCache.RUnlock()
		return cached, nil
	}
	catEmbeddingCache.RUnlock()

	// 构建每个类别的规范化文本
	catTexts := make([]string, len(cfg.Categories))
	for i, cat := range cfg.Categories {
		catTexts[i] = buildCategoryText(cat)
	}

	// 批量计算向量
	embeddings, err := embClient.Embed(catTexts)
	if err != nil {
		return nil, fmt.Errorf("batch embed categories: %w", err)
	}

	if len(embeddings) != len(cfg.Categories) {
		return nil, fmt.Errorf("embedding count mismatch: %d vs %d", len(embeddings), len(cfg.Categories))
	}

	// 组装结果
	result := make([]catVector, len(cfg.Categories))
	for i, cat := range cfg.Categories {
		result[i] = catVector{name: cat.Name, vector: embeddings[i]}
	}

	// 写入缓存
	catEmbeddingCache.Lock()
	catEmbeddingCache.cache[cacheKey] = result
	catEmbeddingCache.Unlock()

	return result, nil
}

// buildCategoryText 将类别定义拼接为一段规范文本用于向量化。
// 格式：类别描述 + 关键词
func buildCategoryText(cat model.IntentCategory) string {
	parts := []string{cat.Description}
	parts = append(parts, cat.Keywords...)
	return strings.Join(parts, " ")
}

// buildCategoryCacheKey 基于分类器定义生成缓存 key。
// 当类别名、描述或关键词变化时，key 会变，缓存自动失效。
func buildCategoryCacheKey(cfg *model.ClassifierDef) string {
	var b strings.Builder
	b.WriteString(cfg.Prompt)
	b.WriteString("|")
	for _, cat := range cfg.Categories {
		b.WriteString(string(cat.Name))
		b.WriteString(":")
		b.WriteString(cat.Description)
		b.WriteString(":")
		b.WriteString(strings.Join(cat.Keywords, ","))
		b.WriteString(";")
	}
	return b.String()
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

// sqrtFloat 简单的平方根（Newton 法，避免引入 math 包）
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

// ============================================================
// LLM 分类（兜底）
// ============================================================

// llmClassify 用 LLM 做意图分类，要求模型输出类别名。
func llmClassify(cfg *model.ClassifierDef, userQuery string, llmClient *llm.Client) model.IntentType {
	systemPrompt := buildClassifierPrompt(cfg)
	userMessage := fmt.Sprintf("用户输入：%s\n\n请输出最匹配的类别名称：", userQuery)

	result, err := llmClient.Chat(systemPrompt, userMessage)
	if err != nil {
		slog.Warn("classifier_llm_call_failed", "error", err)
		return ""
	}

	// 清理结果（去掉空格、引号、标点）
	name := strings.TrimSpace(result)
	name = strings.Trim(name, "\"'。，,.：: ")

	// 校验是否在已知类别列表中
	for _, cat := range cfg.Categories {
		if strings.EqualFold(name, string(cat.Name)) {
			slog.Info("classifier_llm_matched", "intent", cat.Name)
			return cat.Name
		}
		// 检查类别名是否包含在 LLM 输出中（模糊匹配）
		if strings.Contains(strings.ToLower(result), strings.ToLower(string(cat.Name))) {
			slog.Info("classifier_llm_fuzzy_matched", "intent", cat.Name)
			return cat.Name
		}
	}

	slog.Warn("classifier_llm_unknown_category", "result", result)
	return ""
}

// buildClassifierPrompt 构建分类器的 system prompt
func buildClassifierPrompt(cfg *model.ClassifierDef) string {
	var b strings.Builder

	if cfg.Prompt != "" {
		b.WriteString(cfg.Prompt)
		b.WriteString("\n\n")
	} else {
		b.WriteString("你是一个意图分类器。根据用户输入，判断其意图属于以下哪个类别。\n")
		b.WriteString("请只输出类别名称，不要输出任何其他内容。\n\n")
	}

	b.WriteString("可选类别：\n")
	for _, cat := range cfg.Categories {
		b.WriteString(fmt.Sprintf("- %s：%s\n", cat.Name, cat.Description))
	}

	b.WriteString("\n请只输出类别名称，不要解释。")
	return b.String()
}
