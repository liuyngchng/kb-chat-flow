package com.rd.robot.engine;

import com.rd.robot.client.EmbeddingClient;
import com.rd.robot.client.LlmClient;
import com.rd.robot.fasttext.FastTextPredictor;
import com.rd.robot.model.ClassifierDef;
import com.rd.robot.model.IntentCategory;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * Multi-tier intent classifier.
 *
 * Tiers (in order, first match wins):
 *   1. Keyword matching (fastest, ~0ms)
 *   2. fastText local model (~5ms)
 *   3. Embedding semantic matching (~100ms)
 *   4. LLM classification (slowest, most accurate)
 *   5. Fallback to last category
 */
public class IntentClassifier {

    private static final Logger log = LoggerFactory.getLogger(IntentClassifier.class);

    /** Embedding similarity threshold (0~1). */
    private static final double SEMANTIC_THRESHOLD = 0.6;

    // ---- Embedding cache for category vectors ----
    private static final ReentrantReadWriteLock cacheLock = new ReentrantReadWriteLock();
    private static final Map<String, List<CatVector>> catEmbeddingCache = new HashMap<>();

    private static class CatVector {
        final String name;
        final double[] vector;
        CatVector(String name, double[] vector) {
            this.name = name;
            this.vector = vector;
        }
    }

    /** Tier-level classification result for debugging. */
    public static class TierResult {
        public String name;
        public boolean matched;
        public boolean skipped;
        public String result;
        public double score;
        public long elapsedMs;
    }

    /**
     * Classify with detailed tier results (for debugging/testing API).
     */
    public static ClassificationDetail classifyWithDetails(ClassifierDef cfg, String userQuery,
                                                            LlmClient llmClient, EmbeddingClient embClient,
                                                            FastTextPredictor ftPredictor) {
        List<TierResult> tiers = new ArrayList<>();

        if (cfg == null || cfg.getCategories() == null || cfg.getCategories().isEmpty()) {
            return new ClassificationDetail(tiers, "");
        }

        // 1. Keyword matching
        long t0 = System.currentTimeMillis();
        String name = matchKeyword(userQuery, cfg);
        long elapsed = System.currentTimeMillis() - t0;
        if (name != null && !name.isEmpty()) {
            TierResult tr = new TierResult();
            tr.name = "keyword"; tr.matched = true; tr.result = name; tr.elapsedMs = elapsed;
            tiers.add(tr);
            return new ClassificationDetail(tiers, name);
        }
        TierResult tr0 = new TierResult();
        tr0.name = "keyword"; tr0.matched = false; tr0.elapsedMs = elapsed;
        tiers.add(tr0);

        // 2. fastText
        if (ftPredictor != null) {
            t0 = System.currentTimeMillis();
            if (!ftPredictor.isTrained()) {
                TierResult tr = new TierResult();
                tr.name = "fasttext"; tr.skipped = true; tr.elapsedMs = System.currentTimeMillis() - t0;
                tiers.add(tr);
            } else {
                FastTextPredictor.Result result = ftPredictor.predict(userQuery);
                elapsed = System.currentTimeMillis() - t0;
                if (result != null) {
                    TierResult tr = new TierResult();
                    tr.name = "fasttext"; tr.elapsedMs = elapsed;
                    if (result.confidence() >= FastTextPredictor.CONFIDENCE_THRESHOLD) {
                        tr.matched = true; tr.result = result.label(); tr.score = result.confidence();
                        tiers.add(tr);
                        return new ClassificationDetail(tiers, result.label());
                    }
                    tr.matched = false; tr.score = result.confidence();
                    tiers.add(tr);
                } else {
                    TierResult tr = new TierResult();
                    tr.name = "fasttext"; tr.skipped = true; tr.elapsedMs = elapsed;
                    tiers.add(tr);
                }
            }
        }

        // 3. Embedding semantic matching
        if (embClient != null) {
            t0 = System.currentTimeMillis();
            name = matchSemantic(cfg, userQuery, embClient);
            elapsed = System.currentTimeMillis() - t0;
            if (name != null && !name.isEmpty()) {
                TierResult tr = new TierResult();
                tr.name = "embedding"; tr.matched = true; tr.result = name; tr.elapsedMs = elapsed;
                tiers.add(tr);
                return new ClassificationDetail(tiers, name);
            }
            TierResult tr = new TierResult();
            tr.name = "embedding"; tr.matched = false; tr.elapsedMs = elapsed;
            tiers.add(tr);
        }

        // 4. LLM classification
        if (llmClient != null) {
            t0 = System.currentTimeMillis();
            name = llmClassify(cfg, userQuery, llmClient);
            elapsed = System.currentTimeMillis() - t0;
            if (name != null && !name.isEmpty()) {
                TierResult tr = new TierResult();
                tr.name = "llm"; tr.matched = true; tr.result = name; tr.elapsedMs = elapsed;
                tiers.add(tr);
                return new ClassificationDetail(tiers, name);
            }
            TierResult tr = new TierResult();
            tr.name = "llm"; tr.matched = false; tr.elapsedMs = elapsed;
            tiers.add(tr);
        }

        // 5. Fallback to last category
        if (!cfg.getCategories().isEmpty()) {
            String fallback = cfg.getCategories().get(cfg.getCategories().size() - 1).getName();
            TierResult tr = new TierResult();
            tr.name = "fallback"; tr.matched = true; tr.result = fallback;
            tiers.add(tr);
            return new ClassificationDetail(tiers, fallback);
        }

        return new ClassificationDetail(tiers, "");
    }

    /** Wraps tier results with final classification. */
    public static class ClassificationDetail {
        public final List<TierResult> tiers;
        public final String finalResult;
        public ClassificationDetail(List<TierResult> tiers, String finalResult) {
            this.tiers = tiers;
            this.finalResult = finalResult;
        }
    }

    /**
     * Classify user intent through the tiered pipeline:
     *   1. Keyword matching
     *   2. fastText local model
     *   3. Embedding semantic matching
     *   4. LLM classification fallback
     *   5. Final fallback: returns the last category name
     *
     * @param cfg         classifier definition
     * @param userQuery   user input
     * @param llmClient   LLM client for fallback classification
     * @param embClient   embedding client for semantic matching (nullable)
     * @param ftPredictor fastText predictor (nullable)
     * @return matched category name, or empty string if nothing matches
     */
    public static String classify(ClassifierDef cfg, String userQuery, LlmClient llmClient,
                                   EmbeddingClient embClient, FastTextPredictor ftPredictor) {
        if (cfg == null || cfg.getCategories() == null || cfg.getCategories().isEmpty()) {
            return "";
        }

        // 1. Keyword matching (fastest, ~0ms)
        String name = matchKeyword(userQuery, cfg);
        if (name != null && !name.isEmpty()) {
            return name;
        }

        // 2. fastText local model (~5ms)
        if (ftPredictor != null) {
            if (!ftPredictor.isTrained()) {
                log.warn("classifier_fasttext_model_not_found");
            } else {
                FastTextPredictor.Result result = ftPredictor.predict(userQuery);
                if (result != null) {
                    if (result.confidence() >= FastTextPredictor.CONFIDENCE_THRESHOLD) {
                        log.info("classifier_fasttext_matched intent={} confidence={} query={}",
                                result.label(), result.confidence(), truncate(userQuery, 50));
                        return result.label();
                    }
                    log.info("classifier_fasttext_low_confidence label={} confidence={} query={}",
                            result.label(), result.confidence(), truncate(userQuery, 50));
                }
            }
        }

        // 3. Embedding semantic matching (~100ms)
        if (embClient != null) {
            name = matchSemantic(cfg, userQuery, embClient);
            if (name != null && !name.isEmpty()) {
                return name;
            }
        }

        // 4. LLM classification (slowest, most accurate)
        if (llmClient != null) {
            name = llmClassify(cfg, userQuery, llmClient);
            if (name != null && !name.isEmpty()) {
                return name;
            }
        }

        // 5. Final fallback: return the last category
        List<IntentCategory> categories = cfg.getCategories();
        String fallback = categories.get(categories.size() - 1).getName();
        log.info("classifier_fallback intent={} query={}", fallback, truncate(userQuery, 50));
        return fallback;
    }

    /**
     * Backward-compatible classify without embedding or fastText.
     */
    public static String classify(ClassifierDef cfg, String userQuery, LlmClient llmClient) {
        return classify(cfg, userQuery, llmClient, null, null);
    }

    // ============================================================
    // Tier 1: Keyword matching
    // ============================================================

    /**
     * Match user query against category keywords.
     * Picks the category with the longest keyword list when multiple match.
     */
    private static String matchKeyword(String query, ClassifierDef cfg) {
        String queryLower = query.toLowerCase();
        String bestMatch = null;
        int bestLen = 0;

        for (IntentCategory cat : cfg.getCategories()) {
            if (cat.getKeywords() == null) continue;
            for (String kw : cat.getKeywords()) {
                if (queryLower.contains(kw.toLowerCase())) {
                    if (cat.getKeywords().size() > bestLen) {
                        bestMatch = cat.getName();
                        bestLen = cat.getKeywords().size();
                    }
                    break; // one keyword hit is enough
                }
            }
        }

        return bestMatch;
    }

    // ============================================================
    // Tier 2: fastText (in separate FastTextPredictor class)
    // ============================================================

    // ============================================================
    // Tier 3: Embedding semantic matching
    // ============================================================

    /**
     * Match intent using embedding vector cosine similarity.
     */
    private static String matchSemantic(ClassifierDef cfg, String userQuery, EmbeddingClient embClient) {
        // Get category vectors (cached)
        List<CatVector> catVecs = getCategoryVectors(cfg, embClient);
        if (catVecs == null || catVecs.isEmpty()) {
            return null;
        }

        // Compute user query vector
        double[] queryVec;
        try {
            queryVec = embClient.embedSingle(userQuery);
        } catch (Exception e) {
            log.warn("classifier_semantic_embed_query_failed error={}", e.getMessage());
            return null;
        }

        // Find best match by cosine similarity
        double bestScore = 0;
        String bestName = null;
        for (CatVector cv : catVecs) {
            double score = cosineSimilarity(queryVec, cv.vector);
            if (score > bestScore) {
                bestScore = score;
                bestName = cv.name;
            }
        }

        if (bestScore >= SEMANTIC_THRESHOLD) {
            log.info("classifier_semantic_matched intent={} score={} query={}",
                    bestName, bestScore, truncate(userQuery, 50));
            return bestName;
        }

        log.info("classifier_semantic_no_match best_score={} threshold={}", bestScore, SEMANTIC_THRESHOLD);
        return null;
    }

    /**
     * Get or compute category embedding vectors (cached).
     */
    private static List<CatVector> getCategoryVectors(ClassifierDef cfg, EmbeddingClient embClient) {
        String cacheKey = buildCategoryCacheKey(cfg);

        // Read cache
        cacheLock.readLock().lock();
        try {
            List<CatVector> cached = catEmbeddingCache.get(cacheKey);
            if (cached != null) return cached;
        } finally {
            cacheLock.readLock().unlock();
        }

        // Build text for each category
        List<String> catTexts = new ArrayList<>(cfg.getCategories().size());
        for (IntentCategory cat : cfg.getCategories()) {
            catTexts.add(buildCategoryText(cat));
        }

        // Batch embed
        List<double[]> embeddings;
        try {
            embeddings = embClient.embed(catTexts);
        } catch (Exception e) {
            log.warn("classifier_semantic_embed_categories_failed error={}", e.getMessage());
            return null;
        }

        if (embeddings.size() != cfg.getCategories().size()) {
            log.warn("classifier_embedding_count_mismatch expected={} actual={}", embeddings.size(), cfg.getCategories().size());
            return null;
        }

        // Assemble result
        List<CatVector> result = new ArrayList<>(cfg.getCategories().size());
        for (int i = 0; i < cfg.getCategories().size(); i++) {
            result.add(new CatVector(cfg.getCategories().get(i).getName(), embeddings.get(i)));
        }

        // Write cache
        cacheLock.writeLock().lock();
        try {
            catEmbeddingCache.put(cacheKey, result);
        } finally {
            cacheLock.writeLock().unlock();
        }

        return result;
    }

    /**
     * Concatenate category description + keywords into a single text for vectorization.
     */
    private static String buildCategoryText(IntentCategory cat) {
        StringBuilder sb = new StringBuilder();
        if (cat.getDescription() != null) {
            sb.append(cat.getDescription()).append(" ");
        }
        if (cat.getKeywords() != null) {
            for (String kw : cat.getKeywords()) {
                sb.append(kw).append(" ");
            }
        }
        return sb.toString().trim();
    }

    /**
     * Build cache key from classifier definition.
     * When categories change, the key changes and cache auto-invalidates.
     */
    private static String buildCategoryCacheKey(ClassifierDef cfg) {
        StringBuilder sb = new StringBuilder();
        sb.append(cfg.getPrompt() != null ? cfg.getPrompt() : "").append("|");
        for (IntentCategory cat : cfg.getCategories()) {
            sb.append(cat.getName()).append(":");
            sb.append(cat.getDescription()).append(":");
            if (cat.getKeywords() != null) {
                sb.append(String.join(",", cat.getKeywords()));
            }
            sb.append(";");
        }
        return sb.toString();
    }

    /**
     * Compute cosine similarity between two vectors.
     */
    static double cosineSimilarity(double[] a, double[] b) {
        if (a.length != b.length || a.length == 0) {
            return 0;
        }

        double dotProd = 0, normA = 0, normB = 0;
        for (int i = 0; i < a.length; i++) {
            dotProd += a[i] * b[i];
            normA += a[i] * a[i];
            normB += b[i] * b[i];
        }

        if (normA == 0 || normB == 0) {
            return 0;
        }

        return dotProd / (Math.sqrt(normA) * Math.sqrt(normB));
    }

    // ============================================================
    // Tier 4: LLM classification (fallback)
    // ============================================================

    /**
     * Use LLM to classify intent.
     */
    private static String llmClassify(ClassifierDef cfg, String userQuery, LlmClient llmClient) {
        String systemPrompt = buildClassifierPrompt(cfg);
        String userMessage = "用户输入：" + userQuery + "\n\n请输出最匹配的类别名称：";

        try {
            String result = llmClient.chat(systemPrompt, userMessage);
            if (result == null) return null;

            String name = result.trim();
            // Remove punctuation
            name = name.replaceAll("[\"'。，,.：:]", "").trim();

            // Validate against known categories
            for (IntentCategory cat : cfg.getCategories()) {
                if (name.equalsIgnoreCase(cat.getName())) {
                    log.info("classifier_llm_matched intent={}", cat.getName());
                    return cat.getName();
                }
                // Fuzzy match: check if category name is contained in LLM output
                if (result.toLowerCase().contains(cat.getName().toLowerCase())) {
                    log.info("classifier_llm_fuzzy_matched intent={}", cat.getName());
                    return cat.getName();
                }
            }

            log.warn("classifier_llm_unknown_category result={}", result);
        } catch (Exception e) {
            log.warn("classifier_llm_call_failed", e);
        }

        return null;
    }

    private static String buildClassifierPrompt(ClassifierDef cfg) {
        StringBuilder sb = new StringBuilder();

        if (cfg.getPrompt() != null && !cfg.getPrompt().isEmpty()) {
            sb.append(cfg.getPrompt()).append("\n\n");
        } else {
            sb.append("你是一个意图分类器。根据用户输入，判断其意图属于以下哪个类别。\n");
            sb.append("请只输出类别名称，不要输出任何其他内容。\n\n");
        }

        sb.append("可选类别：\n");
        for (IntentCategory cat : cfg.getCategories()) {
            sb.append("- ").append(cat.getName()).append("：").append(cat.getDescription()).append("\n");
        }

        sb.append("\n请只输出类别名称，不要解释。");
        return sb.toString();
    }

    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        return s.length() <= maxLen ? s : s.substring(0, maxLen);
    }
}
