package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.engine.TemplateResolver;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.List;
import java.util.Map;

/**
 * AI Agent management controller.
 */
public class AgentController {

    private static final Logger log = LoggerFactory.getLogger(AgentController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final MetaStore metaStore;

    public AgentController(MetaStore metaStore) {
        this.metaStore = metaStore;
    }

    /**
     * GET /api/system-vars — list available system variables for agent prompts
     */
    public void listSystemVars(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            var vars = TemplateResolver.getSystemVars();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", vars)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/ai-agents/public — public list for chat page dropdown
     */
    public void listPublic(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            List<AgentDef> agents = metaStore.listAgents();
            var result = agents.stream().map(a -> Map.of("id", a.getId(), "name", a.getName())).toList();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", result)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取智能体列表失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/ai-agents — admin full list
     */
    public void list(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            List<AgentDef> agents = metaStore.listAgents();
            if (agents == null) agents = List.of();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", agents)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取智能体列表失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * GET /api/ai-agents/:id — get single agent
     */
    public void get(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            AgentDef agent = metaStore.getAgent(id);
            if (agent == null) {
                HttpServer.sendJson(ctx, 404, "{\"error\":\"智能体不存在\"}");
                return;
            }

            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", agent)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"获取智能体失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/ai-agents — create agent
     */
    public void create(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateAgentRequest createReq = MAPPER.readValue(body, CreateAgentRequest.class);

            // Validate system prompt variable references
            List<String> invalid = TemplateResolver.validateTemplateVars(createReq.getSystemPrompt());
            if (!invalid.isEmpty()) {
                log.warn("agent_create_system_prompt_invalid_vars={}", invalid);
                HttpServer.sendJson(ctx, 400,
                    "{\"error\":\"system_prompt 包含非法的系统变量：" + String.join("、", invalid) + "\"}");
                return;
            }

            // Serialize VdbIDs as JSON string
            String vdbIdsJson = "[]";
            if (createReq.getVdbIds() != null && !createReq.getVdbIds().isEmpty()) {
                vdbIdsJson = MAPPER.writeValueAsString(createReq.getVdbIds());
            }

            AgentDef agent = new AgentDef();
            agent.setName(createReq.getName());
            agent.setDescription(createReq.getDescription());
            agent.setSystemPrompt(createReq.getSystemPrompt());
            agent.setModelName(createReq.getModelName());
            agent.setTemperature(createReq.getTemperature());
            agent.setTopP(createReq.getTopP());
            agent.setMaxTokens(createReq.getMaxTokens());
            agent.setVdbIds(vdbIdsJson);

            long id = metaStore.createAgent(agent);
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("status", "ok", "id", id)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"创建智能体失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * PUT /api/ai-agents/:id — update agent
     */
    public void update(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            // Check exists
            AgentDef existing = metaStore.getAgent(id);
            if (existing == null) {
                HttpServer.sendJson(ctx, 404, "{\"error\":\"智能体不存在\"}");
                return;
            }

            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateAgentRequest updateReq = MAPPER.readValue(body, CreateAgentRequest.class);

            // Validate system prompt variable references
            List<String> invalid = TemplateResolver.validateTemplateVars(updateReq.getSystemPrompt());
            if (!invalid.isEmpty()) {
                log.warn("agent_update_system_prompt_invalid_vars={}", invalid);
                HttpServer.sendJson(ctx, 400,
                    "{\"error\":\"system_prompt 包含非法的系统变量：" + String.join("、", invalid) + "\"}");
                return;
            }

            String vdbIdsJson = "[]";
            if (updateReq.getVdbIds() != null && !updateReq.getVdbIds().isEmpty()) {
                vdbIdsJson = MAPPER.writeValueAsString(updateReq.getVdbIds());
            }

            AgentDef agent = new AgentDef();
            agent.setId(id);
            agent.setName(updateReq.getName());
            agent.setDescription(updateReq.getDescription());
            agent.setSystemPrompt(updateReq.getSystemPrompt());
            agent.setModelName(updateReq.getModelName());
            agent.setTemperature(updateReq.getTemperature());
            agent.setTopP(updateReq.getTopP());
            agent.setMaxTokens(updateReq.getMaxTokens());
            agent.setVdbIds(vdbIdsJson);

            metaStore.updateAgent(agent);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"更新智能体失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * DELETE /api/ai-agents/:id — delete agent
     */
    public void delete(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            long id = parseIdFromPath(req.uri());
            if (id == 0) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"无效的 ID\"}");
                return;
            }

            metaStore.deleteAgent(id);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"删除智能体失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * Parse :id from path like /api/ai-agents/123 or /api/ai-agents/123/something
     */
    private static long parseIdFromPath(String uri) {
        String path = HttpServer.sanitizePath(uri);
        // Pattern: /api/ai-agents/{id} or /api/ai-agents/{id}/*
        if (path.startsWith("/api/ai-agents/")) {
            String rest = path.substring(15);
            int slashIdx = rest.indexOf('/');
            if (slashIdx >= 0) rest = rest.substring(0, slashIdx);
            try { return Long.parseLong(rest); } catch (NumberFormatException ignored) {}
        }
        return 0;
    }
}