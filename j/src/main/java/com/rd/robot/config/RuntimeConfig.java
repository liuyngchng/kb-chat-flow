package com.rd.robot.config;

import com.rd.robot.model.APIConfig;
import com.rd.robot.model.Config;
import com.rd.robot.model.FaqConfig;
import com.rd.robot.model.KBConfig;
import com.rd.robot.model.LLMParams;
import com.rd.robot.repository.MetaStore;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Map;

/**
 * Runtime configuration management.
 * Loads config from DB (sys_config table) and applies to the Config object.
 * YAML values serve as seeds; DB values take precedence at runtime.
 */
public class RuntimeConfig {

    private static final Logger log = LoggerFactory.getLogger(RuntimeConfig.class);

    private RuntimeConfig() {}

    /**
     * Load runtime config from DB and apply to the Config object.
     * Seeds default configs if the DB table is empty.
     */
    public static void load(MetaStore metaStore, Config cfg) {
        metaStore.seedDefaultConfigs();

        Map<String, String> configs = metaStore.getAllConfigs();
        apply(configs, cfg);
        log.info("runtime_config_loaded");
    }

    /**
     * Reload runtime config from DB (used after config update).
     */
    public static void reload(MetaStore metaStore, Config cfg) {
        Map<String, String> configs = metaStore.getAllConfigs();
        apply(configs, cfg);
        log.info("runtime_config_reloaded");
    }

    private static void apply(Map<String, String> configs, Config cfg) {
        // sys.name / sys.auth come from cfg.yml only (bootstrap config)
        if (configs.containsKey("sys.api_auth")) {
            cfg.getSys().setApiAuth("true".equals(configs.get("sys.api_auth")));
        }
        if (configs.containsKey("sys.work_mode")) {
            cfg.getSys().setWorkMode(parseInt(configs.get("sys.work_mode"), 0));
        }
        if (configs.containsKey("sys.default_workflow_id")) {
            cfg.getSys().setDefaultWorkflowId(parseLong(configs.get("sys.default_workflow_id"), 0));
        }
        if (cfg.getApi() == null) {
            cfg.setApi(new APIConfig());
        }
        if (configs.containsKey("api.llm_api_uri")) {
            cfg.getApi().setLlmApiUri(configs.get("api.llm_api_uri"));
        }
        if (configs.containsKey("api.llm_api_key")) {
            cfg.getApi().setLlmApiKey(configs.get("api.llm_api_key"));
        }
        if (configs.containsKey("api.llm_model_name")) {
            cfg.getApi().setLlmModelName(configs.get("api.llm_model_name"));
        }
        if (configs.containsKey("api.embedding_api_uri")) {
            cfg.getApi().setEmbeddingApiUri(configs.get("api.embedding_api_uri"));
        }
        if (configs.containsKey("api.embedding_api_key")) {
            cfg.getApi().setEmbeddingApiKey(configs.get("api.embedding_api_key"));
        }
        if (configs.containsKey("api.embedding_model_name")) {
            cfg.getApi().setEmbeddingModelName(configs.get("api.embedding_model_name"));
        }
        if (configs.containsKey("api.rerank_api_uri")) {
            cfg.getApi().setRerankApiUri(configs.get("api.rerank_api_uri"));
        }
        if (configs.containsKey("api.rerank_api_key")) {
            cfg.getApi().setRerankApiKey(configs.get("api.rerank_api_key"));
        }
        if (configs.containsKey("api.rerank_model_name")) {
            cfg.getApi().setRerankModelName(configs.get("api.rerank_model_name"));
        }

        // KB params
        if (cfg.getKb() == null) {
            cfg.setKb(new KBConfig());
        }
        if (configs.containsKey("kb.chunk_size")) {
            cfg.getKb().setChunkSize(parseInt(configs.get("kb.chunk_size"), 300));
        }
        if (configs.containsKey("kb.chunk_overlap")) {
            cfg.getKb().setChunkOverlap(parseInt(configs.get("kb.chunk_overlap"), 80));
        }
        // 兜底：overlap 必须严格小于 chunkSize 的一定比例，否则文本切分会死循环
        if (cfg.getKb().getChunkOverlap() >= cfg.getKb().getChunkSize()) {
            cfg.getKb().setChunkOverlap(cfg.getKb().getChunkSize() / 3);
        }
        if (configs.containsKey("kb.top_k")) {
            cfg.getKb().setTopK(parseInt(configs.get("kb.top_k"), 3));
        }
        if (configs.containsKey("kb.score_threshold")) {
            cfg.getKb().setScoreThreshold(parseDouble(configs.get("kb.score_threshold"), 0.1));
        }
        if (configs.containsKey("kb.rerank_enabled")) {
            cfg.getKb().setRerankEnabled("true".equals(configs.get("kb.rerank_enabled")));
        }
        if (configs.containsKey("kb.rerank_retrieve_n")) {
            cfg.getKb().setRerankRetrieveN(parseInt(configs.get("kb.rerank_retrieve_n"), 15));
        }

        // LLM params
        if (cfg.getLlm() == null) {
            cfg.setLlm(new LLMParams());
        }
        if (configs.containsKey("llm.temperature")) {
            cfg.getLlm().setTemperature(parseDouble(configs.get("llm.temperature"), 0.7));
        }
        if (configs.containsKey("llm.top_p")) {
            cfg.getLlm().setTopP(parseDouble(configs.get("llm.top_p"), 0.9));
        }
        if (configs.containsKey("llm.max_tokens")) {
            cfg.getLlm().setMaxTokens(parseInt(configs.get("llm.max_tokens"), 2048));
        }

        // FAQ params
        if (cfg.getFaq() == null) {
            cfg.setFaq(new FaqConfig());
        }
        if (configs.containsKey("faq.match_threshold")) {
            cfg.getFaq().setMatchThreshold(parseDouble(configs.get("faq.match_threshold"), 0.85));
        }
    }

    private static int parseInt(String s, int defaultValue) {
        try { return Integer.parseInt(s); } catch (NumberFormatException e) { return defaultValue; }
    }

    private static long parseLong(String s, long defaultValue) {
        try { return Long.parseLong(s); } catch (NumberFormatException e) { return defaultValue; }
    }

    private static double parseDouble(String s, double defaultValue) {
        try { return Double.parseDouble(s); } catch (NumberFormatException e) { return defaultValue; }
    }
}