package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.client.ClientFactory;
import com.rd.robot.client.LlmClient;
import com.rd.robot.engine.CsmEngine;
import com.rd.robot.engine.IntentClassifier;
import com.rd.robot.engine.TemplateResolver;
import com.rd.robot.engine.WorkflowEngine;
import com.rd.robot.knowledge.KnowledgeBaseManager;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.session.SessionManager;
import com.rd.robot.web.server.HttpServer;
import io.netty.buffer.Unpooled;
import io.netty.channel.ChannelFutureListener;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.*;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.LocalDate;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * Chat controller — SSE streaming chat and workflow execution.
 */
public class ChatController {

    private static final Logger log = LoggerFactory.getLogger(ChatController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final Config cfg;
    private final KnowledgeBaseManager kbMgr;
    private final SessionManager sessionMgr;
    private final ClientFactory clientFactory;
    private final MetaStore metaStore;
    private final WorkflowEngine workflowEngine;
    private final CsmEngine csmEngine;
    private final FaqController faqController;

    public ChatController(Config cfg, KnowledgeBaseManager kbMgr, SessionManager sessionMgr,
                          MetaStore metaStore, ClientFactory clientFactory, FaqController faqController) {
        this.cfg = cfg;
        this.kbMgr = kbMgr;
        this.sessionMgr = sessionMgr;
        this.metaStore = metaStore;
        this.clientFactory = clientFactory;
        this.faqController = faqController;
        this.workflowEngine = new WorkflowEngine(cfg, kbMgr, metaStore, clientFactory);
        this.csmEngine = new CsmEngine(cfg, kbMgr, clientFactory, metaStore);
    }

    public CsmEngine getCsmEngine() { return csmEngine; }

    private LlmClient getLlmClient() {
        return clientFactory.getLlmClient();
    }

    /**
     * POST /api/chat/sync — non-streaming chat, returns JSON.
     */
    public void chatSync(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            ChatRequest req = MAPPER.readValue(body, ChatRequest.class);

            if (req.getMsg() == null || req.getMsg().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"msg 不能为空\"}");
                return;
            }

            String uid = resolveUID(request, req);

            switch (cfg.getSys().getWorkMode()) {
                case 1: chatSyncWithCSM(ctx, req, uid); return;
                case 2: chatSyncWithDynamic(ctx, req, uid); return;
                default: chatSyncWithKB(ctx, req, uid);
            }
        } catch (Exception e) {
            log.error("chatSync error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"聊天请求失败: " + e.getMessage() + "\"}");
        }
    }

    private void chatSyncWithKB(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        try {
            List<ChatMessage> history = sessionMgr.getHistory(uid);
            String historyStr = SessionManager.formatHistory(history);

            double faqThreshold = cfg.getFaq().getMatchThreshold();
            if (faqController.getFaqCount() > 0) {
                FaqController.FaqMatchResult faqResult = faqController.matchFaq(req.getMsg(), faqThreshold);
                if (faqResult != null) {
                    sessionMgr.addMessage(uid, "user", req.getMsg());
                    sessionMgr.addMessage(uid, "assistant", faqResult.answer());
                    Map<String, Object> resp = new java.util.LinkedHashMap<>();
                    resp.put("answer", faqResult.answer());
                    resp.put("source", "faq");
                    resp.put("score", faqResult.score());
                    HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
                    return;
                }
            }

            LocalDate today = LocalDate.now();
            String curDate = today.format(DateTimeFormatter.ofPattern("yyyy-MM-dd"));
            String curWeek = getWeekdayCN(today.getDayOfWeek().getValue());
            String contextStr = kbMgr.searchAllKBs(req.getMsg(), uid,
                    cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
            String promptTemplate = getPromptTemplate();
            String systemPrompt = buildPrompt(promptTemplate, contextStr, historyStr, req.getMsg(), curDate, curWeek);

            sessionMgr.addMessage(uid, "user", req.getMsg());
            String answer = getLlmClient().chat(systemPrompt, "");
            sessionMgr.addMessage(uid, "assistant", answer);

            Map<String, Object> resp = new java.util.LinkedHashMap<>();
            resp.put("answer", answer);
            resp.put("source", "kb");
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            log.error("chat sync kb error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    private void chatSyncWithCSM(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        try {
            List<ChatMessage> history = sessionMgr.getHistory(uid);
            List<TemplateResolver.ChatMsg> historyMsgs = history.stream()
                    .map(h -> new TemplateResolver.ChatMsg(h.getRole(), h.getContent()))
                    .collect(Collectors.toList());

            sessionMgr.addMessage(uid, "user", req.getMsg());
            var eventQueue = csmEngine.executeStreamCSM(0, req.getMsg(), uid, historyMsgs);
            StringBuilder sb = new StringBuilder();
            while (true) {
                EngineEvent evt = eventQueue.take();
                if ("done".equals(evt.getType())) break;
                if ("chunk".equals(evt.getType())) sb.append(evt.getContent());
                if ("error".equals(evt.getType())) throw new RuntimeException(evt.getContent());
            }
            String answer = sb.toString();
            sessionMgr.addMessage(uid, "assistant", answer);

            Map<String, Object> resp = new java.util.LinkedHashMap<>();
            resp.put("answer", answer);
            resp.put("source", "csm");
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            log.error("chat sync csm error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    private void chatSyncWithDynamic(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        try {
            List<ChatMessage> history = sessionMgr.getHistory(uid);
            List<TemplateResolver.ChatMsg> historyMsgs = history.stream()
                    .map(h -> new TemplateResolver.ChatMsg(h.getRole(), h.getContent()))
                    .collect(Collectors.toList());

            sessionMgr.addMessage(uid, "user", req.getMsg());
            long workflowId = cfg.getSys().getDefaultWorkflowId();
            String answer = workflowEngine.execute(workflowId, req.getMsg(), uid, historyMsgs);
            sessionMgr.addMessage(uid, "assistant", answer);

            Map<String, Object> resp = new java.util.LinkedHashMap<>();
            resp.put("answer", answer);
            resp.put("source", "dynamic");
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            log.error("chat sync dynamic error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    private String resolveUID(FullHttpRequest request, ChatRequest req) {
        // open_api (Bearer): 强制使用 token 中的用户名
        String path = HttpServer.sanitizePath(request.uri());
        if (path.startsWith("/open_api/")) {
            return getUid(request);
        }
        if (cfg.getSys().isApiAuth()) {
            // API auth enabled: force token UID
            return getUid(request);
        }
        // API auth disabled: prefer request UID, fallback to token
        if (req.getUid() != null && !req.getUid().isEmpty()) {
            return req.getUid();
        }
        return getUid(request);
    }

    /**
     * POST /api/chat — SSE streaming chat
     */
    public void chat(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            ChatRequest req = MAPPER.readValue(body, ChatRequest.class);

            if (req.getMsg() == null || req.getMsg().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"msg 不能为空\"}");
                return;
            }

            String uid = resolveUID(request, req);

            // Set SSE headers
            FullHttpResponse initResponse = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, HttpResponseStatus.OK, Unpooled.EMPTY_BUFFER);
            initResponse.headers()
                    .set(HttpHeaderNames.CONTENT_TYPE, "text/event-stream; charset=utf-8")
                    .set(HttpHeaderNames.CACHE_CONTROL, "no-cache")
                    .set(HttpHeaderNames.CONNECTION, "keep-alive")
                    .set("X-Accel-Buffering", "no");
            ctx.writeAndFlush(initResponse);

            // Route by work mode
            switch (cfg.getSys().getWorkMode()) {
                case 1: // CSM
                    chatWithCSMWorkflow(ctx, req, uid);
                    return;
                case 2: // Dynamic
                    chatWithDynamicWorkflow(ctx, req, uid);
                    return;
                default: // KB
                    chatWithKB(ctx, req, uid);
                    return;
            }

        } catch (Exception e) {
            log.error("chat error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"聊天请求失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * KB chat mode — FAQ matching → KB search → LLM conversation.
     */
    private void chatWithKB(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        // Get history
        List<ChatMessage> history = sessionMgr.getHistory(uid);
        String historyStr = SessionManager.formatHistory(history);

        // Try FAQ matching first
        double faqThreshold = cfg.getFaq().getMatchThreshold();
        if (faqController.getFaqCount() > 0) {
            try {
                FaqController.FaqMatchResult faqResult = faqController.matchFaq(req.getMsg(), faqThreshold);
                if (faqResult != null) {
                    log.info("faq-matched uid={} query={} score={}",
                            uid, truncate(req.getMsg(), 50), faqResult.score());
                    sessionMgr.addMessage(uid, "user", req.getMsg());
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: \n\n", CharsetUtil.UTF_8)));
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: " + faqResult.answer() + "\n\n", CharsetUtil.UTF_8)));
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: [DONE]\n\n", CharsetUtil.UTF_8)));
                    ctx.writeAndFlush(LastHttpContent.EMPTY_LAST_CONTENT)
                            .addListener(ChannelFutureListener.CLOSE);
                    sessionMgr.addMessage(uid, "assistant", faqResult.answer());
                    return;
                }
            } catch (Exception e) {
                log.warn("FAQ 匹配失败", e);
            }
        }

        // Get KB context
        LocalDate today = LocalDate.now();
        String curDate = today.format(DateTimeFormatter.ofPattern("yyyy-MM-dd"));
        String curWeek = getWeekdayCN(today.getDayOfWeek().getValue());

        String contextStr = kbMgr.searchAllKBs(req.getMsg(), uid,
                cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());

        // Build prompt
        String promptTemplate = getPromptTemplate();
        String systemPrompt = buildPrompt(promptTemplate, contextStr, historyStr, req.getMsg(), curDate, curWeek);

        log.info("chat uid={} query={} contextLen={}",
                uid, truncate(req.getMsg(), 50), contextStr.length());

        // Save user message
        sessionMgr.addMessage(uid, "user", req.getMsg());

        // Send initial event
        ctx.writeAndFlush(new DefaultHttpContent(
                Unpooled.copiedBuffer("data: \n\n", CharsetUtil.UTF_8)));

        // Stream LLM response
        StringBuilder fullResponse = new StringBuilder();

        getLlmClient().chatStream(systemPrompt, "",
                chunk -> {
                    fullResponse.append(chunk);
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: " + chunk + "\n\n", CharsetUtil.UTF_8)));
                },
                error -> {
                    log.error("LLM 错误 error={}", error);
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: [错误] " + error + "\n\n", CharsetUtil.UTF_8)));
                },
                () -> {
                    ctx.writeAndFlush(new DefaultHttpContent(
                            Unpooled.copiedBuffer("data: [DONE]\n\n", CharsetUtil.UTF_8)));

                    String responseText = fullResponse.toString();
                    if (!responseText.isEmpty()) {
                        sessionMgr.addMessage(uid, "assistant", responseText);
                    }

                    ctx.writeAndFlush(LastHttpContent.EMPTY_LAST_CONTENT)
                            .addListener(ChannelFutureListener.CLOSE);
                });
    }

    /**
     * Chat with CSM workflow engine (hardcoded customer service logic).
     */
    private void chatWithCSMWorkflow(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        List<ChatMessage> history = sessionMgr.getHistory(uid);
        List<TemplateResolver.ChatMsg> historyMsgs = history.stream()
                .map(h -> new TemplateResolver.ChatMsg(h.getRole(), h.getContent()))
                .collect(Collectors.toList());

        sessionMgr.addMessage(uid, "user", req.getMsg());

        log.info("workflow-chat-csm uid={} query={}",
                uid, truncate(req.getMsg(), 50));

        ctx.writeAndFlush(new DefaultHttpContent(
                Unpooled.copiedBuffer("data: \n\n", CharsetUtil.UTF_8)));

        StringBuilder fullResponse = new StringBuilder();

        // 【CSM 硬编码模式】直接走 CsmEngine 写死的客服问答逻辑（分类→路由→检索→回答），
        // 不再从数据库加载工作流配置。若需恢复动态配置，放开下面这行、注释掉 executeStreamCSM：
        // var eventQueue = workflowEngine.executeStream(0, req.getMsg(), uid, historyMsgs);
        var eventQueue = csmEngine.executeStreamCSM(0, req.getMsg(), uid, historyMsgs);

        new Thread(() -> {
            try {
                while (true) {
                    EngineEvent evt = eventQueue.take();
                    switch (evt.getType()) {
                        case "progress":
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [步骤 " + evt.getStep() + "/" + evt.getTotal() + "] " + evt.getAgent() + "\n\n",
                                    CharsetUtil.UTF_8)));
                            break;
                        case "chunk":
                            fullResponse.append(evt.getContent());
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: " + evt.getContent() + "\n\n", CharsetUtil.UTF_8)));
                            break;
                        case "error":
                            log.error("workflow error error={}", evt.getContent());
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [错误] " + evt.getContent() + "\n\n", CharsetUtil.UTF_8)));
                            break;
                        case "done":
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [DONE]\n\n", CharsetUtil.UTF_8)));

                            String responseText = fullResponse.toString();
                            if (!responseText.isEmpty()) {
                                sessionMgr.addMessage(uid, "assistant", responseText);
                            }

                            ctx.writeAndFlush(LastHttpContent.EMPTY_LAST_CONTENT)
                                    .addListener(ChannelFutureListener.CLOSE);
                            return;
                    }
                }
            } catch (Exception e) {
                log.error("workflow event processing error", e);
            }
        }).start();
    }

    /**
     * Chat with dynamic workflow engine (loads workflow config from DB).
     */
    private void chatWithDynamicWorkflow(ChannelHandlerContext ctx, ChatRequest req, String uid) {
        List<ChatMessage> history = sessionMgr.getHistory(uid);
        List<TemplateResolver.ChatMsg> historyMsgs = history.stream()
                .map(h -> new TemplateResolver.ChatMsg(h.getRole(), h.getContent()))
                .collect(Collectors.toList());

        sessionMgr.addMessage(uid, "user", req.getMsg());

        long workflowId = cfg.getSys().getDefaultWorkflowId();
        log.info("workflow-chat-dynamic uid={} workflow={} query={}",
                uid, workflowId, truncate(req.getMsg(), 50));

        ctx.writeAndFlush(new DefaultHttpContent(
                Unpooled.copiedBuffer("data: \n\n", CharsetUtil.UTF_8)));

        StringBuilder fullResponse = new StringBuilder();

        var eventQueue = workflowEngine.executeStream(workflowId, req.getMsg(), uid, historyMsgs);

        new Thread(() -> {
            try {
                while (true) {
                    EngineEvent evt = eventQueue.take();
                    switch (evt.getType()) {
                        case "progress":
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [步骤 " + evt.getStep() + "/" + evt.getTotal() + "] " + evt.getAgent() + "\n\n",
                                    CharsetUtil.UTF_8)));
                            break;
                        case "chunk":
                            fullResponse.append(evt.getContent());
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: " + evt.getContent() + "\n\n", CharsetUtil.UTF_8)));
                            break;
                        case "error":
                            log.error("workflow error error={}", evt.getContent());
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [错误] " + evt.getContent() + "\n\n", CharsetUtil.UTF_8)));
                            break;
                        case "done":
                            ctx.writeAndFlush(new DefaultHttpContent(Unpooled.copiedBuffer(
                                    "data: [DONE]\n\n", CharsetUtil.UTF_8)));

                            String responseText = fullResponse.toString();
                            if (!responseText.isEmpty()) {
                                sessionMgr.addMessage(uid, "assistant", responseText);
                            }

                            ctx.writeAndFlush(LastHttpContent.EMPTY_LAST_CONTENT)
                                    .addListener(ChannelFutureListener.CLOSE);
                            return;
                    }
                }
            } catch (Exception e) {
                log.error("workflow event processing error", e);
            }
        }).start();
    }

    /**
     * POST /api/chat/clear — clear session
     */
    /**
     * GET /api/chat/history — get current user's chat history.
     */
    public void history(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String uid = getUid(request);
            List<ChatMessage> history = sessionMgr.getHistory(uid);
            HttpServer.sendJson(ctx, 200,
                    "{\"data\":" + MAPPER.writeValueAsString(history) + "}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取历史消息失败: " + e.getMessage() + "\"}");
        }
    }

    public void clear(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String uid = getUid(request);
            sessionMgr.clear(uid);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"清空会话失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/classifier/test — intent classification testing
     */
    public void testClassifier(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            @SuppressWarnings("unchecked")
            Map<String, Object> req = MAPPER.readValue(body, Map.class);

            String text = (String) req.get("text");
            if (text == null || text.isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"text 不能为空\"}");
                return;
            }

            long workflowId = req.get("workflow_id") instanceof Number
                    ? ((Number) req.get("workflow_id")).longValue() : 0;
            if (workflowId <= 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"workflow_id 不能为空\"}");
                return;
            }

            // Load workflow
            WorkflowDef workflow = metaStore.getWorkflow(workflowId);
            if (workflow == null) {
                HttpServer.sendJson(ctx, 404, "{\"error\":\"工作流不存在\"}");
                return;
            }

            if (workflow.getClassifier() == null || workflow.getClassifier().getCategories() == null
                    || workflow.getClassifier().getCategories().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"该工作流没有配置意图分类器\"}");
                return;
            }

            // Train fastText model
            try {
                workflowEngine.ftPredictor().train(
                        workflow.getClassifier().getCategories(),
                        workflow.getClassifier().getPrompt());
            } catch (Exception e) {
                log.warn("fastText train failed for test: {}", e.getMessage());
            }

            // Execute classification with details
            IntentClassifier.ClassificationDetail detail = IntentClassifier.classifyWithDetails(
                    workflow.getClassifier(), text,
                    getLlmClient(),
                    workflowEngine.embClient(),
                    workflowEngine.ftPredictor());

            // Build tier result maps for JSON serialization
            List<Map<String, Object>> tiers = new ArrayList<>();
            long totalMs = 0;
            for (IntentClassifier.TierResult t : detail.tiers) {
                Map<String, Object> tm = new java.util.LinkedHashMap<>();
                tm.put("name", t.name);
                tm.put("matched", t.matched);
                if (t.skipped) tm.put("skipped", true);
                if (t.result != null) tm.put("result", t.result);
                if (t.score > 0) tm.put("score", t.score);
                tm.put("elapsed_ms", t.elapsedMs);
                tiers.add(tm);
                totalMs += t.elapsedMs;
            }

            Map<String, Object> result = new java.util.LinkedHashMap<>();
            result.put("tiers", tiers);
            result.put("final", detail.finalResult);
            result.put("total_ms", totalMs);

            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(result));
        } catch (Exception e) {
            log.error("classifier test error", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"分类测试失败: " + e.getMessage() + "\"}");
        }
    }

    // ============================================================
    // Helpers
    // ============================================================

    private String buildPrompt(String template, String context, String history, String question,
                               String curDate, String curWeek) {
        return template
                .replace("{context}", context != null ? context : "")
                .replace("{history}", history != null ? history : "")
                .replace("{question}", question != null ? question : "")
                .replace("{cur_date}", curDate)
                .replace("{cur_week}", curWeek);
    }

    private String getWeekdayCN(int dayOfWeek) {
        String[] days = {"一", "二", "三", "四", "五", "六", "日"};
        return days[dayOfWeek - 1];
    }

    private String getPromptTemplate() {
        if (metaStore != null) {
            String prompt = metaStore.getPrompt("chat_msg");
            if (prompt != null && !prompt.isEmpty()) return prompt;
        }
        return cfg.getPrompts() != null && cfg.getPrompts().getChatMsg() != null
                ? cfg.getPrompts().getChatMsg()
                : "你是专业的对话机器人，负责解答客户咨询。\n\n知识库内容：\n---\n{context}\n---\n\n历史对话：\n{history}\n\n用户问题：{question}\n\n请用亲切、专业的中文回答：";
    }

    private String getUid(FullHttpRequest request) {
        // Try Authorization header first
        User user = AuthController.parseUserFromRequest(request);
        if (user != null) return user.getUserName();
        // Fallback to query param
        String uid = HttpServer.getQueryParam(request, "uid");
        return uid != null ? uid : "default";
    }

    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        return s.length() <= maxLen ? s : s.substring(0, maxLen);
    }
}