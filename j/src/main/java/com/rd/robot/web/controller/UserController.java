package com.rd.robot.web.controller;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.*;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.security.PasswordEncoder;
import com.rd.robot.security.TokenProvider;
import com.rd.robot.web.server.HttpServer;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Map;

/**
 * User management controller.
 */
public class UserController {

    private static final Logger log = LoggerFactory.getLogger(UserController.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final MetaStore metaStore;

    public UserController(MetaStore metaStore) {
        this.metaStore = metaStore;
    }

    // ============================================================
    // Admin: User management
    // ============================================================

    public void listUsers(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            List<User> users = metaStore.listUsers();
            if (users == null) users = List.of();

            // Strip password
            var resp = users.stream().map(u -> Map.of(
                    "uid", u.getUid(),
                    "user_name", u.getUserName(),
                    "role", u.getRole(),
                    "note", u.getNote() != null ? u.getNote() : ""
            )).toList();

            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", resp)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    public void createUser(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String body = req.content().toString(CharsetUtil.UTF_8);
            CreateUserRequest createReq = MAPPER.readValue(body, CreateUserRequest.class);

            // Validate password
            PasswordEncoder.validatePassword(createReq.getUserPwd());

            // Hash password with bcrypt
            String pwdHash = PasswordEncoder.hashPassword(createReq.getUserPwd());

            metaStore.createUser(createReq.getUserName(), pwdHash, createReq.getRole(), createReq.getNote());
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (IllegalArgumentException e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"" + e.getMessage() + "\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"创建用户失败: " + e.getMessage() + "\"}");
        }
    }

    public void deleteUser(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String userName = HttpServer.getParam(req, "name");
            if (userName == null || userName.isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"用户名不能为空\"}");
                return;
            }

            // Cannot delete self
            User currentUser = AuthController.parseUserFromRequest(req);
            if (currentUser != null && userName.equals(currentUser.getUserName())) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"不能删除自己\"}");
                return;
            }

            metaStore.deleteUserByName(userName);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"删除失败: " + e.getMessage() + "\"}");
        }
    }

    public void resetUserPwd(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            String userName = HttpServer.getParam(req, "name");
            String body = req.content().toString(CharsetUtil.UTF_8);
            ResetPwdRequest pwdReq = MAPPER.readValue(body, ResetPwdRequest.class);

            // Validate password
            PasswordEncoder.validatePassword(pwdReq.getUserPwd());

            // Hash password with bcrypt
            String pwdHash = PasswordEncoder.hashPassword(pwdReq.getUserPwd());

            metaStore.resetPassword(userName, pwdHash);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (IllegalArgumentException e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"" + e.getMessage() + "\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"重置密码失败: " + e.getMessage() + "\"}");
        }
    }

    // ============================================================
    // Self-service: Change password
    // ============================================================

    public void changePassword(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            User user = AuthController.parseUserFromRequest(req);
            if (user == null) {
                HttpServer.sendJson(ctx, 401, "{\"error\":\"未登录\"}");
                return;
            }

            String body = req.content().toString(CharsetUtil.UTF_8);
            ChangePwdRequest pwdReq = MAPPER.readValue(body, ChangePwdRequest.class);

            if (pwdReq.getNewPwd() == null || pwdReq.getNewPwd().isEmpty()) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"新密码不能为空\"}");
                return;
            }

            // Validate new password
            PasswordEncoder.validatePassword(pwdReq.getNewPwd());

            // Fetch current user to verify old password
            User dbUser = metaStore.getUserByName(user.getUserName());
            if (dbUser == null) {
                HttpServer.sendJson(ctx, 500, "{\"error\":\"获取用户信息失败\"}");
                return;
            }

            // Verify old password with bcrypt
            if (!PasswordEncoder.verifyPassword(pwdReq.getOldPwd(), dbUser.getUserPwd())) {
                HttpServer.sendJson(ctx, 400, "{\"error\":\"旧密码不正确\"}");
                return;
            }

            // Hash new password
            String newPwdHash = PasswordEncoder.hashPassword(pwdReq.getNewPwd());

            metaStore.updatePassword(user.getUserName(), newPwdHash);
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        } catch (IllegalArgumentException e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"" + e.getMessage() + "\"}");
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 400, "{\"error\":\"修改密码失败: " + e.getMessage() + "\"}");
        }
    }

    // ============================================================
    // API Token management
    // ============================================================

    public void listMyTokens(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            User user = AuthController.parseUserFromRequest(req);
            if (user == null) {
                HttpServer.sendJson(ctx, 401, "{\"error\":\"未登录\"}");
                return;
            }

            List<ApiToken> tokens = metaStore.getUserApiTokens(user.getUserName());
            if (tokens == null) tokens = List.of();

            var now = LocalDateTime.now();
            var resp = tokens.stream().map(t -> {
                boolean expiringSoon = t.getExpiresAt() != null &&
                        t.getExpiresAt().minusMinutes(10).isBefore(now);
                return Map.of(
                        "id", t.getId(),
                        "token_preview", t.getTokenPreview() != null ? t.getTokenPreview() : "",
                        "expires_at", t.getExpiresAt() != null ? t.getExpiresAt().toString() : "",
                        "expiring_soon", expiringSoon,
                        "create_time", t.getCreateTime() != null ? t.getCreateTime().toString() : ""
                );
            }).toList();

            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", resp)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }

    public void generateToken(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            User user = AuthController.parseUserFromRequest(req);
            if (user == null) {
                HttpServer.sendJson(ctx, 401, "{\"error\":\"未登录\"}");
                return;
            }

            int role = user.getRole();
            if (role != User.ROLE_API) role = User.ROLE_API;

            Instant expiry = Instant.now().plusSeconds(2 * 3600);
            String token = TokenProvider.generateToken(user.getUserName(), role, expiry);

            // Save to DB
            String preview = token.length() > 16 ? token.substring(0, 16) : token;
            LocalDateTime expiryLdt = LocalDateTime.ofInstant(expiry, ZoneId.systemDefault());
            metaStore.saveApiToken(user.getUserName(), preview, expiryLdt);

            Map<String, Object> resp = Map.of(
                    "status", "ok",
                    "token", token,
                    "expires_at", expiryLdt.toString()
            );
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(resp));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"生成 token 失败: " + e.getMessage() + "\"}");
        }
    }

    public void myCallLogs(ChannelHandlerContext ctx, FullHttpRequest req) {
        try {
            User user = AuthController.parseUserFromRequest(req);
            if (user == null) {
                HttpServer.sendJson(ctx, 401, "{\"error\":\"未登录\"}");
                return;
            }

            List<ApiCallLog> logs = metaStore.getUserApiCallLogs(user.getUserName());
            if (logs == null) logs = List.of();
            HttpServer.sendJson(ctx, 200, MAPPER.writeValueAsString(Map.of("data", logs)));
        } catch (Exception e) {
            HttpServer.sendJson(ctx, 500, "{\"error\":\"" + e.getMessage() + "\"}");
        }
    }
}