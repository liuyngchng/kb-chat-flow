package com.rd.robot.web.controller;

import com.rd.robot.model.Config;
import com.rd.robot.model.User;
import com.rd.robot.security.TokenProvider;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.InputStream;
import java.nio.charset.StandardCharsets;

/**
 * Controller for page rendering (HTML).
 */
public class PageController {

    private static final Logger log = LoggerFactory.getLogger(PageController.class);

    private final Config cfg;

    public PageController(Config cfg) {
        this.cfg = cfg;
    }

    /** 根据服务角色生成页面标题 */
    private String pageTitle() {
        String name = cfg.getSys().getName();
        if ("admin".equals(cfg.getServer().getRole())) {
            return name + "系统管理";
        }
        return name;
    }

    public void index(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (requireAuth(ctx, request)) return;
        try {
            String uid = getUid(request);
            String token = getToken(request);
            int role = getUserRole(request);

            String html = loadTemplate("templates/index.html")
                    .replace("{{sys_name}}", cfg.getSys().getName())
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{uid}}", uid)
                    .replace("{{role}}", String.valueOf(role))
                    .replace("{{token}}", token != null ? token : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void vdbIndex(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (requireAuth(ctx, request)) return;
        try {
            String uid = getUid(request);
            String token = getToken(request);
            int role = getUserRole(request);

            String html = loadTemplate("templates/vdb.html")
                    .replace("{{sys_name}}", cfg.getSys().getName())
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{uid}}", uid)
                    .replace("{{role}}", String.valueOf(role))
                    .replace("{{token}}", token != null ? token : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void configIndex(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (requireAuth(ctx, request)) return;
        // Admin-only page
        int role = getUserRole(request);
        if (role != User.ROLE_ADMIN) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.FORBIDDEN, "仅管理员可访问");
            return;
        }
        try {
            String uid = getUid(request);
            String token = getToken(request);

            String html = loadTemplate("templates/config.html")
                    .replace("{{sys_name}}", cfg.getSys().getName())
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{uid}}", uid)
                    .replace("{{role}}", String.valueOf(role))
                    .replace("{{token}}", token != null ? token : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void vdbBindIndex(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (requireAuth(ctx, request)) return;
        int role = getUserRole(request);
        if (role != User.ROLE_ADMIN) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.FORBIDDEN, "仅管理员可访问");
            return;
        }
        try {
            String uid = getUid(request);
            String token = getToken(request);

            String html = loadTemplate("templates/vdb_bind.html")
                    .replace("{{sys_name}}", cfg.getSys().getName())
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{uid}}", uid)
                    .replace("{{token}}", token != null ? token : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void userApiIndex(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (requireAuth(ctx, request)) return;
        try {
            String uid = getUid(request);
            String token = getToken(request);

            String html = loadTemplate("templates/user_api.html")
                    .replace("{{sys_name}}", cfg.getSys().getName())
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{uid}}", uid)
                    .replace("{{token}}", token != null ? token : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void loginPage(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String html = loadTemplate("templates/login.html")
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{error_msg}}", "")
                    .replace("{{debug}}", String.valueOf(cfg.getServer().isDebug()));
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    public void registerPage(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String msg = HttpServer.getQueryParam(request, "msg");
            String html = loadTemplate("templates/register.html")
                    .replace("{{page_title}}", pageTitle())
                    .replace("{{msg}}", msg != null ? msg : "");
            HttpServer.sendHtml(ctx, html);
        } catch (Exception e) {
            HttpServer.sendError(ctx, io.netty.handler.codec.http.HttpResponseStatus.INTERNAL_SERVER_ERROR, "页面加载失败");
        }
    }

    private String loadTemplate(String path) throws Exception {
        try (InputStream in = getClass().getClassLoader().getResourceAsStream(path)) {
            if (in == null) throw new RuntimeException("模板文件不存在: " + path);
            return new String(in.readAllBytes(), StandardCharsets.UTF_8);
        }
    }

    private String getUid(FullHttpRequest request) {
        // Get username from authenticated token first
        String token = getToken(request);
        if (token != null) {
            User user = TokenProvider.parseToken(token);
            if (user != null) return user.getUserName();
        }
        // Fallback to query param
        String uid = HttpServer.getQueryParam(request, "uid");
        return uid != null ? uid : "default";
    }

    private String getToken(FullHttpRequest request) {
        // Cookie first (browser users)
        String cookie = request.headers().get("Cookie");
        String token = TokenProvider.extractToken(
                request.headers().get("Authorization"),
                cookie,
                HttpServer.getQueryParam(request, "t")
        );
        return token;
    }

    private int getUserRole(FullHttpRequest request) {
        String token = getToken(request);
        if (token == null) return User.ROLE_NORMAL;
        User user = TokenProvider.parseToken(token);
        return user != null ? user.getRole() : User.ROLE_NORMAL;
    }

    /**
     * Check page auth. Returns true if redirected to login (caller should return).
     */
    private boolean requireAuth(ChannelHandlerContext ctx, FullHttpRequest request) {
        if (!cfg.getSys().isAuth()) return false;
        String token = getToken(request);
        if (token == null) {
            HttpServer.sendRedirect(ctx, "/login");
            return true;
        }
        User user = TokenProvider.parseToken(token);
        if (user == null) {
            HttpServer.sendRedirect(ctx, "/login");
            return true;
        }
        return false;
    }
}