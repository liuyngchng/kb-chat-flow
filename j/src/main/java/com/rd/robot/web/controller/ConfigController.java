package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.client.ClientFactory;
import com.rd.robot.client.EmbeddingClient;
import com.rd.robot.client.LlmClient;
import com.rd.robot.client.RerankClient;
import com.rd.robot.config.RuntimeConfig;
import com.rd.robot.model.Config;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * System configuration controller.
 */
public class ConfigController {

    private static final Logger log = LoggerFactory.getLogger(ConfigController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    /** overlap 允许占 chunkSize 的最大比例（百分比），避免文本切分死循环 */
    private static final int MAX_CHUNK_OVERLAP_RATIO = 30;

    private final Config cfg;
    private final MetaStore metaStore;
    private final ClientFactory clientFactory;

    public ConfigController(Config cfg, MetaStore metaStore, ClientFactory clientFactory) {
        this.cfg = cfg;
        this.metaStore = metaStore;
        this.clientFactory = clientFactory;
    }

    /**
     * GET /api/config — get all configuration
     */
    public void getConfig(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            Map<String, Object> data = new java.util.LinkedHashMap<>();
            data.put("sys", _map(
                    "name", cfg.getSys().getName(),
                    "auth", cfg.getSys().isAuth() ? "true" : "false",
                    "api_auth", cfg.getSys().isApiAuth() ? "true" : "false",
                    "work_mode", cfg.getSys().getWorkMode(),
                    "default_workflow_id", cfg.getSys().getDefaultWorkflowId()));
            data.put("api", _map(
                    "llm_api_uri", cfg.getApi().getLlmApiUri(),
                    "llm_api_key", cfg.getApi().getLlmApiKey(),
                    "llm_model_name", cfg.getApi().getLlmModelName(),
                    "embedding_api_uri", cfg.getApi().getEmbeddingApiUri(),
                    "embedding_api_key", cfg.getApi().getEmbeddingApiKey(),
                    "embedding_model_name", cfg.getApi().getEmbeddingModelName(),
                    "rerank_api_uri", cfg.getApi().getRerankApiUri(),
                    "rerank_api_key", cfg.getApi().getRerankApiKey(),
                    "rerank_model_name", cfg.getApi().getRerankModelName()
            ));
            data.put("prompt", _map("chat_msg", getPrompt()));
            data.put("kb", _map(
                    "chunk_size", cfg.getKb().getChunkSize(),
                    "chunk_overlap", cfg.getKb().getChunkOverlap(),
                    "top_k", cfg.getKb().getTopK(),
                    "score_threshold", cfg.getKb().getScoreThreshold(),
                    "rerank_enabled", cfg.getKb().isRerankEnabled(),
                    "rerank_retrieve_n", cfg.getKb().getRerankRetrieveN()
            ));
            data.put("llm", _map(
                    "temperature", cfg.getLlm().getTemperature(),
                    "top_p", cfg.getLlm().getTopP(),
                    "max_tokens", cfg.getLlm().getMaxTokens()
            ));
            data.put("faq", _map("match_threshold", cfg.getFaq().getMatchThreshold()));
            Map<String, Object> resp = Map.of("data", data);
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            log.error("config_handler_get_config_failed", e);
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "获取配置失败: " + e.getMessage());
        }
    }

    /** null-safe Map builder from key-value pairs (allows null values, unlike Map.of) */
    private static Map<String, Object> _map(Object... kvs) {
        Map<String, Object> m = new java.util.LinkedHashMap<>();
        for (int i = 0; i < kvs.length; i += 2) {
            m.put((String) kvs[i], kvs[i + 1]);
        }
        return m;
    }

    /**
     * PUT /api/config — update configuration
     */
    public void updateConfig(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            @SuppressWarnings("unchecked")
            Map<String, Object> req = MAPPER.readValue(body, Map.class);

            @SuppressWarnings("unchecked")
            Map<String, Object> sys = (Map<String, Object>) req.get("sys");
            @SuppressWarnings("unchecked")
            Map<String, Object> api = (Map<String, Object>) req.get("api");
            @SuppressWarnings("unchecked")
            Map<String, Object> prompt = (Map<String, Object>) req.get("prompt");
            @SuppressWarnings("unchecked")
            Map<String, Object> kb = (Map<String, Object>) req.get("kb");
            @SuppressWarnings("unchecked")
            Map<String, Object> llm = (Map<String, Object>) req.get("llm");
            @SuppressWarnings("unchecked")
            Map<String, Object> faq = (Map<String, Object>) req.get("faq");

            if (sys != null) {
                if (sys.get("name") != null && !((String) sys.get("name")).isEmpty())
                    metaStore.setConfig("sys.name", (String) sys.get("name"), "系统名称");
                // sys.auth is read-only from cfg.yml, not updateable via page
                if (sys.get("api_auth") != null) metaStore.setConfig("sys.api_auth", (String) sys.get("api_auth"), "是否启用接口认证");
                // 工作模式（始终保存）
                if (sys.get("work_mode") != null) metaStore.setConfig("sys.work_mode", String.valueOf(sys.get("work_mode")), "工作模式: 0=KB, 1=CSM, 2=动态工作流");
                // 动态工作流 ID
                if (sys.get("default_workflow_id") != null) metaStore.setConfig("sys.default_workflow_id", String.valueOf(sys.get("default_workflow_id")), "动态工作流 ID");
            }

            if (api != null) {
                setConfigIfPresent(api, "llm_api_uri", "api.llm_api_uri");
                setConfigIfPresent(api, "llm_api_key", "api.llm_api_key");
                setConfigIfPresent(api, "llm_model_name", "api.llm_model_name");
                setConfigIfPresent(api, "embedding_api_uri", "api.embedding_api_uri");
                setConfigIfPresent(api, "embedding_api_key", "api.embedding_api_key");
                setConfigIfPresent(api, "embedding_model_name", "api.embedding_model_name");
                setConfigIfPresent(api, "rerank_api_uri", "api.rerank_api_uri");
                setConfigIfPresent(api, "rerank_api_key", "api.rerank_api_key");
                setConfigIfPresent(api, "rerank_model_name", "api.rerank_model_name");
            }

            if (prompt != null && prompt.get("chat_msg") != null) {
                metaStore.upsertPrompt("chat_msg", (String) prompt.get("chat_msg"), 0);
            }

            if (kb != null) {
                setConfigIfPresentInt(kb, "chunk_size", "kb.chunk_size");
                // 校验 overlap：必须严格小于 chunkSize 的一定比例，否则文本切分步长为 0 会死循环
                Object overlapVal = kb.get("chunk_overlap");
                if (overlapVal != null) {
                    int chunkSize = cfg.getKb().getChunkSize();
                    Object sizeVal = kb.get("chunk_size");
                    if (sizeVal instanceof Number) {
                        chunkSize = ((Number) sizeVal).intValue();
                    }
                    int overlap = Integer.parseInt(String.valueOf(overlapVal));
                    int maxOverlap = chunkSize * MAX_CHUNK_OVERLAP_RATIO / 100;
                    if (overlap >= chunkSize) {
                        HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.BAD_REQUEST,
                                "分片重叠必须小于分片大小（当前 " + overlap + " ≥ " + chunkSize + "）");
                        return;
                    }
                    if (overlap > maxOverlap) {
                        HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.BAD_REQUEST,
                                "分片重叠过大，最多为分片大小的 " + MAX_CHUNK_OVERLAP_RATIO + "%（" + maxOverlap + "），当前 " + overlap);
                        return;
                    }
                    metaStore.setConfig("kb.chunk_overlap", String.valueOf(overlap), "");
                }
                setConfigIfPresentInt(kb, "top_k", "kb.top_k");
                setConfigIfPresentDouble(kb, "score_threshold", "kb.score_threshold");
                setConfigIfPresentBool(kb, "rerank_enabled", "kb.rerank_enabled");
                setConfigIfPresentInt(kb, "rerank_retrieve_n", "kb.rerank_retrieve_n");
            }

            if (llm != null) {
                setConfigIfPresentDouble(llm, "temperature", "llm.temperature");
                setConfigIfPresentDouble(llm, "top_p", "llm.top_p");
                setConfigIfPresentInt(llm, "max_tokens", "llm.max_tokens");
            }

            if (faq != null) {
                setConfigIfPresentDouble(faq, "match_threshold", "faq.match_threshold");
            }

            // Reload runtime config and invalidate client cache
            RuntimeConfig.reload(metaStore, cfg);
            clientFactory.invalidate();

            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");

        } catch (Exception e) {
            log.error("config_handler_update_config_failed", e);
            String errMsg = e.getMessage() != null ? e.getMessage() : "未知错误";
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "更新配置失败: " + errMsg);
        }
    }

    /**
     * POST /api/config/test-models — test API connectivity.
     */
    public void testModels(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            @SuppressWarnings("unchecked")
            Map<String, Object> req = MAPPER.readValue(body, Map.class);

            List<Map<String, Object>> results = new ArrayList<>();

            // 1. Test LLM model
            String llmUri = (String) req.get("llm_api_uri");
            if (llmUri != null && !llmUri.isEmpty()) {
                long t0 = System.currentTimeMillis();
                try {
                    String llmKey = (String) req.get("llm_api_key");
                    String llmModel = (String) req.get("llm_model_name");
                    LlmClient client = new LlmClient(llmUri, llmKey, llmModel);
                    client.chat("你是一个助手，请回复 OK。", "hi");
                    long elapsed = System.currentTimeMillis() - t0;
                    results.add(_map("name", "LLM 对话模型", "ok", true, "message", "连接成功", "elapsed_ms", elapsed));
                } catch (Exception e) {
                    long elapsed = System.currentTimeMillis() - t0;
                    log.warn("config_handler_model_test_llm_failed error={}", e.getMessage());
                    results.add(_map("name", "LLM 对话模型", "ok", false, "message", e.getMessage(), "elapsed_ms", elapsed));
                }
            } else {
                results.add(_map("name", "LLM 对话模型", "ok", false, "message", "未配置 API 地址"));
            }

            // 2. Test Embedding model
            String embUri = (String) req.get("embedding_api_uri");
            if (embUri != null && !embUri.isEmpty()) {
                long t0 = System.currentTimeMillis();
                try {
                    String embKey = (String) req.get("embedding_api_key");
                    String embModel = (String) req.get("embedding_model_name");
                    EmbeddingClient client = new EmbeddingClient(embUri, embKey, embModel);
                    int dim = client.dimension();
                    long elapsed = System.currentTimeMillis() - t0;
                    results.add(_map("name", "Embedding 向量模型", "ok", true, "message", "连接成功 (dim=" + dim + ")", "elapsed_ms", elapsed));
                } catch (Exception e) {
                    long elapsed = System.currentTimeMillis() - t0;
                    log.warn("config_handler_model_test_embedding_failed error={}", e.getMessage());
                    results.add(_map("name", "Embedding 向量模型", "ok", false, "message", e.getMessage(), "elapsed_ms", elapsed));
                }
            } else {
                results.add(_map("name", "Embedding 向量模型", "ok", false, "message", "未配置 API 地址"));
            }

            // 3. Test Rerank model
            String rerankUri = (String) req.get("rerank_api_uri");
            if (rerankUri != null && !rerankUri.isEmpty()) {
                long t0 = System.currentTimeMillis();
                try {
                    String rerankKey = (String) req.get("rerank_api_key");
                    String rerankModel = (String) req.get("rerank_model_name");
                    RerankClient client = new RerankClient(rerankUri, rerankKey, rerankModel);
                    client.rerank("test", List.of("hello world", "goodbye"), 1);
                    long elapsed = System.currentTimeMillis() - t0;
                    results.add(_map("name", "Rerank 重排序模型", "ok", true, "message", "连接成功", "elapsed_ms", elapsed));
                } catch (Exception e) {
                    long elapsed = System.currentTimeMillis() - t0;
                    log.warn("config_handler_model_test_rerank_failed error={}", e.getMessage());
                    results.add(_map("name", "Rerank 重排序模型", "ok", false, "message", e.getMessage(), "elapsed_ms", elapsed));
                }
            } else {
                results.add(_map("name", "Rerank 重排序模型", "ok", false, "message", "未配置 API 地址"));
            }

            boolean allOK = results.stream().allMatch(r -> Boolean.TRUE.equals(r.get("ok")));

            Map<String, Object> resp = new java.util.LinkedHashMap<>();
            resp.put("results", results);
            resp.put("all_ok", allOK);
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));

        } catch (Exception e) {
            log.error("config_handler_model_test_error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"模型测试失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/info — service information
     */
    public void info(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            Map<String, Object> resp = new java.util.LinkedHashMap<>();
            resp.put("name", cfg.getSys().getName());
            resp.put("version", "1.0.0");
            resp.put("server_mode", cfg.getServer().getMode());
            resp.put("server_role", cfg.getServer().getRole());
            resp.put("work_mode", cfg.getSys().getWorkMode());
            resp.put("vector_backend", cfg.getVector() != null ? cfg.getVector().getBackend() : "local");
            resp.put("store_backend", cfg.getStore() != null ? cfg.getStore().getBackend() : "sqlite");
            resp.put("supported_file_types", List.of("txt", "md", "pdf", "docx", "xlsx"));
            resp.put("api_auth_enabled", cfg.getSys().isApiAuth());
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    private String getPrompt() {
        if (metaStore != null) {
            String prompt = metaStore.getPrompt("chat_msg");
            if (prompt != null && !prompt.isEmpty()) return prompt;
        }
        return "你是专业的对话机器人，负责解答客户咨询。";
    }

    private void setConfigIfPresent(Map<String, Object> map, String key, String configKey) {
        if (map.get(key) != null) {
            metaStore.setConfig(configKey, String.valueOf(map.get(key)), "");
        }
    }

    private void setConfigIfPresentInt(Map<String, Object> map, String key, String configKey) {
        if (map.get(key) != null) {
            metaStore.setConfig(configKey, String.valueOf(map.get(key)), "");
        }
    }

    private void setConfigIfPresentDouble(Map<String, Object> map, String key, String configKey) {
        if (map.get(key) != null) {
            metaStore.setConfig(configKey, String.valueOf(map.get(key)), "");
        }
    }

    private void setConfigIfPresentBool(Map<String, Object> map, String key, String configKey) {
        if (map.get(key) != null) {
            metaStore.setConfig(configKey, String.valueOf(map.get(key)), "");
        }
    }
}