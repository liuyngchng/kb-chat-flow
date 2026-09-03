package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.engine.CsmEngine;
import com.rd.robot.knowledge.KnowledgeBaseManager;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.util.*;

/**
 * Knowledge base (VDB) management controller.
 */
public class VdbController {

    private static final Logger log = LoggerFactory.getLogger(VdbController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final Set<String> ALLOWED_EXTS = Set.of(".txt", ".md", ".pdf", ".docx", ".xlsx");

    private final Config cfg;
    private final KnowledgeBaseManager kbMgr;
    private final MetaStore store;
    private final CsmEngine csmEngine;

    public VdbController(Config cfg, KnowledgeBaseManager kbMgr, MetaStore store, CsmEngine csmEngine) {
        this.cfg = cfg;
        this.kbMgr = kbMgr;
        this.store = store;
        this.csmEngine = csmEngine;
    }

    public void myList(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        try {
            List<VdbInfo> list = kbMgr.getUserKBs(uid);
            if (list == null) list = List.of();
            sendJson(ctx, 200, Map.of("data", list));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void pubList(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        try {
            List<VdbInfo> list = kbMgr.getPublicKBs(uid);
            if (list == null) list = List.of();
            sendJson(ctx, 200, Map.of("data", list));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void fileList(ChannelHandlerContext ctx, FullHttpRequest req) {
        long vdbId = getLongParam(req, "vdb_id");
        try {
            List<VdbFileInfo> files = kbMgr.getFiles(vdbId);
            if (files == null) files = List.of();
            sendJson(ctx, 200, Map.of("data", files));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void setDefault(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        long id = getLongParam(req, "id");
        try {
            kbMgr.setDefaultKB(id, uid);
            sendJson(ctx, 200, Map.of("status", "ok"));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void create(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        String name = HttpServer.getParam(req, "name");
        String isPublicStr = HttpServer.getParam(req, "is_public");
        boolean isPublic = "true".equals(isPublicStr) || "1".equals(isPublicStr);

        if (name == null || name.isEmpty()) {
            sendJson(ctx, 400, Map.of("error", "知识库名称不能为空"));
            return;
        }

        try {
            long id = kbMgr.createKB(name, uid, isPublic);
            sendJson(ctx, 200, Map.of("status", "ok", "id", id));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void delete(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        long id = getLongParam(req, "id");
        try {
            kbMgr.deleteKB(id, uid);
            sendJson(ctx, 200, Map.of("status", "ok"));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void upload(ChannelHandlerContext ctx, FullHttpRequest req) {
        String contentType = req.headers().get(HttpHeaderNames.CONTENT_TYPE);
        if (contentType == null || !contentType.contains("multipart/form-data")) {
            sendJson(ctx, 400, Map.of("error", "请使用 multipart/form-data 上传文件"));
            return;
        }

        try {
            MultipartForm form = parseMultipart(req, contentType);
            String uid = form.getField("uid");
            if (uid == null || uid.isEmpty()) uid = "default";

            String vdbIdStr = form.getField("vdb_id");
            if (vdbIdStr == null) {
                sendJson(ctx, 400, Map.of("error", "无效的知识库 ID"));
                return;
            }
            long vdbId = Long.parseLong(vdbIdStr);

            MultipartForm.FilePart file = form.getFile("file");
            if (file == null) {
                sendJson(ctx, 400, Map.of("error", "请选择文件"));
                return;
            }

            // Check file extension
            String fileName = file.filename;
            String ext = fileName.toLowerCase();
            int dotIdx = ext.lastIndexOf('.');
            if (dotIdx < 0 || !ALLOWED_EXTS.contains(ext.substring(dotIdx))) {
                sendJson(ctx, 400, Map.of("error", "不支持的文件格式，支持: txt, md, pdf, docx, xlsx"));
                return;
            }

            InputStream fileStream = new ByteArrayInputStream(file.content);
            VdbFileInfo finfo = kbMgr.uploadFile(vdbId, uid, fileName, fileStream);

            sendJson(ctx, 200, Map.of("status", "ok", "file", finfo));
        } catch (Exception e) {
            log.error("vdb_upload_file_failed", e);
            sendError(ctx, e.getMessage());
        }
    }

    public void processInfo(ChannelHandlerContext ctx, FullHttpRequest req) {
        long fileId = getLongParam(req, "file_id");
        try {
            VdbFileInfo finfo = store.getFileByID(fileId);
            sendJson(ctx, 200, Map.of("data", finfo != null ? finfo : Map.of()));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void search(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        String query = HttpServer.getParam(req, "query");

        if (query == null || query.isEmpty()) {
            sendJson(ctx, 400, Map.of("error", "请输入搜索内容"));
            return;
        }

        try {
            // Parse request body for vdb_ids / vdb_id
            String body = req.content().toString(CharsetUtil.UTF_8);
            VdbSearchRequest sr = MAPPER.readValue(body, VdbSearchRequest.class);

            String result;
            if (sr.getVdbIds() != null && !sr.getVdbIds().isEmpty()) {
                result = kbMgr.searchInKBs(query, sr.getVdbIds(), uid,
                        cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
            } else if (sr.getVdbId() != null && sr.getVdbId() > 0) {
                result = kbMgr.searchInKB(query, sr.getVdbId(), uid,
                        cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
            } else {
                result = kbMgr.searchAllKBs(query, uid,
                        cfg.getKb().getTopK(), cfg.getKb().getScoreThreshold());
            }

            sendJson(ctx, 200, Map.of("data", result != null ? result : ""));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void chunks(ChannelHandlerContext ctx, FullHttpRequest req) {
        long fileId = getLongParam(req, "file_id");
        if (fileId == 0) {
            sendJson(ctx, 400, Map.of("error", "无效的文件 ID"));
            return;
        }
        try {
            VdbFileInfo finfo = store.getFileByID(fileId);
            String uid = getUid(req);
            if (finfo == null || !uid.equals(finfo.getUid())) {
                sendJson(ctx, 404, Map.of("error", "文件不存在"));
                return;
            }
            List<SearchResult> chunks = kbMgr.getFileChunks(fileId);
            if (chunks == null) chunks = List.of();
            sendJson(ctx, 200, Map.of("data", chunks));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void download(ChannelHandlerContext ctx, FullHttpRequest req) {
        long fileId = getLongParam(req, "file_id");
        if (fileId == 0) {
            sendJson(ctx, 400, Map.of("error", "无效的文件 ID"));
            return;
        }
        try {
            VdbFileInfo finfo = store.getFileByID(fileId);
            String uid = getUid(req);
            if (finfo == null || !uid.equals(finfo.getUid())) {
                sendJson(ctx, 404, Map.of("error", "文件不存在"));
                return;
            }
            HttpServer.sendFile(ctx, finfo.getFilePath(), finfo.getName());
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    public void fileDelete(ChannelHandlerContext ctx, FullHttpRequest req) {
        String uid = getUid(req);
        long fileId = getLongParam(req, "file_id");
        try {
            kbMgr.deleteFile(fileId, uid);
            sendJson(ctx, 200, Map.of("status", "ok"));
        } catch (Exception e) {
            sendError(ctx, e.getMessage());
        }
    }

    // ============================================================
    // Helpers
    // ============================================================

    private String getUid(FullHttpRequest req) {
        String uid = HttpServer.getParam(req, "uid");
        return uid != null && !uid.isEmpty() ? uid : "default";
    }

    private long getLongParam(FullHttpRequest req, String key) {
        String val = HttpServer.getParam(req, key);
        if (val == null || val.isEmpty()) return 0;
        try { return Long.parseLong(val); } catch (NumberFormatException e) { return 0; }
    }

    private void sendJson(ChannelHandlerContext ctx, int statusCode, Object data) {
        try {
            String json = MAPPER.writeValueAsString(data);
            HttpServer.sendJson(ctx, statusCode, json);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "JSON 序列化失败");
        }
    }

    private void sendError(ChannelHandlerContext ctx, String message) {
        String safeMsg = message != null ? message : "内部服务器错误";
        Map<String, Object> m = new java.util.HashMap<>();
        m.put("error", safeMsg);
        sendJson(ctx, 500, m);
    }

    // ============================================================
    // Multipart parser (same as old VdbHandler)
    // ============================================================

    private MultipartForm parseMultipart(FullHttpRequest req, String contentType) {
        int boundaryIdx = contentType.indexOf("boundary=");
        if (boundaryIdx < 0) throw new RuntimeException("缺少 boundary");
        String boundary = "--" + contentType.substring(boundaryIdx + 9).trim();

        byte[] body = new byte[req.content().readableBytes()];
        req.content().readBytes(body);

        MultipartForm form = new MultipartForm();
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
                MultipartForm.FilePart file = new MultipartForm.FilePart();
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

    // ============================================================
    // CSM Business KB Bindings (admin only)
    // ============================================================

    /** GET /api/vdb/bindings — get current CSM branch KB bindings. */
    public void bindingGet(ChannelHandlerContext ctx, FullHttpRequest req) {
        Map<String, List<Long>> data = new LinkedHashMap<>();
        data.put("billing", csmEngine.billingVdbIDs());
        data.put("repair", csmEngine.repairVdbIDs());
        data.put("faq", csmEngine.faqVdbIDs());
        try {
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", data)));
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "获取绑定失败");
        }
    }

    /** PUT /api/vdb/bindings — save CSM branch KB bindings and hot-reload. */
    public void bindingPut(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            @SuppressWarnings("unchecked")
            Map<String, Object> payload = MAPPER.readValue(body, Map.class);

            @SuppressWarnings("unchecked")
            var billing = (List<Integer>) payload.get("billing");
            @SuppressWarnings("unchecked")
            var repair = (List<Integer>) payload.get("repair");
            @SuppressWarnings("unchecked")
            var faq = (List<Integer>) payload.get("faq");

            if (billing != null) {
                store.setConfig("csm.billing_vdb_ids", MAPPER.writeValueAsString(billing), "账单分支检索的知识库 id");
            }
            if (repair != null) {
                store.setConfig("csm.repair_vdb_ids", MAPPER.writeValueAsString(repair), "维修分支检索的知识库 id");
            }
            if (faq != null) {
                store.setConfig("csm.faq_vdb_ids", MAPPER.writeValueAsString(faq), "FAQ分支检索的知识库 id");
            }

            // Hot-reload to take effect immediately
            csmEngine.reloadVdbBindings();

            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            log.error("vdb_save_bindings_failed", e);
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "保存绑定失败: " + e.getMessage());
        }
    }

    static class MultipartForm {
        Map<String, String> fields = new HashMap<>();
        Map<String, FilePart> files = new HashMap<>();

        String getField(String name) { return fields.get(name); }
        FilePart getFile(String name) { return files.get(name); }

        static class FilePart {
            String fieldName;
            String filename;
            String contentType;
            byte[] content;
        }
    }
}