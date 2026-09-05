package com.rd.robot.engine;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.client.ClientFactory;
import com.rd.robot.client.EmbeddingClient;
import com.rd.robot.client.LlmClient;
import com.rd.robot.fasttext.FastTextPredictor;
import com.rd.robot.knowledge.KnowledgeBaseManager;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.concurrent.*;
import java.util.stream.Collectors;

/**
 * Workflow execution engine.
 * Executes a workflow's nodes sequentially, supporting streaming output,
 * variable passing, KB retrieval, and multi-tier intent classification routing.
 */
public class WorkflowEngine {

    private static final Logger log = LoggerFactory.getLogger(WorkflowEngine.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final Config cfg;
    private final KnowledgeBaseManager kbMgr;
    private final MetaStore metaStore;
    private final ClientFactory clientFactory;
    private final FastTextPredictor ftPredictor;

    public WorkflowEngine(Config cfg, KnowledgeBaseManager kbMgr, MetaStore metaStore,
                          ClientFactory clientFactory) {
        this.cfg = cfg;
        this.kbMgr = kbMgr;
        this.metaStore = metaStore;
        this.clientFactory = clientFactory;
        this.ftPredictor = new FastTextPredictor();
    }

    /** Returns the embedding client (for external debug/test use). */
    public EmbeddingClient embClient() {
        return clientFactory.getEmbeddingClient();
    }

    /** Returns the fastText predictor (for external debug/test use). */
    public FastTextPredictor ftPredictor() {
        return ftPredictor;
    }

    /**
     * Execute a workflow with streaming events.
     * Returns a BlockingQueue of events that the caller can poll or iterate.
     */
    public BlockingQueue<EngineEvent> executeStream(long workflowId, String userQuery, String uid,
                                                     List<TemplateResolver.ChatMsg> messages) {
        BlockingQueue<EngineEvent> eventQueue = new LinkedBlockingQueue<>(100);

        new Thread(() -> {
            try {
                // 1. Load workflow
                WorkflowDef workflow = metaStore.getWorkflow(workflowId);
                if (workflow == null) {
                    eventQueue.offer(new EngineEvent("error", "工作流不存在"));
                    return;
                }
                if (workflow.getNodes() == null || workflow.getNodes().isEmpty()) {
                    eventQueue.offer(new EngineEvent("error", "工作流没有节点"));
                    return;
                }

                List<WorkflowNode> nodes = workflow.getNodes();
                int total = nodes.size();
                int stepCounter = 0;

                // 2. Initialize variable pool
                String curDate = java.time.LocalDate.now().toString();
                String curWeek = TemplateResolver.getWeekdayCN();

                Map<String, String> vars = new HashMap<>();
                // New names (sys. prefix)
                vars.put("sys.user_query", userQuery);
                vars.put("sys.history", TemplateResolver.formatHistory(messages));
                vars.put("sys.cur_date", curDate);
                vars.put("sys.cur_week", curWeek);
                vars.put("sys.kb_context", ""); // filled per node by KB retrieval

                // Legacy names (backward compatibility)
                vars.put("user_query", userQuery);
                vars.put("history", TemplateResolver.formatHistory(messages));
                vars.put("cur_date", curDate);
                vars.put("cur_week", curWeek);

                // 3. Intent classification (if workflow has a classifier)
                String classifierOutputVar = "intent"; // default
                if (workflow.getClassifier() != null) {
                    // Train fastText model from category keywords
                    try {
                        ftPredictor.train(workflow.getClassifier().getCategories(),
                                workflow.getClassifier().getPrompt());
                    } catch (Exception e) {
                        log.warn("engine_fasttext_train_failed {}", e.getMessage());
                    }

                    log.info("engine_classifier_start workflow={}", workflow.getName());
                    long classifyStart = System.currentTimeMillis();

                    String intent = IntentClassifier.classify(
                            workflow.getClassifier(), userQuery,
                            clientFactory.getLlmClient(),
                            clientFactory.getEmbeddingClient(),
                            ftPredictor);

                    long classifyElapsed = System.currentTimeMillis() - classifyStart;

                    classifierOutputVar = workflow.getClassifier().getOutputVar();
                    if (classifierOutputVar == null || classifierOutputVar.isEmpty()) {
                        classifierOutputVar = "intent";
                    }
                    vars.put(classifierOutputVar, intent);
                    // sys. prefixed copy for template reference
                    vars.put("sys." + classifierOutputVar, intent);

                    eventQueue.offer(new EngineEvent("progress", 0, total, "意图分类: " + intent, ""));

                    log.info("engine_classifier_done workflow={} intent={} duration_ms={} query={}",
                            workflow.getName(), intent, classifyElapsed, truncate(userQuery, 50));
                }

                // 4. Execute nodes (DAG or linear mode)
                if (hasNextNodes(nodes)) {
                    executeDAG(eventQueue, workflow, nodes, vars, classifierOutputVar, uid, userQuery);
                } else {
                    executeLinear(eventQueue, nodes, vars, classifierOutputVar, uid, userQuery);
                }

                // Send completion event
                log.info("engine_workflow_nodes_done workflow={} total_nodes={}", workflow.getName(), total);
                eventQueue.offer(new EngineEvent("done", total, total, "", ""));

            } catch (Exception e) {
                log.error("engine_workflow_execution_error", e);
                eventQueue.offer(new EngineEvent("error", "工作流执行失败: " + e.getMessage()));
            }
        }).start();

        return eventQueue;
    }

    /**
     * Non-streaming workflow execution.
     */
    public String execute(long workflowId, String userQuery, String uid,
                          List<TemplateResolver.ChatMsg> messages) throws Exception {
        StringBuilder result = new StringBuilder();
        String lastError = null;

        BlockingQueue<EngineEvent> events = executeStream(workflowId, userQuery, uid, messages);

        while (true) {
            EngineEvent evt = events.take();
            switch (evt.getType()) {
                case "chunk":
                    result.append(evt.getContent());
                    break;
                case "error":
                    lastError = evt.getContent();
                    if (result.isEmpty()) {
                        throw new RuntimeException(lastError);
                    }
                    break;
                case "done":
                    if (lastError != null && result.isEmpty()) {
                        throw new RuntimeException(lastError);
                    }
                    return result.toString();
            }
        }
    }

    // ============================================================
    // DAG execution engine
    // ============================================================

    /** DAG scheduling internal node. */
    private static class DagNode {
        WorkflowNode node;
        int inDegree;
        List<String> outEdges;
        int level;

        DagNode(WorkflowNode node) {
            this.node = node;
            this.outEdges = node.getNextNodes() != null ? node.getNextNodes() : List.of();
            this.inDegree = 0;
            this.level = 0;
        }
    }

    /** Check if any node has DAG edges defined. */
    private static boolean hasNextNodes(List<WorkflowNode> nodes) {
        return nodes.stream().anyMatch(n -> n.getNextNodes() != null && !n.getNextNodes().isEmpty());
    }

    /** Build DAG adjacency map from node list. */
    private static Map<String, DagNode> buildDAG(List<WorkflowNode> nodes) throws Exception {
        Map<String, DagNode> dag = new LinkedHashMap<>();
        for (WorkflowNode n : nodes) {
            dag.put(n.getId(), new DagNode(n));
        }
        // Compute in-degree
        for (DagNode dn : dag.values()) {
            for (String targetId : dn.outEdges) {
                DagNode target = dag.get(targetId);
                if (target == null) {
                    throw new Exception("节点 " + dn.node.getId() + " 引用了不存在的下游节点 " + targetId);
                }
                target.inDegree++;
            }
        }
        return dag;
    }

    /** Kahn's algorithm: returns nodes grouped by topological level. */
    private static List<List<WorkflowNode>> topologicalLevels(Map<String, DagNode> dag) throws Exception {
        // Collect nodes with in-degree 0
        List<DagNode> queue = new ArrayList<>();
        for (DagNode dn : dag.values()) {
            if (dn.inDegree == 0) queue.add(dn);
        }

        List<List<WorkflowNode>> levels = new ArrayList<>();
        int processed = 0;
        int total = dag.size();

        while (!queue.isEmpty()) {
            int levelSize = queue.size();
            List<WorkflowNode> level = new ArrayList<>(levelSize);
            List<DagNode> nextQueue = new ArrayList<>();

            for (int i = 0; i < levelSize; i++) {
                DagNode dn = queue.get(i);
                dn.level = levels.size();
                level.add(dn.node);
                processed++;

                for (String targetId : dn.outEdges) {
                    DagNode target = dag.get(targetId);
                    target.inDegree--;
                    if (target.inDegree == 0) {
                        nextQueue.add(target);
                    }
                }
            }

            levels.add(level);
            queue = nextQueue;
        }

        if (processed != total) {
            throw new Exception("工作流图中存在循环依赖，无法执行");
        }

        return levels;
    }

    /** Validate that the workflow graph is well-formed. */
    public static void validateWorkflowGraph(List<WorkflowNode> nodes) throws Exception {
        if (nodes == null || nodes.isEmpty()) {
            throw new Exception("工作流至少需要一个节点");
        }

        // Check all NextNodes references exist
        Set<String> nodeIds = nodes.stream().map(WorkflowNode::getId).collect(Collectors.toSet());
        for (WorkflowNode n : nodes) {
            if (n.getNextNodes() != null) {
                for (String targetId : n.getNextNodes()) {
                    if (!nodeIds.contains(targetId)) {
                        throw new Exception("节点 " + n.getId() + " 引用了不存在的下游节点 " + targetId);
                    }
                    if (targetId.equals(n.getId())) {
                        throw new Exception("节点 " + n.getId() + " 不能引用自身");
                    }
                }
            }
        }

        // Check for cycles via topological sort
        Map<String, DagNode> dag = buildDAG(nodes);
        topologicalLevels(dag);

        // Check at least one sink exists
        boolean hasSink = nodes.stream().anyMatch(n -> n.getNextNodes() == null || n.getNextNodes().isEmpty());
        if (!hasSink) {
            throw new Exception("工作流图必须至少有一个终点节点（无下游节点）");
        }
    }

    /** Execute nodes in linear mode (backward compatible). */
    private void executeLinear(BlockingQueue<EngineEvent> eventQueue,
                               List<WorkflowNode> nodes, Map<String, String> vars,
                               String classifierOutputVar, String uid, String userQuery) {
        log.info("engine_linear_nodes_start total_nodes={}", nodes.size());
        int stepCounter = 0;
        int total = nodes.size();

        for (int i = 0; i < nodes.size(); i++) {
            WorkflowNode node = nodes.get(i);

            // Condition routing
            if (node.getCondition() != null && !node.getCondition().isEmpty()) {
                String currentIntent = vars.get(classifierOutputVar);
                if (!node.getCondition().equals(currentIntent)) {
                    log.info("engine_skip_node_linear node={} agent_name={} condition={} current_intent={}",
                            node.getId(), node.getAgentName(), node.getCondition(), currentIntent);
                    continue;
                }
            }

            stepCounter++;
            EngineEvent evt = executeNodeInternal(node, stepCounter, total, vars, uid, userQuery, classifierOutputVar, null);
            if (evt != null) {
                eventQueue.offer(evt);
                if ("error".equals(evt.getType())) return;
            }
        }
    }

    /** Execute nodes in DAG mode: topological levels, parallel within level. */
    private void executeDAG(BlockingQueue<EngineEvent> eventQueue,
                            WorkflowDef workflow, List<WorkflowNode> nodes,
                            Map<String, String> vars, String classifierOutputVar,
                            String uid, String userQuery) {
        log.info("engine_dag_nodes_start total_nodes={}", nodes.size());

        Map<String, DagNode> dag;
        List<List<WorkflowNode>> levels;
        try {
            dag = buildDAG(nodes);
            levels = topologicalLevels(dag);
        } catch (Exception e) {
            eventQueue.offer(new EngineEvent("error", "构建执行图失败: " + e.getMessage()));
            return;
        }

        log.info("engine_dag_levels_computed levels={}", levels.size());

        // Per-level executor service for parallel execution
        ExecutorService executor = Executors.newCachedThreadPool();

        for (int li = 0; li < levels.size(); li++) {
            List<WorkflowNode> level = levels.get(li);
            log.info("engine_dag_level_start level={} nodes={} total_levels={}", li + 1, level.size(), levels.size());

            if (level.size() == 1) {
                // Single node: execute synchronously
                WorkflowNode node = level.get(0);
                if (node.getCondition() != null && !node.getCondition().isEmpty()) {
                    if (!node.getCondition().equals(vars.get(classifierOutputVar))) {
                        log.info("engine_skip_node_dag node={} agent_name={} condition={}",
                                node.getId(), node.getAgentName(), node.getCondition());
                        continue;
                    }
                }
                EngineEvent evt = executeNodeInternal(node, li + 1, levels.size(), vars, uid, userQuery,
                        classifierOutputVar, null);
                if (evt != null) {
                    eventQueue.offer(evt);
                    if ("error".equals(evt.getType())) {
                        executor.shutdown();
                        return;
                    }
                }
            } else {
                // Multiple nodes: execute in parallel
                boolean hadError = executeParallelLevel(eventQueue, level, li + 1, levels.size(),
                        vars, uid, userQuery, classifierOutputVar, executor);
                if (hadError) {
                    log.warn("engine_parallel_level_errors");
                }
            }
        }

        executor.shutdown();
    }

    /** Execute multiple nodes at the same level in parallel. */
    private boolean executeParallelLevel(BlockingQueue<EngineEvent> eventQueue,
                                         List<WorkflowNode> level, int step, int total,
                                         Map<String, String> vars, String uid, String userQuery,
                                         String classifierOutputVar,
                                         ExecutorService executor) {
        Object lock = new Object();
        boolean[] hasError = {false};
        List<Future<?>> futures = new ArrayList<>();

        for (WorkflowNode node : level) {
            futures.add(executor.submit(() -> {
                // Condition routing
                if (node.getCondition() != null && !node.getCondition().isEmpty()) {
                    String intent;
                    synchronized (lock) { intent = vars.get(classifierOutputVar); }
                    if (!node.getCondition().equals(intent)) {
                        log.info("engine_skip_node_dag_parallel node={} agent_name={} condition={}",
                                node.getId(), node.getAgentName(), node.getCondition());
                        String label = node.getAgentName() + " (已跳过)";
                        EngineEvent progEvt = new EngineEvent("progress", step, total, label, "");
                        progEvt.setNodeId(node.getId());
                        progEvt.setParallelGroup(node.getParallelGroup());
                        synchronized (lock) { eventQueue.offer(progEvt); }
                        return;
                    }
                }

                // Progress event
                String label = (node.getParallelGroup() != null && !node.getParallelGroup().isEmpty())
                        ? "[并行:" + node.getParallelGroup() + "] " + node.getAgentName()
                        : "[并行] " + node.getAgentName();
                EngineEvent progEvt = new EngineEvent("progress", step, total, label, "");
                progEvt.setNodeId(node.getId());
                progEvt.setParallelGroup(node.getParallelGroup());
                synchronized (lock) { eventQueue.offer(progEvt); }

                EngineEvent evt = executeNodeInternal(node, step, total, vars, uid, userQuery,
                        classifierOutputVar, lock);

                if (evt != null) {
                    synchronized (lock) {
                        if ("error".equals(evt.getType())) hasError[0] = true;
                        eventQueue.offer(evt);
                    }
                }
            }));
        }

        for (Future<?> f : futures) {
            try { f.get(); } catch (Exception ignored) {}
        }

        return hasError[0];
    }

    /** Execute a single node. lock may be null (sequential mode) or shared (parallel mode). */
    private EngineEvent executeNodeInternal(WorkflowNode node, int step, int total,
                                            Map<String, String> vars, String uid, String userQuery,
                                            String classifierOutputVar, Object lock) {
        // Load agent
        AgentDef agent = metaStore.getAgent(node.getAgentId());
        if (agent == null) {
            return new EngineEvent("error", "节点 " + node.getId() + " 引用的智能体 (ID: " + node.getAgentId() + ") 不存在");
        }

        log.info("engine_node_start node={} agent={} step={} total={}", node.getId(), agent.getName(), step, total);

        // Render input template
        String input;
        if (lock != null) { synchronized (lock) { input = TemplateResolver.resolve(node.getInputTemplate(), vars); } }
        else { input = TemplateResolver.resolve(node.getInputTemplate(), vars); }

        log.info("engine_node_input_ready node={} agent={} input_len={}", node.getId(), agent.getName(),
                input != null ? input.length() : 0);

        // KB retrieval
        if (lock != null) { synchronized (lock) { vars.put("sys.kb_context", retrieveKbContext(agent, userQuery, uid)); } }
        else { vars.put("sys.kb_context", retrieveKbContext(agent, userQuery, uid)); }

        // Build system prompt
        String systemPrompt;
        if (lock != null) { synchronized (lock) { systemPrompt = TemplateResolver.resolve(agent.getSystemPrompt(), vars); } }
        else { systemPrompt = TemplateResolver.resolve(agent.getSystemPrompt(), vars); }

        // LLM client
        LlmClient llmClient = getLlmClient(agent);
        log.info("engine_llm_call_start node={} agent={} model={} system_prompt_len={}",
                node.getId(), agent.getName(), llmClient.getModelName(),
                systemPrompt != null ? systemPrompt.length() : 0);

        // Determine if final (no outgoing edges)
        boolean noOutEdges = node.getNextNodes() == null || node.getNextNodes().isEmpty();
        boolean isFinal = node.isFinal() || noOutEdges;

        long llmStart = System.currentTimeMillis();

        if (isFinal) {
            // Final node: synchronous call (streaming conflicts with parallel execution)
            try {
                String fullOutput = llmClient.chat(systemPrompt, input);
                long llmElapsed = System.currentTimeMillis() - llmStart;

                if (fullOutput == null) fullOutput = "[错误] LLM 返回空结果";
                log.info("engine_node_done_sync_final node={} agent={} type=sync duration_ms={} output_len={}",
                        node.getId(), agent.getName(), llmElapsed, fullOutput.length());

                if (lock != null) { synchronized (lock) { vars.put(node.getOutputVar(), fullOutput); vars.put(node.getId(), fullOutput); } }
                else { vars.put(node.getOutputVar(), fullOutput); vars.put(node.getId(), fullOutput); }

                EngineEvent evt = new EngineEvent("chunk", step, total, agent.getName(), fullOutput);
                evt.setNodeId(node.getId());
                return evt;
            } catch (Exception e) {
                String errorOutput = "[错误] " + e.getMessage();
                if (lock != null) { synchronized (lock) { vars.put(node.getOutputVar(), errorOutput); vars.put(node.getId(), errorOutput); } }
                else { vars.put(node.getOutputVar(), errorOutput); vars.put(node.getId(), errorOutput); }
                EngineEvent evt = new EngineEvent("chunk", step, total, agent.getName(), errorOutput);
                evt.setNodeId(node.getId());
                return evt;
            }
        }

        // Non-final node: synchronous call
        try {
            String fullOutput = llmClient.chat(systemPrompt, input);
            long llmElapsed = System.currentTimeMillis() - llmStart;

            if (fullOutput == null) fullOutput = "[错误] LLM 返回空结果";
            log.info("engine_node_done_sync_non_final node={} agent={} type=sync duration_ms={} output_len={}",
                    node.getId(), agent.getName(), llmElapsed, fullOutput.length());

            if (lock != null) { synchronized (lock) { vars.put(node.getOutputVar(), fullOutput); vars.put(node.getId(), fullOutput); } }
            else { vars.put(node.getOutputVar(), fullOutput); vars.put(node.getId(), fullOutput); }
        } catch (Exception e) {
            log.error("engine_node_error node={} agent={} error={}", node.getId(), agent.getName(), e.getMessage());
            String errorOutput = "[错误] " + e.getMessage();
            if (lock != null) { synchronized (lock) { vars.put(node.getOutputVar(), errorOutput); vars.put(node.getId(), errorOutput); } }
            else { vars.put(node.getOutputVar(), errorOutput); vars.put(node.getId(), errorOutput); }
        }

        return null;
    }

    // ============================================================
    // Internal methods
    // ============================================================

    private LlmClient getLlmClient(AgentDef agent) {
        String modelName = (agent.getModelName() != null && !agent.getModelName().isEmpty())
                ? agent.getModelName() : null;
        LlmClient client = clientFactory.createLlmClient(modelName);

        // Agent-specific overrides
        if (agent.getTemperature() != null || agent.getTopP() != null || agent.getMaxTokens() != null) {
            double temp = agent.getTemperature() != null ? agent.getTemperature() : 0.7;
            double topP = agent.getTopP() != null ? agent.getTopP() : 0.9;
            int maxTok = agent.getMaxTokens() != null ? agent.getMaxTokens() : 2048;
            client.setParams(temp, topP, maxTok);
        }
        return client;
    }

    private String retrieveKbContext(AgentDef agent, String userQuery, String uid) {
        if (agent.getVdbIds() == null || agent.getVdbIds().isEmpty() || "[]".equals(agent.getVdbIds())) {
            return "";
        }

        try {
            List<Long> vdbIds = MAPPER.readValue(agent.getVdbIds(), new TypeReference<List<Long>>() {});
            if (vdbIds.isEmpty()) return "";

            log.info("engine_kb_search_start node={} agent={} vdb_ids={}", "workflow", agent.getName(), vdbIds);

            long kbStart = System.currentTimeMillis();
            StringBuilder sb = new StringBuilder();
            for (long vdbId : vdbIds) {
                String ctx = kbMgr.searchInKB(userQuery, vdbId, uid,
                        cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
                if (ctx != null && !ctx.isEmpty()) {
                    sb.append(ctx).append("\n");
                }
            }
            long kbElapsed = System.currentTimeMillis() - kbStart;

            log.info("engine_kb_search_done node={} agent={} kb_context_len={} duration_ms={}",
                    "workflow", agent.getName(), sb.length(), kbElapsed);

            return sb.toString();
        } catch (Exception e) {
            log.warn("engine_kb_retrieval_failed agent={}", agent.getName(), e);
            return "";
        }
    }

    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        return s.length() <= maxLen ? s : s.substring(0, maxLen);
    }
}
