package com.rd.robot.client;

import com.rd.robot.model.Config;
import com.rd.robot.repository.MetaStore;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * Lazy client factory.
 * Clients are created on first use, reading config from DB (cfg.db).
 * Cached after creation; call {@link #invalidate()} when admin updates config.
 */
public class ClientFactory {

    private static final Logger log = LoggerFactory.getLogger(ClientFactory.class);

    private final MetaStore metaStore;

    private volatile EmbeddingClient embeddingClient;
    private volatile LlmClient llmClient;
    private volatile RerankClient rerankClient;

    public ClientFactory(MetaStore metaStore) {
        this.metaStore = metaStore;
    }

    // ============================================================
    // Embedding
    // ============================================================

    public EmbeddingClient getEmbeddingClient() {
        if (embeddingClient == null) {
            synchronized (this) {
                if (embeddingClient == null) {
                    String uri = metaStore.getConfig("api.embedding_api_uri");
                    String key = metaStore.getConfig("api.embedding_api_key");
                    String model = metaStore.getConfig("api.embedding_model_name");
                    if (uri == null || uri.isEmpty()) {
                        throw new RuntimeException("Embedding API 未配置，请在系统管理页面中设置");
                    }
                    log.info("client_factory_create_embedding model={}", model);
                    embeddingClient = new EmbeddingClient(uri, key, model);
                }
            }
        }
        return embeddingClient;
    }

    // ============================================================
    // LLM
    // ============================================================

    public LlmClient getLlmClient() {
        if (llmClient == null) {
            synchronized (this) {
                if (llmClient == null) {
                    String uri = metaStore.getConfig("api.llm_api_uri");
                    String key = metaStore.getConfig("api.llm_api_key");
                    String model = metaStore.getConfig("api.llm_model_name");
                    if (uri == null || uri.isEmpty()) {
                        throw new RuntimeException("LLM API 未配置，请在系统管理页面中设置");
                    }
                    log.info("client_factory_create_llm model={}", model);
                    llmClient = new LlmClient(uri, key, model);
                }
            }
        }
        // Apply latest LLM params from DB each time
        applyLlmParams(llmClient);
        return llmClient;
    }

    /**
     * Create a LlmClient with a specific model name (for agent overrides).
     * Uses the same API URL/key from DB.
     */
    public LlmClient createLlmClient(String modelName) {
        String uri = metaStore.getConfig("api.llm_api_uri");
        String key = metaStore.getConfig("api.llm_api_key");
        if (uri == null || uri.isEmpty()) {
            throw new RuntimeException("LLM API 未配置，请在系统管理页面中设置");
        }
        LlmClient client = new LlmClient(uri, key, modelName != null && !modelName.isEmpty() ? modelName : metaStore.getConfig("api.llm_model_name"));
        applyLlmParams(client);
        return client;
    }

    // ============================================================
    // Rerank
    // ============================================================

    /**
     * Returns the RerankClient, or null if not configured.
     */
    public RerankClient getRerankClient() {
        // Not cached via double-check — check config each time since rerank is optional
        String uri = metaStore.getConfig("api.rerank_api_uri");
        String model = metaStore.getConfig("api.rerank_model_name");
        if (uri == null || uri.isEmpty() || model == null || model.isEmpty()) {
            return null;
        }
        if (rerankClient == null) {
            synchronized (this) {
                if (rerankClient == null) {
                    String key = metaStore.getConfig("api.rerank_api_key");
                    log.info("client_factory_create_rerank model={}", model);
                    rerankClient = new RerankClient(uri, key, model);
                }
            }
        }
        return rerankClient;
    }

    // ============================================================
    // Cache invalidation
    // ============================================================

    public void invalidate() {
        log.info("client_factory_cache_cleared");
        embeddingClient = null;
        llmClient = null;
        rerankClient = null;
    }

    // ============================================================
    // Helpers
    // ============================================================

    private void applyLlmParams(LlmClient client) {
        try {
            String tempStr = metaStore.getConfig("llm.temperature");
            String topPStr = metaStore.getConfig("llm.top_p");
            String maxTokStr = metaStore.getConfig("llm.max_tokens");
            double temp = tempStr != null ? Double.parseDouble(tempStr) : 0.7;
            double topP = topPStr != null ? Double.parseDouble(topPStr) : 0.9;
            int maxTok = maxTokStr != null ? Integer.parseInt(maxTokStr) : 2048;
            client.setParams(temp, topP, maxTok);
        } catch (NumberFormatException ignored) {
            client.setParams(0.7, 0.9, 2048);
        }
    }
}
