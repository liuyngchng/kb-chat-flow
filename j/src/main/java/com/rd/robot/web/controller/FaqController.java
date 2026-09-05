package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.client.ClientFactory;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;

/**
 * FAQ management controller.
 */
public class FaqController {

    private static final Logger log = LoggerFactory.getLogger(FaqController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private static final String FAQ_TEMPLATE_CONTENT = """
            # FAQ 模板文件
            # 格式说明：Q: 开头为问题（可多个 Q 对应同一个答案），A: 开头为答案
            # 空行分隔不同的 FAQ 条目
            #
            # 用法：修改此文件后，在管理后台 → FAQ 管理 → 上传 FAQ 文件

            Q: 如何重置密码？
            Q: 忘记密码怎么办？
            Q: 密码忘了
            A: 您好，您可以在登录页面点击"忘记密码"，按照提示输入注册邮箱，系统会发送重置链接到您的邮箱。链接有效期为 24 小时，请及时操作。

            Q: 支持哪些支付方式？
            Q: 可以用微信支付吗？
            Q: 能用支付宝吗？
            Q: 是否支持银行卡付款？
            A: 目前支持微信支付、支付宝、银联银行卡（储蓄卡及信用卡）以及 Apple Pay。

            Q: 如何联系人工客服？
            Q: 转人工
            Q: 找客服
            A: 您好，人工客服工作时间为周一至周五 9:00-18:00。
            """;

    private final MetaStore metaStore;
    private final ClientFactory clientFactory;

    public FaqController(MetaStore metaStore, ClientFactory clientFactory) {
        this.metaStore = metaStore;
        this.clientFactory = clientFactory;
    }

    /**
     * GET /api/faq — list all FAQ entries
     */
    public void list(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            List<FaqEntry> entries = metaStore.getFaqEntries();
            if (entries == null) entries = List.of();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", entries)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取 FAQ 列表失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/faq/template — download FAQ template
     */
    public void template(ChannelHandlerContext ctx, FullHttpRequest req) {
        ctx.writeAndFlush(new io.netty.handler.codec.http.DefaultFullHttpResponse(
                io.netty.handler.codec.http.HttpVersion.HTTP_1_1,
                io.netty.handler.codec.http.HttpResponseStatus.OK,
                io.netty.buffer.Unpooled.copiedBuffer(FAQ_TEMPLATE_CONTENT, CharsetUtil.UTF_8)
        )).addListener(io.netty.channel.ChannelFutureListener.CLOSE);
    }

    /**
     * POST /api/faq/match — standalone FAQ matching
     */
    public void match(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            @SuppressWarnings("unchecked")
            Map<String, Object> params = MAPPER.readValue(body, Map.class);
            String query = (String) params.get("query");

            if (query == null || query.isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"query 不能为空\"}");
                return;
            }

            double threshold = 0.85;
            try {
                String cfgVal = metaStore.getConfig("faq.match_threshold");
                if (cfgVal != null && !cfgVal.isEmpty()) {
                    threshold = Double.parseDouble(cfgVal);
                }
            } catch (Exception ignored) {}

            FaqMatchResult result = matchFaq(query, threshold);
            Map<String, Object> resp = new LinkedHashMap<>();
            if (result != null) {
                resp.put("answer", result.answer());
                resp.put("score", result.score());
                resp.put("matched", true);
            } else {
                resp.put("answer", "");
                resp.put("score", 0.0);
                resp.put("matched", false);
            }
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"FAQ 匹配失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/faq — create a single FAQ entry
     */
    public void create(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateFaqRequest createReq = MAPPER.readValue(body, CreateFaqRequest.class);

            if (createReq.getQuestions() == null || createReq.getQuestions().isEmpty()
                    || createReq.getAnswer() == null || createReq.getAnswer().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"问题和答案不能为空\"}");
                return;
            }

            createFaqEntry(createReq.getQuestions(), createReq.getAnswer(), "");
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"创建 FAQ 条目失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/faq/upload — upload FAQ file (multipart)
     */
    public void upload(ChannelHandlerContext ctx, FullHttpRequest req) {
        String contentType = req.headers().get(HttpHeaderNames.CONTENT_TYPE);
        if (contentType == null || !contentType.contains("multipart/form-data")) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"请使用 multipart/form-data 上传文件\"}");
            return;
        }

        try {
            VdbController.MultipartForm form = parseMultipart(req, contentType);
            VdbController.MultipartForm.FilePart file = form.getFile("file");
            if (file == null) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"请选择文件\"}");
                return;
            }

            String name = file.filename.toLowerCase();
            if (!name.endsWith(".txt") && !name.endsWith(".md")) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"仅支持 txt/md 格式的 FAQ 文件\"}");
                return;
            }

            String content = new String(file.content, CharsetUtil.UTF_8);
            List<FaqPair> entries = parseFaqContent(content);

            if (entries.isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"FAQ 文件内容为空或格式不正确\"}");
                return;
            }

            int created = 0;
            for (FaqPair pair : entries) {
                try {
                    createFaqEntry(pair.questions, pair.answer, file.filename);
                    created++;
                } catch (Exception e) {
                    log.error("faq_upload_create_entry_failed", e);
                }
            }

            Map<String, Object> resp = Map.of("status", "ok", "created", created, "total", entries.size());
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            log.error("faq_upload_file_failed", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"上传 FAQ 失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * PUT /api/faq/:id — update FAQ entry
     */
    public void update(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            String body = req.content().toString(CharsetUtil.UTF_8);
            UpdateFaqRequest updateReq = MAPPER.readValue(body, UpdateFaqRequest.class);

            if (updateReq.getQuestions() == null || updateReq.getQuestions().isEmpty()
                    || updateReq.getAnswer() == null || updateReq.getAnswer().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"问题和答案不能为空\"}");
                return;
            }

            // Update answer
            metaStore.updateFaqEntry(id, updateReq.getAnswer());

            // Delete old questions
            metaStore.deleteFaqQuestionsByEntryId(id);

            // Re-vectorize new questions
            for (String q : updateReq.getQuestions()) {
                q = q.trim();
                if (q.isEmpty()) continue;
                try {
                    double[] emb = clientFactory.getEmbeddingClient().embedSingle(q);
                    String embJson = MAPPER.writeValueAsString(Arrays.stream(emb).boxed().toList());
                    metaStore.createFaqQuestion(id, q, embJson);
                } catch (Exception e) {
                    log.warn("faq_create_question_embed_failed question={}", truncate(q, 30), e);
                }
            }

            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"更新 FAQ 失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * DELETE /api/faq/:id — delete FAQ entry
     */
    public void delete(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            metaStore.deleteFaqEntry(id);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"删除 FAQ 失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * DELETE /api/faq — clear all FAQ
     */
    public void clearAll(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            metaStore.clearAllFaq();
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"清空 FAQ 失败: " + e.getMessage() + "\"}");
        }
    }

    // ============================================================
    // FAQ matching (called by ChatController)
    // ============================================================

    /**
     * Match user query against FAQ entries using cosine similarity.
     * Returns (answer, score) if matched, or null if no match above threshold.
     */
    public FaqMatchResult matchFaq(String query, double threshold) {
        try {
            var questions = metaStore.getAllFaqQuestionsWithEmbedding();
            if (questions.isEmpty()) return null;

            double[] queryVec = clientFactory.getEmbeddingClient().embedSingle(query);

            double bestScore = 0;
            long bestEntryId = 0;

            for (var q : questions) {
                if (q.getEmbedding() == null || q.getEmbedding().length == 0) continue;
                double score = cosineSimilarity(queryVec, q.getEmbedding());
                if (score > bestScore) {
                    bestScore = score;
                    bestEntryId = q.getEntryId();
                }
            }

            if (bestScore < threshold) return null;

            var entries = metaStore.getFaqEntries();
            for (var e : entries) {
                if (e.getId() == bestEntryId) {
                    return new FaqMatchResult(e.getAnswer(), bestScore);
                }
            }
        } catch (Exception e) {
            log.warn("faq_match_failed", e);
        }
        return null;
    }

    /**
     * Returns the number of FAQ entries.
     */
    public int getFaqCount() {
        try {
            return metaStore.getFaqEntries().size();
        } catch (Exception e) {
            return 0;
        }
    }

    /**
     * FAQ match result.
     */
    public record FaqMatchResult(String answer, double score) {}

    // ============================================================
    // Internal helpers
    // ============================================================

    private void createFaqEntry(List<String> questions, String answer, String sourceFile) {
        long entryId = metaStore.createFaqEntry(answer, sourceFile);
        for (String q : questions) {
            q = q.trim();
            if (q.isEmpty()) continue;
            try {
                double[] emb = clientFactory.getEmbeddingClient().embedSingle(q);
                String embJson = MAPPER.writeValueAsString(Arrays.stream(emb).boxed().toList());
                metaStore.createFaqQuestion(entryId, q, embJson);
            } catch (Exception e) {
                log.warn("faq_update_question_embed_failed question={}", truncate(q, 30), e);
            }
        }
    }

    private static class FaqPair {
        List<String> questions = new ArrayList<>();
        String answer;
    }

    private static List<FaqPair> parseFaqContent(String content) {
        String[] lines = content.replace("\r\n", "\n").split("\n");
        List<FaqPair> pairs = new ArrayList<>();
        FaqPair current = null;

        for (String line : lines) {
            String trimmed = line.trim();
            if (trimmed.isEmpty()) {
                if (current != null && !current.questions.isEmpty() && current.answer != null) {
                    pairs.add(current);
                    current = null;
                }
                continue;
            }

            String upperLine = trimmed.toUpperCase();
            if (upperLine.startsWith("Q:") || upperLine.startsWith("Q：")) {
                String q = trimmed.substring(2).trim();
                if (q.isEmpty()) continue;
                if (current == null) current = new FaqPair();
                if (current.answer != null) {
                    pairs.add(current);
                    current = new FaqPair();
                }
                current.questions.add(q);
            } else if (upperLine.startsWith("A:") || upperLine.startsWith("A：")) {
                String a = trimmed.substring(2).trim();
                if (current == null) current = new FaqPair();
                current.answer = a;
            }
        }

        if (current != null && !current.questions.isEmpty() && current.answer != null) {
            pairs.add(current);
        }

        return pairs;
    }

    private static long parseIdFromPath(String uri) {
        String path = HttpServer.sanitizePath(uri);
        String rest = extractIdFromPath(path, "/api/faq/", "/api/v1/faq/", "/open_api/faq/");
        if (rest != null) {
            try { return Long.parseLong(rest); } catch (NumberFormatException ignored) {}
        }
        return 0;
    }

    private static String extractIdFromPath(String path, String... prefixes) {
        for (String prefix : prefixes) {
            if (path.startsWith(prefix)) {
                String rest = path.substring(prefix.length());
                int slashIdx = rest.indexOf('/');
                if (slashIdx >= 0) rest = rest.substring(0, slashIdx);
                return rest;
            }
        }
        return null;
    }

    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        return s.length() <= maxLen ? s : s.substring(0, maxLen);
    }

    private static double cosineSimilarity(double[] a, double[] b) {
        if (a.length != b.length || a.length == 0) return 0;
        double dotProd = 0, normA = 0, normB = 0;
        for (int i = 0; i < a.length; i++) {
            dotProd += a[i] * b[i];
            normA += a[i] * a[i];
            normB += b[i] * b[i];
        }
        if (normA == 0 || normB == 0) return 0;
        return dotProd / (Math.sqrt(normA) * Math.sqrt(normB));
    }

    // Reuse multipart parser from VdbController
    private VdbController.MultipartForm parseMultipart(FullHttpRequest req, String contentType) {
        // Create a temporary VdbController to use its parser
        // Since the parser is static, we just inline it here
        int boundaryIdx = contentType.indexOf("boundary=");
        if (boundaryIdx < 0) throw new RuntimeException("缺少 boundary");
        String boundary = "--" + contentType.substring(boundaryIdx + 9).trim();

        byte[] body = new byte[req.content().readableBytes()];
        req.content().readBytes(body);

        VdbController.MultipartForm form = new VdbController.MultipartForm();
        int pos = 0;
        byte[] boundaryBytes = boundary.getBytes(CharsetUtil.UTF_8);

        while (pos < body.length) {
            int nextBoundary = indexOf(body, boundaryBytes, pos);
            if (nextBoundary < 0) break;

            int headerStart = nextBoundary + boundaryBytes.length;
            if (headerStart >= body.length) break;

            if (body[headerStart] == '\r' && headerStart + 1 < body.length && body[headerStart + 1] == '\n') {
                headerStart += 2;
            }

            int headerEnd = indexOf(body, new byte[]{'\r', '\n', '\r', '\n'}, headerStart);
            if (headerEnd < 0) break;

            String headers = new String(body, headerStart, headerEnd - headerStart, CharsetUtil.UTF_8);

            int contentStart = headerEnd + 4;
            int contentEnd = indexOf(body, boundaryBytes, contentStart);
            if (contentEnd < 0) break;

            int contentActualEnd = contentEnd;
            if (contentActualEnd > contentStart && body[contentActualEnd - 1] == '\n') contentActualEnd--;
            if (contentActualEnd > contentStart && body[contentActualEnd - 1] == '\r') contentActualEnd--;

            byte[] content = Arrays.copyOfRange(body, contentStart, contentActualEnd);

            String lowerHeaders = headers.toLowerCase();
            if (lowerHeaders.contains("filename=")) {
                VdbController.MultipartForm.FilePart file = new VdbController.MultipartForm.FilePart();
                file.fieldName = extractHeaderValue(headers, "name=\"", "\"");
                file.filename = extractHeaderValue(headers, "filename=\"", "\"");
                file.contentType = extractHeaderValue(headers, "Content-Type: ", "\r\n");
                file.content = content;
                form.files.put(file.fieldName, file);
            } else {
                String fieldName = extractHeaderValue(headers, "name=\"", "\"");
                String value = new String(content, CharsetUtil.UTF_8);
                form.fields.put(fieldName, value);
            }

            pos = contentEnd;
        }

        return form;
    }

    private int indexOf(byte[] data, byte[] pattern, int from) {
        for (int i = from; i <= data.length - pattern.length; i++) {
            boolean match = true;
            for (int j = 0; j < pattern.length; j++) {
                if (data[i + j] != pattern[j]) { match = false; break; }
            }
            if (match) return i;
        }
        return -1;
    }

    private String extractHeaderValue(String headers, String prefix, String suffix) {
        int idx = headers.indexOf(prefix);
        if (idx < 0) return "";
        idx += prefix.length();
        int endIdx = headers.indexOf(suffix, idx);
        if (endIdx < 0) return headers.substring(idx);
        return headers.substring(idx, endIdx);
    }
}