package com.rd.robot.engine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.client.ClientFactory;
import com.rd.robot.client.EmbeddingClient;
import com.rd.robot.client.LlmClient;
import com.rd.robot.fasttext.FastTextPredictor;
import com.rd.robot.knowledge.KnowledgeBaseManager;
import com.rd.robot.model.ClassifierDef;
import com.rd.robot.model.Config;
import com.rd.robot.model.EngineEvent;
import com.rd.robot.model.IntentCategory;
import com.rd.robot.repository.MetaStore;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Collections;
import java.util.List;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * Hard-coded customer service Q&A logic (CSM = Customer Service Module).
 *
 * Background: the dynamic workflow config (cfg.db workflow_def) already supports complex
 * orchestration, but daily business has high configuration cost. This class hard-codes the
 * "燃气客服" (gas customer service) Q&A flow, bypassing the database workflow config.
 *
 * Logic mirrors workflow 1 (燃气客服工作流) in cfg.db:
 *   intent classification (emergency/billing/business/repair/faq)
 *   -> route by intent -> (emergency/business answer directly, billing/repair/faq search KB first)
 *   -> LLM streaming final answer
 *
 * The dynamic config logic (WorkflowEngine / IntentClassifier / TemplateResolver) is unaffected.
 *
 * This is a Java port of Go's csm.go. Event protocol fully reuses EngineEvent
 * (progress / chunk / done / error), so the handler's SSE loop needs no changes.
 */
public class CsmEngine {

    private static final Logger log = LoggerFactory.getLogger(CsmEngine.class);

    /** Total steps in the hard-coded flow (0=intent classify, 1=retrieve, 2=answer). */
    private static final int CSM_TOTAL_STEP = 3;

    /** Default KB IDs when no binding is configured. */
    private static final List<Long> DEFAULT_VDB_IDS = List.of(3L);

    /** Config keys for CSM branch KB bindings. */
    private static final String CFG_KEY_BILLING = "csm.billing_vdb_ids";
    private static final String CFG_KEY_REPAIR = "csm.repair_vdb_ids";
    private static final String CFG_KEY_FAQ = "csm.faq_vdb_ids";

    /** Hard-coded intent classifier config, identical to workflow 1's classifier. */
    private static final ClassifierDef CSM_CLASSIFIER = buildClassifier();

    /** Emergency dispatch prompt: no KB retrieval, answer directly. */
    private static final String CSM_EMERGENCY_PROMPT = "你是燃气公司紧急调度员。用户遇到了紧急情况，你必须优先处理。\n" +
            "请引导用户立即采取安全措施：关闭燃气阀门、开窗通风、禁止明火、撤离现场，\n" +
            "同时告知用户已安排紧急维修人员尽快到达。\n" +
            "语气要冷静、专业，给用户安全感。";

    /** Billing prompt: answer based on retrieved KB results. */
    private static final String CSM_BILLING_PROMPT = "你是燃气公司账单客服。根据检索到的账单信息，帮助用户解决账单查询、缴费方式、欠费处理等问题。\n" +
            "用亲切专业的中文回答，引导用户完成缴费操作。";

    /** Business prompt: no KB retrieval, answer directly. */
    private static final String CSM_BUSINESS_PROMPT = "你是燃气公司业务办理专员。帮助用户办理开户、过户、改名、报装、停气等业务。\n" +
            "请告知用户所需材料、办理流程和注意事项。\n" +
            "语气亲切、专业，一步步引导用户完成业务办理。";

    /** Repair prompt: answer based on retrieved KB results. */
    private static final String CSM_REPAIR_PROMPT = "你是燃气公司维修客服。根据检索到的维修信息，帮助用户进行故障诊断、保养指导、报修登记。\n" +
            "对于简单故障给出排查建议，无法解决的安排维修人员上门。\n" +
            "语气专业、耐心。";

    /** FAQ prompt: answer based on retrieved KB results. */
    private static final String CSM_FAQ_PROMPT = "你是燃气公司综合客服。根据检索到的FAQ信息，回答用户的各种常见问题，\n" +
            "如营业时间、服务电话、地址、投诉渠道等。\n" +
            "语气亲切、专业，解答清晰明了。";

    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final Config cfg;
    private final KnowledgeBaseManager kbMgr;
    private final ClientFactory clientFactory;
    private final FastTextPredictor ftPredictor;
    private final MetaStore store;

    // Dynamic KB bindings (loaded from sys_config, hot-reloadable)
    private final ReadWriteLock bindingLock = new ReentrantReadWriteLock();
    private List<Long> billingVdbIDs = DEFAULT_VDB_IDS;
    private List<Long> repairVdbIDs = DEFAULT_VDB_IDS;
    private List<Long> faqVdbIDs = DEFAULT_VDB_IDS;

    public CsmEngine(Config cfg, KnowledgeBaseManager kbMgr, ClientFactory clientFactory, MetaStore store) {
        this.cfg = cfg;
        this.kbMgr = kbMgr;
        this.clientFactory = clientFactory;
        this.store = store;
        this.ftPredictor = new FastTextPredictor();
        loadVdbBindings();
    }

    /**
     * Execute the hard-coded CSM flow with streaming events.
     * Signature mirrors WorkflowEngine.executeStream; workflowId is retained only for
     * call compatibility and is no longer used to load DB config.
     */
    public BlockingQueue<EngineEvent> executeStreamCSM(long workflowId, String userQuery, String uid,
                                                       List<TemplateResolver.ChatMsg> messages) {
        BlockingQueue<EngineEvent> eventQueue = new LinkedBlockingQueue<>(100);

        new Thread(() -> {
            try {
                csmRun(eventQueue, userQuery, uid, messages.size());
            } catch (Exception e) {
                log.error("csm_execution_error", e);
                eventQueue.offer(new EngineEvent("error", "CSM 执行失败: " + e.getMessage()));
            }
        }).start();

        return eventQueue;
    }

    /** Hard-coded flow main logic. */
    private void csmRun(BlockingQueue<EngineEvent> eventQueue, String userQuery, String uid, int historyCount) {
        long runStart = System.currentTimeMillis();
        log.info("csm_run_start uid={} query={} query_len={} history={}",
                uid, truncate(userQuery, 80), userQuery.length(), historyCount);

        // 1. Intent classification (multi-tier: keyword -> fastText -> semantic -> LLM -> fallback)
        try {
            ftPredictor.train(CSM_CLASSIFIER.getCategories(), CSM_CLASSIFIER.getPrompt());
        } catch (Exception e) {
            log.warn("csm_fasttext_train_failed {}", e.getMessage());
        }
        long classifyStart = System.currentTimeMillis();
        String intent = IntentClassifier.classify(
                CSM_CLASSIFIER, userQuery,
                clientFactory.getLlmClient(),
                clientFactory.getEmbeddingClient(),
                ftPredictor);
        if (intent == null || intent.isEmpty()) {
            // classify should at least fall back to the last category; defensive fallback
            intent = "faq";
        }
        log.info("csm_classify_done intent={} duration_ms={} query={}",
                intent, System.currentTimeMillis() - classifyStart, truncate(userQuery, 80));
        eventQueue.offer(new EngineEvent("progress", 0, CSM_TOTAL_STEP, "意图分类: " + intent, ""));

        // 2. Route by intent
        String branch = csmBranchName(intent);
        log.info("csm_route intent={} branch={}", intent, branch);

        switch (intent) {
            case "emergency":
                csmAnswerDirect(eventQueue, "紧急调度", CSM_EMERGENCY_PROMPT, userQuery);
                break;
            case "billing":
                csmAnswerWithKB(eventQueue, "账单检索", "账单客服", CSM_BILLING_PROMPT, userQuery, uid, billingVdbIDsSnapshot());
                break;
            case "business":
                csmAnswerDirect(eventQueue, "业务办理", CSM_BUSINESS_PROMPT, userQuery);
                break;
            case "repair":
                csmAnswerWithKB(eventQueue, "维修检索", "维修客服", CSM_REPAIR_PROMPT, userQuery, uid, repairVdbIDsSnapshot());
                break;
            default: // faq / unrecognized
                csmAnswerWithKB(eventQueue, "FAQ检索", "综合FAQ", CSM_FAQ_PROMPT, userQuery, uid, faqVdbIDsSnapshot());
                break;
        }

        // 3. Done — emitted inside csmStream's onDone callback, so it always fires AFTER
        //    the last LLM chunk. Doing it here would race the async chatStream and the
        //    consumer would close the SSE connection before the final chunks arrive.
        log.info("csm_run_done intent={} total_ms={}", intent, System.currentTimeMillis() - runStart);
    }

    /** Return the route branch description for an intent (log only). */
    private static String csmBranchName(String intent) {
        switch (intent) {
            case "emergency": return "emergency -> 紧急调度（直接回答）";
            case "billing":   return "billing -> 账单检索 + 账单客服";
            case "business":  return "business -> 业务办理（直接回答）";
            case "repair":    return "repair -> 维修检索 + 维修客服";
            default:          return "faq -> FAQ检索 + 综合FAQ";
        }
    }

    /** Answer directly without KB retrieval (emergency dispatch / business). */
    private void csmAnswerDirect(BlockingQueue<EngineEvent> eventQueue,
                                 String agentName, String systemPrompt, String userQuery) {
        eventQueue.offer(new EngineEvent("progress", 2, CSM_TOTAL_STEP, agentName, ""));
        csmStream(eventQueue, agentName, systemPrompt, userQuery);
    }

    /** Search KB first, then answer based on retrieved context (billing / repair / faq). */
    private void csmAnswerWithKB(BlockingQueue<EngineEvent> eventQueue,
                                 String retrieveAgent, String answerAgent,
                                 String systemPrompt, String userQuery, String uid,
                                 List<Long> vdbIds) {
        eventQueue.offer(new EngineEvent("progress", 1, CSM_TOTAL_STEP, retrieveAgent, ""));

        String kbContext = csmSearchKB(userQuery, uid, vdbIds);

        // Matches workflow node InputTemplate "用户问题：{{user_query}}\n检索信息：{{xx_ctx}}"
        String userMessage = "用户问题：" + userQuery + "\n检索信息：" + kbContext;

        eventQueue.offer(new EngineEvent("progress", 2, CSM_TOTAL_STEP, answerAgent, ""));
        csmStream(eventQueue, answerAgent, systemPrompt, userMessage);
    }

    /** Search the given KB list and join context. */
    private String csmSearchKB(String userQuery, String uid, List<Long> vdbIds) {
        long start = System.currentTimeMillis();
        log.info("csm_kb_search_start vdb_ids={} query={}", vdbIds, truncate(userQuery, 80));

        StringBuilder sb = new StringBuilder();
        for (long vdbId : vdbIds) {
            try {
                String ctx = kbMgr.searchInKB(userQuery, vdbId, uid,
                        cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
                if (ctx != null && !ctx.isEmpty()) {
                    sb.append(ctx).append("\n");
                }
            } catch (Exception e) {
                log.warn("csm_kb_search_failed vdb_id={} error={}", vdbId, e.getMessage());
            }
        }
        log.info("csm_kb_search_done duration_ms={} context_len={}",
                System.currentTimeMillis() - start, sb.length());
        return sb.toString();
    }

    // ============================================================
    // Dynamic KB Bindings (loaded from sys_config, hot-reloadable)
    // ============================================================

    /** Load KB bindings from sys_config. */
    public void loadVdbBindings() {
        bindingLock.writeLock().lock();
        try {
            billingVdbIDs = loadVdbIDs(CFG_KEY_BILLING);
            repairVdbIDs = loadVdbIDs(CFG_KEY_REPAIR);
            faqVdbIDs = loadVdbIDs(CFG_KEY_FAQ);
            log.info("csm_vdb_bindings_loaded billing={} repair={} faq={}",
                    billingVdbIDs, repairVdbIDs, faqVdbIDs);
        } finally {
            bindingLock.writeLock().unlock();
        }
    }

    /** Reload bindings after config update (hot-reload, takes effect immediately). */
    public void reloadVdbBindings() {
        loadVdbBindings();
    }

    private List<Long> loadVdbIDs(String key) {
        if (store == null) return DEFAULT_VDB_IDS;
        try {
            String val = store.getConfig(key);
            if (val == null || val.trim().isEmpty()) return DEFAULT_VDB_IDS;
            List<Long> ids = MAPPER.readValue(val, new TypeReference<List<Long>>() {});
            if (ids == null || ids.isEmpty()) return DEFAULT_VDB_IDS;
            return ids;
        } catch (Exception e) {
            log.warn("csm_vdb_binding_parse_failed key={} error={}", key, e.getMessage());
            return DEFAULT_VDB_IDS;
        }
    }

    private List<Long> billingVdbIDsSnapshot() {
        bindingLock.readLock().lock();
        try { return billingVdbIDs; } finally { bindingLock.readLock().unlock(); }
    }

    private List<Long> repairVdbIDsSnapshot() {
        bindingLock.readLock().lock();
        try { return repairVdbIDs; } finally { bindingLock.readLock().unlock(); }
    }

    private List<Long> faqVdbIDsSnapshot() {
        bindingLock.readLock().lock();
        try { return faqVdbIDs; } finally { bindingLock.readLock().unlock(); }
    }

    /** Return current billing branch KB IDs (for handler display). */
    public List<Long> billingVdbIDs() { return billingVdbIDsSnapshot(); }

    /** Return current repair branch KB IDs (for handler display). */
    public List<Long> repairVdbIDs() { return repairVdbIDsSnapshot(); }

    /** Return current FAQ branch KB IDs (for handler display). */
    public List<Long> faqVdbIDs() { return faqVdbIDsSnapshot(); }

    /** Stream the LLM response, emitting chunk events; emits the final "done" after streaming ends. */
    private void csmStream(BlockingQueue<EngineEvent> eventQueue,
                           String agentName, String systemPrompt, String userMessage) {
        long start = System.currentTimeMillis();
        LlmClient llmClient = clientFactory.getLlmClient();
        log.info("csm_llm_start agent={} model={} prompt_len={} input_len={}",
                agentName, llmClient.getModelName(), systemPrompt.length(), userMessage.length());

        StringBuilder output = new StringBuilder();
        int[] chunkCount = {0};

        llmClient.chatStream(systemPrompt, userMessage,
                chunk -> {
                    output.append(chunk);
                    chunkCount[0]++;
                    eventQueue.offer(new EngineEvent("chunk", 2, CSM_TOTAL_STEP, agentName, chunk));
                },
                error -> {
                    log.error("csm_llm_error agent={} error={} duration_ms={} chunks={}",
                            agentName, error, System.currentTimeMillis() - start, chunkCount[0]);
                    eventQueue.offer(new EngineEvent("error", "[错误] " + error));
                },
                () -> {
                    log.info("csm_llm_done agent={} duration_ms={} chunks={} output_len={} output_preview={}",
                            agentName, System.currentTimeMillis() - start, chunkCount[0], output.length(),
                            truncate(output.toString(), 80));
                    // done must fire only after the async chatStream fully completes,
                    // so the SSE consumer does not close before the last chunk arrives.
                    eventQueue.offer(new EngineEvent("done", CSM_TOTAL_STEP, CSM_TOTAL_STEP, "", ""));
                });
    }

    /** Truncate a string for log preview (by code points to avoid breaking Chinese). */
    private static String truncate(String s, int n) {
        if (s == null) return "";
        if (s.codePointCount(0, s.length()) <= n) return s;
        int end = s.offsetByCodePoints(0, n);
        return s.substring(0, end) + "...";
    }

    /** Build the hard-coded classifier config, identical to Go csm.go. */
    private static ClassifierDef buildClassifier() {
        ClassifierDef classifier = new ClassifierDef();
        classifier.setOutputVar("intent");
        classifier.setPrompt("你是一个燃气公司客服意图分类器。根据用户输入，判断其意图属于以下哪个类别。\n请只输出类别名称，不要输出任何其他内容。");
        classifier.setCategories(List.of(
                category("emergency", "燃气泄漏、燃气味、报警等紧急安全情况",
                        "漏气", "燃气味", "煤气味", "报警", "爆炸", "火灾", "着火", "泄漏", "冒烟", "异味", "刺鼻"),
                category("billing", "账单查询、缴费、欠费、发票等财务问题",
                        "账单", "缴费", "欠费", "余额", "发票", "价格", "费用", "多少钱", "扣费", "充值", "代扣", "阶梯价"),
                category("business", "开户、过户、改名、报装、停气等业务办理",
                        "开户", "过户", "改名", "报装", "停气", "新装", "移表", "增容", "改管", "安装", "开通", "搬迁", "换表"),
                category("repair", "燃气设备维修、故障排查、保养、安检",
                        "维修", "故障", "坏了", "打不着火", "点不着", "不着火", "保养", "安检", "检查", "熄火", "红火", "小火", "自动关", "打火"),
                category("faq", "常见综合咨询：营业时间、电话、地址、投诉建议等",
                        "营业时间", "电话", "地址", "投诉", "建议", "表扬", "几点", "在哪", "怎么去", "客服", "人工", "工作时间")
        ));
        return classifier;
    }

    private static IntentCategory category(String name, String description, String... keywords) {
        IntentCategory cat = new IntentCategory();
        cat.setName(name);
        cat.setDescription(description);
        cat.setKeywords(List.of(keywords));
        return cat;
    }
}
