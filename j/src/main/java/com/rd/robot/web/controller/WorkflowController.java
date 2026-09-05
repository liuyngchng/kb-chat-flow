package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.engine.WorkflowEngine;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.stream.Collectors;

/**
 * Workflow management controller.
 */
public class WorkflowController {

    private static final Logger log = LoggerFactory.getLogger(WorkflowController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final MetaStore metaStore;

    public WorkflowController(MetaStore metaStore) {
        this.metaStore = metaStore;
    }

    /**
     * GET /api/workflows — public list for chat page dropdown
     */
    public void listPublic(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            List<WorkflowDef> workflows = metaStore.listWorkflows();
            var result = workflows.stream().map(w -> {
                Map<String, Object> m = new HashMap<>();
                m.put("id", w.getId());
                m.put("name", w.getName());
                m.put("description", w.getDescription() != null ? w.getDescription() : "");
                m.put("classifier", w.getClassifier());
                m.put("nodes", w.getNodes() != null ? w.getNodes() : List.of());
                m.put("created_at", w.getCreatedAt() != null ? w.getCreatedAt().toString() : "");
                m.put("updated_at", w.getUpdatedAt() != null ? w.getUpdatedAt().toString() : "");
                return m;
            }).toList();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", result)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取工作流列表失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/workflows/:id — get single workflow
     */
    public void get(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            WorkflowDef workflow = metaStore.getWorkflow(id);
            if (workflow == null) {
                HttpServer.sendJson(ctx, 404, "{\"error\":\"工作流不存在\"}");
                return;
            }

            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", workflow)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取工作流失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/workflows — create workflow
     */
    public void create(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateWorkflowRequest createReq = MAPPER.readValue(body, CreateWorkflowRequest.class);

            if (createReq.getNodes() == null || createReq.getNodes().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"工作流至少需要一个节点\"}");
                return;
            }

            // DAG mode: validate graph structure
            if (hasNextNodes(createReq.getNodes())) {
                WorkflowEngine.validateWorkflowGraph(createReq.getNodes());
                autoDetectIsFinal(createReq.getNodes());
            } else {
                // Linear mode: auto-mark last node as final
                createReq.getNodes().get(createReq.getNodes().size() - 1).setFinal(true);
            }

            WorkflowDef workflow = new WorkflowDef();
            workflow.setName(createReq.getName());
            workflow.setDescription(createReq.getDescription());
            workflow.setClassifier(createReq.getClassifier());
            workflow.setNodes(createReq.getNodes());

            long id = metaStore.createWorkflow(workflow);
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("status", "ok", "id", id)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"创建工作流失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * PUT /api/workflows/:id — update workflow
     */
    public void update(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            WorkflowDef existing = metaStore.getWorkflow(id);
            if (existing == null) {
                HttpServer.sendJson(ctx, 404, "{\"error\":\"工作流不存在\"}");
                return;
            }

            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateWorkflowRequest updateReq = MAPPER.readValue(body, CreateWorkflowRequest.class);

            if (updateReq.getNodes() == null || updateReq.getNodes().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"工作流至少需要一个节点\"}");
                return;
            }

            // DAG mode: validate graph structure
            if (hasNextNodes(updateReq.getNodes())) {
                WorkflowEngine.validateWorkflowGraph(updateReq.getNodes());
                autoDetectIsFinal(updateReq.getNodes());
            } else {
                // Linear mode: auto-set final flag
                for (int i = 0; i < updateReq.getNodes().size(); i++) {
                    updateReq.getNodes().get(i).setFinal(i == updateReq.getNodes().size() - 1);
                }
            }

            WorkflowDef workflow = new WorkflowDef();
            workflow.setId(id);
            workflow.setName(updateReq.getName());
            workflow.setDescription(updateReq.getDescription());
            workflow.setClassifier(updateReq.getClassifier());
            workflow.setNodes(updateReq.getNodes());

            metaStore.updateWorkflow(workflow);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"更新工作流失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * DELETE /api/workflows/:id — delete workflow
     */
    public void delete(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            metaStore.deleteWorkflow(id);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"删除工作流失败: " + e.getMessage() + "\"}");
        }
    }

    private static long parseIdFromPath(String uri) {
        String path = HttpServer.sanitizePath(uri);
        String rest = extractIdFromPath(path, "/api/workflows/", "/api/v1/workflows/", "/open_api/workflows/");
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

    /** Check if any node has DAG edges defined. */
    private static boolean hasNextNodes(List<WorkflowNode> nodes) {
        return nodes.stream().anyMatch(n -> n.getNextNodes() != null && !n.getNextNodes().isEmpty());
    }

    /** Auto-detect IsFinal: nodes with no outgoing edges are sinks. */
    private static void autoDetectIsFinal(List<WorkflowNode> nodes) {
        Set<String> referenced = new HashSet<>();
        for (WorkflowNode n : nodes) {
            if (n.getNextNodes() != null) referenced.addAll(n.getNextNodes());
        }
        for (WorkflowNode n : nodes) {
            n.setFinal(!referenced.contains(n.getId()));
        }
    }
}