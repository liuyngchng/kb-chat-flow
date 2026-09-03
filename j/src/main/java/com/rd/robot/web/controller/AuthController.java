package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.Config;
import com.rd.robot.model.LoginRequest;
import com.rd.robot.model.User;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.security.PasswordEncoder;
import com.rd.robot.security.TokenProvider;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Instant;
import java.time.LocalDateTime;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Authentication controller — login, logout, session management.
 */
public class AuthController {

    private static final Logger log = LoggerFactory.getLogger(AuthController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    // Login rate limiting constants
    private static final int LOGIN_MAX_FAILURES = 5;
    private static final long LOGIN_LOCK_DURATION_MS = 15 * 60 * 1000; // 15 minutes
    private static final String COOKIE_AUTH_TOKEN = "auth_token";

    private final Config cfg;
    private final MetaStore metaStore;
    private final PresenceStore presenceStore;

    // Login rate limiting: IP -> {count, lockedUntil}
    private final ConcurrentHashMap<String, LoginFailRecord> loginFailures = new ConcurrentHashMap<>();
    private final Object cleanupLock = new Object();
    private Thread cleanupThread = null;

    public AuthController(Config cfg, MetaStore metaStore, PresenceStore presenceStore) {
        this.cfg = cfg;
        this.metaStore = metaStore;
        this.presenceStore = presenceStore;
    }

    /**
     * POST /api/login — JSON login
     */
    public void login(ChannelHandlerContext ctx, FullHttpRequest request) {
        try {
            String body = request.content().toString(CharsetUtil.UTF_8);
            LoginRequest req = MAPPER.readValue(body, LoginRequest.class);

            if (req.getUserName() == null || req.getUserName().isEmpty()
                    || req.getUserPwd() == null || req.getUserPwd().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"用户名和密码不能为空\"}");
                return;
            }

            String clientIP = clientIP(request);

            // Check login rate limiting
            if (isLoginLocked(clientIP)) {
                HttpServer.sendJson(ctx, 429, "{\"error\":\"登录失败次数过多，请稍后再试\"}");
                return;
            }

            // Query user by name only (password verification via bcrypt)
            User user = metaStore.getUserByLogin(req.getUserName());

            // Verify password with bcrypt
            if (user == null || !PasswordEncoder.verifyPassword(req.getUserPwd(), user.getUserPwd())) {
                recordLoginFailure(clientIP);
                HttpServer.sendJson(ctx, 401, "{\"error\":\"用户名或密码错误\"}");
                return;
            }

            // Login success, clear failure record
            clearLoginFailures(clientIP);

            // 检查密码是否过期（仅 SQLite 单机版种子 admin 有此字段）
            if (user.getPwdExpiresAt() != null && LocalDateTime.now().isAfter(user.getPwdExpiresAt())) {
                HttpServer.sendJson(ctx, 403, "{\"error\":\"密码已过期，请联系管理员重置\"}");
                return;
            }
            boolean mustChangePwd = user.getPwdExpiresAt() != null;

            // admin 实例：仅管理员可登录
            if (cfg.getServer().isAdminOnly() && user.getRole() != User.ROLE_ADMIN) {
                HttpServer.sendJson(ctx, 403, "{\"error\":\"此账号无法访问管理后台\"}");
                return;
            }

            Instant expiry = Instant.now().plusSeconds(2 * 3600);
            String token = TokenProvider.generateToken(user.getUserName(), user.getRole(), expiry);

            // Track online agents
            if (user.getRole() == User.ROLE_AGENT) {
                presenceStore.setPresence(user.getUserName(), System.currentTimeMillis());
            }

            // Set httpOnly cookie
            setAuthCookie(ctx, request, token, 2 * 3600);

            Map<String, Object> resp = Map.of(
                    "status", "ok",
                    "token", token,
                    "user_name", user.getUserName(),
                    "role", user.getRole(),
                    "must_change_pwd", mustChangePwd
            );
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));

        } catch (Exception e) {
            log.error("auth_login_failed", e);
            HttpServer.sendJson(ctx, 500, "{\"error\":\"登录失败: " + e.getMessage() + "\"}");
        }
    }

    /**
     * POST /api/logout — logout
     */
    public void logout(ChannelHandlerContext ctx, FullHttpRequest request) {
        String token = extractToken(request);
        if (token != null) {
            User user = TokenProvider.parseToken(token);
            if (user != null) {
                presenceStore.removePresence(user.getUserName());
            }
            // Blacklist the token so it can't be reused
            TokenProvider.blacklistToken(token);
        }
        // Clear auth cookie
        clearAuthCookie(ctx, request);
        HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
    }

    /**
     * GET /api/me — current user info
     */
    public void me(ChannelHandlerContext ctx, FullHttpRequest request) {
        String token = extractToken(request);
        if (token == null) {
            HttpServer.sendJson(ctx, 401, "{\"error\":\"未登录\"}");
            return;
        }

        User user = TokenProvider.parseToken(token);
        if (user == null) {
            HttpServer.sendJson(ctx, 401, "{\"error\":\"token 无效或已过期\"}");
            return;
        }

        HttpServer.sendJson(ctx, 200, String.format(
                "{\"user_name\":\"%s\",\"role\":%d}", user.getUserName(), user.getRole()));
    }

    /**
     * GET /api/agents — online agents list
     */
    public void getOnlineAgents(ChannelHandlerContext ctx, FullHttpRequest request) {
        List<Map<String, Object>> agents = presenceStore.getOnlineAgents();

        StringBuilder sb = new StringBuilder("{\"agents\":[");
        boolean first = true;
        for (var info : agents) {
            if (!first) sb.append(",");
            first = false;
            sb.append("{");
            boolean firstField = true;
            for (var entry : info.entrySet()) {
                if (!firstField) sb.append(",");
                firstField = false;
                sb.append("\"").append(entry.getKey()).append("\":\"")
                        .append(escapeJson(String.valueOf(entry.getValue()))).append("\"");
            }
            sb.append("}");
        }
        sb.append("]}");
        HttpServer.sendJson(ctx, 200, sb.toString());
    }

    // ============================================================
    // Token extraction helpers
    // ============================================================

    public static String extractToken(FullHttpRequest request) {
        // Cookie first (browser users)
        String cookie = request.headers().get(HttpHeaderNames.COOKIE);
        String token = TokenProvider.extractToken(
                request.headers().get(HttpHeaderNames.AUTHORIZATION),
                cookie,
                HttpServer.getQueryParam(request, "t")
        );
        return token;
    }

    public static User parseUserFromRequest(FullHttpRequest request) {
        String token = extractToken(request);
        if (token == null) return null;
        return TokenProvider.parseToken(token);
    }

    // ============================================================
    // Cookie helpers
    // ============================================================

    private static void setAuthCookie(ChannelHandlerContext ctx, FullHttpRequest request, String token, int maxAge) {
        // Determine if the connection is secure (TLS or proxied)
        boolean secure = request.headers().contains("X-Forwarded-Proto", "https", true)
                || request.headers().contains("X-Forwarded-Scheme", "https", true);

        String cookie = String.format(
                "%s=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Strict%s",
                COOKIE_AUTH_TOKEN, token, maxAge, secure ? "; Secure" : ""
        );
        // Note: Cookie will be set in the response via a separate mechanism
        // Store for the response handler to add
        request.headers().set("X-Set-Cookie", cookie);
    }

        private static void clearAuthCookie(ChannelHandlerContext ctx, FullHttpRequest request) {
        // 通过 X-Set-Cookie 机制下发清除 Cookie（HttpServer 会转成标准 Set-Cookie）
        String cookie = String.format("%s=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict", COOKIE_AUTH_TOKEN);
        request.headers().set("X-Set-Cookie", cookie);
    }

    // ============================================================
    // Login rate limiting (IP-based)
    // ============================================================

    private static String clientIP(FullHttpRequest request) {
        // X-Forwarded-For from reverse proxy
        String fwd = request.headers().get("X-Forwarded-For");
        if (fwd != null && !fwd.isEmpty()) {
            int commaIdx = fwd.indexOf(',');
            return (commaIdx > 0 ? fwd.substring(0, commaIdx) : fwd).trim();
        }
        // X-Real-IP
        String realIP = request.headers().get("X-Real-IP");
        if (realIP != null && !realIP.isEmpty()) {
            return realIP.trim();
        }
        // Fall back to remote address
        try {
            String remoteAddr = request.headers().get("X-Remote-Addr");
            if (remoteAddr != null) return remoteAddr;
        } catch (Exception ignored) {}
        return "unknown";
    }

    private boolean isLoginLocked(String ip) {
        LoginFailRecord rec = loginFailures.get(ip);
        if (rec == null) return false;
        return rec.count >= LOGIN_MAX_FAILURES && System.currentTimeMillis() < rec.lockedUntil;
    }

    private void recordLoginFailure(String ip) {
        LoginFailRecord rec = loginFailures.computeIfAbsent(ip, k -> new LoginFailRecord());
        rec.count++;
        if (rec.count >= LOGIN_MAX_FAILURES) {
            rec.lockedUntil = System.currentTimeMillis() + LOGIN_LOCK_DURATION_MS;
        }
        ensureCleanupStarted();
    }

    private void clearLoginFailures(String ip) {
        loginFailures.remove(ip);
    }

    private void ensureCleanupStarted() {
        synchronized (cleanupLock) {
            if (cleanupThread == null || !cleanupThread.isAlive()) {
                cleanupThread = new Thread(() -> {
                    while (!Thread.currentThread().isInterrupted()) {
                        try {
                            Thread.sleep(15 * 60 * 1000); // every 15 minutes
                        } catch (InterruptedException e) {
                            break;
                        }
                        long now = System.currentTimeMillis();
                        loginFailures.entrySet().removeIf(e -> {
                            LoginFailRecord r = e.getValue();
                            return now > r.lockedUntil && r.count >= LOGIN_MAX_FAILURES;
                        });
                    }
                }, "login-failures-cleanup");
                cleanupThread.setDaemon(true);
                cleanupThread.start();
            }
        }
    }

    // ============================================================
    // Helpers
    // ============================================================

    private static String escapeJson(String s) {
        if (s == null) return "";
        StringBuilder sb = new StringBuilder();
        for (char c : s.toCharArray()) {
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default: sb.append(c);
            }
        }
        return sb.toString();
    }

    private static class LoginFailRecord {
        int count = 0;
        long lockedUntil = 0;
    }
}