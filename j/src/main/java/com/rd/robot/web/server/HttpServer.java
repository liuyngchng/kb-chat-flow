package com.rd.robot.web.server;

import com.rd.robot.model.Config;
import com.rd.robot.model.User;
import com.rd.robot.security.TokenProvider;
import com.rd.robot.web.router.RouteHandler;
import com.rd.robot.web.router.Router;
import io.netty.bootstrap.ServerBootstrap;
import io.netty.buffer.Unpooled;
import io.netty.channel.*;
import io.netty.channel.nio.NioEventLoopGroup;
import io.netty.channel.socket.nio.NioServerSocketChannel;
import io.netty.handler.codec.http.*;
import io.netty.handler.stream.ChunkedWriteHandler;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.InputStream;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Netty-based HTTP server with routing, static file serving, and middleware support.
 */
public class HttpServer {

    private static final Logger log = LoggerFactory.getLogger(HttpServer.class);

    private final int port;
    private final Router router;
    private final Config config;
    private final EventLoopGroup bossGroup;
    private final EventLoopGroup workerGroup;
    private Channel channel;

    // ThreadLocal for path parameters per request
    private static final ThreadLocal<Map<String, String>> PATH_PARAMS = ThreadLocal.withInitial(ConcurrentHashMap::new);

    // ThreadLocal for the current request, so response helpers can emit Set-Cookie
    // (AuthController sets X-Set-Cookie on the request; response layer converts it).
    private static final ThreadLocal<FullHttpRequest> CURRENT_REQUEST = new ThreadLocal<>();

    // Security response headers
    private static final Map<String, String> SECURITY_HEADERS = Map.of(
            "Strict-Transport-Security", "max-age=31536000; includeSubDomains",
            "X-Content-Type-Options", "nosniff",
            "X-Frame-Options", "DENY",
            "X-XSS-Protection", "0",
            "Referrer-Policy", "strict-origin-when-cross-origin",
            "Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com; script-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'"
    );

    public HttpServer(int port, Router router, Config config) {
        this.port = port;
        this.router = router;
        this.config = config;
        this.bossGroup = new NioEventLoopGroup(1);
        this.workerGroup = new NioEventLoopGroup();
    }

    public void start() {
        try {
            ServerBootstrap bootstrap = new ServerBootstrap();
            bootstrap.group(bossGroup, workerGroup)
                    .channel(NioServerSocketChannel.class)
                    .childHandler(new ChannelInitializer<Channel>() {
                        @Override
                        protected void initChannel(Channel ch) {
                            ChannelPipeline p = ch.pipeline();
                            p.addLast(new HttpServerCodec());
                            p.addLast(new HttpObjectAggregator(64 * 1024 * 1024)); // 64MB
                            p.addLast(new ChunkedWriteHandler());
                            p.addLast(new ServerHandler(router, config));
                        }
                    });

            channel = bootstrap.bind(port).sync().channel();
            log.info("HTTP 服务器已启动端口={}", port);
        } catch (Exception e) {
            throw new RuntimeException("启动 HTTP 服务器失败", e);
        }
    }

    public void stop() {
        try {
            if (channel != null) channel.close().sync();
        } catch (Exception ignored) {}
        bossGroup.shutdownGracefully();
        workerGroup.shutdownGracefully();
    }

    // ============================================================
    // Netty ChannelHandler
    // ============================================================

    private static class ServerHandler extends SimpleChannelInboundHandler<FullHttpRequest> {

        private final Router router;
        private final Config config;

        ServerHandler(Router router, Config config) {
            this.router = router;
            this.config = config;
        }

        @Override
        protected void channelRead0(ChannelHandlerContext ctx, FullHttpRequest request) {
            CURRENT_REQUEST.set(request);
            String path = sanitizePath(request.uri());
            String method = request.method().name();

            // Static files
            if (path.startsWith("/static/")) {
                serveStatic(ctx, path);
                CURRENT_REQUEST.remove();
                return;
            }

            // Route matching
            Router.RouteMatch match = router.match(method, path);
            if (match != null) {
                // Store path params
                PATH_PARAMS.set(match.getPathParams());

                try {
                    // Apply auth middleware based on path
                    if (requiresAuth(path)) {
                        User user = authenticateByScheme(path, request);
                        if (user == null) {
                            if (path.startsWith("/open_api/")) {
                                sendJson(ctx, 401, "{\"error\":\"未提供认证 token，请使用 Authorization: Bearer <token>\"}");
                            } else {
                                sendJson(ctx, 401, "{\"error\":\"未提供有效认证 token\"}");
                            }
                            return;
                        }
                        // Check admin-only paths
                        if (isAdminPath(path, method) && user.getRole() != User.ROLE_ADMIN) {
                            sendJson(ctx, 403, "{\"error\":\"仅管理员可访问\"}");
                            return;
                        }
                    }

                    match.getHandler().handle(ctx, request);
                } catch (Exception e) {
                    log.error("处理请求失败 method={} path={}", method, path, e);
                    sendError(ctx, HttpResponseStatus.INTERNAL_SERVER_ERROR, "内部服务器错误");
                } finally {
                    PATH_PARAMS.remove();
                    CURRENT_REQUEST.remove();
                }
            } else {
                CURRENT_REQUEST.remove();
                sendError(ctx, HttpResponseStatus.NOT_FOUND, "404 Not Found");
            }
        }

        @Override
        public void exceptionCaught(ChannelHandlerContext ctx, Throwable cause) {
            log.error("HTTP 处理异常", cause);
            ctx.close();
        }

        private boolean requiresAuth(String path) {
            // /open_api/* is always authenticated (third-party API, Bearer header), independent of api_auth switch
            if (path.startsWith("/open_api/")) return true;
            // If API auth is disabled globally, skip token check for frontend API
            if (!config.getSys().isApiAuth()) return false;
            // Only API v1 routes need token authentication
            if (!path.startsWith("/api/v1/")) return false;
            // Login API is public
            if (path.equals("/api/login")) return false;
            return true;
        }

        /**
         * 按路径选择认证方案：
         *  - /open_api/*（第三方）: 仅接受 Authorization: Bearer header
         *  - /api/v1/*（前端）:    仅接受 httpOnly Cookie
         *  - 其他（兼容）:          Cookie 优先，Bearer 兜底
         */
        private User authenticateByScheme(String path, FullHttpRequest request) {
            if (path.startsWith("/open_api/")) {
                String tokenStr = TokenProvider.extractTokenFromBearer(
                        request.headers().get(HttpHeaderNames.AUTHORIZATION));
                return tokenStr != null ? TokenProvider.parseToken(tokenStr) : null;
            }
            if (path.startsWith("/api/v1/")) {
                String tokenStr = TokenProvider.extractTokenFromCookie(
                        request.headers().get(HttpHeaderNames.COOKIE));
                return tokenStr != null ? TokenProvider.parseToken(tokenStr) : null;
            }
            // 兼容旧路径（/api/）: Cookie 优先，Bearer 兜底
            return authenticateRequest(request);
        }

        private boolean isAdminPath(String path, String method) {
            // Pages
            if (path.startsWith("/admin/")) return true;

            // Normalize: strip /api/v1 and /open_api prefixes, then reuse the same admin-path rules.
            String normalized = path;
            if (path.startsWith("/api/v1/")) {
                normalized = "/api/" + path.substring("/api/v1/".length());
            } else if (path.startsWith("/open_api/")) {
                normalized = "/api/" + path.substring("/open_api/".length());
            }

            // Config write
            if (normalized.equals("/api/config") && !"GET".equalsIgnoreCase(method)) return true;
            if (normalized.equals("/api/config/test-models")) return true;
            // VDB write
            if (normalized.equals("/api/vdb") && ("POST".equalsIgnoreCase(method) || "PUT".equalsIgnoreCase(method))) return true;
            if (normalized.matches("/api/vdb/\\d+") && "DELETE".equalsIgnoreCase(method)) return true;
            if (normalized.matches("/api/vdb/\\d+/default") && "PUT".equalsIgnoreCase(method)) return true;
            if (normalized.matches("/api/vdb/\\d+/files") && "GET".equalsIgnoreCase(method)) return true;
            if (normalized.matches("/api/vdb/\\d+/upload") && "POST".equalsIgnoreCase(method)) return true;
            if (normalized.matches("/api/vdb/file/\\d+/progress")) return true;
            if (normalized.matches("/api/vdb/file/\\d+/chunks")) return true;
            if (normalized.matches("/api/vdb/file/\\d+/download")) return true;
            if (normalized.startsWith("/api/vdb/file/") && normalized.endsWith("/delete")) return true;
            if (normalized.startsWith("/api/vdb/bindings")) return true;
            // FAQ write
            if (normalized.equals("/api/faq") && ("POST".equalsIgnoreCase(method) || "DELETE".equalsIgnoreCase(method))) return true;
            if (normalized.equals("/api/faq/upload")) return true;
            if (normalized.matches("/api/faq/\\d+") && ("PUT".equalsIgnoreCase(method) || "DELETE".equalsIgnoreCase(method))) return true;
            // User management
            if (normalized.equals("/api/users")) return true;
            if (normalized.matches("/api/users/[^/]+")) return true;
            // Workflow write
            if (normalized.equals("/api/workflows") && "POST".equalsIgnoreCase(method)) return true;
            if (normalized.matches("/api/workflows/\\d+") && ("PUT".equalsIgnoreCase(method) || "DELETE".equalsIgnoreCase(method))) return true;
            return false;
        }

        private User authenticateRequest(FullHttpRequest request) {
            String tokenStr = TokenProvider.extractToken(
                    request.headers().get(HttpHeaderNames.AUTHORIZATION),
                    request.headers().get(HttpHeaderNames.COOKIE),
                    getQueryParam(request, "t")
            );
            if (tokenStr == null) return null;
            return TokenProvider.parseToken(tokenStr);
        }
    }

    // ============================================================
    // Static file serving
    // ============================================================

    private static void serveStatic(ChannelHandlerContext ctx, String path) {
        String resourcePath = "static/" + path.substring(8);

        try (InputStream in = HttpServer.class.getClassLoader().getResourceAsStream(resourcePath)) {
            if (in == null) {
                sendError(ctx, HttpResponseStatus.NOT_FOUND, "404 Not Found");
                return;
            }

            byte[] data = in.readAllBytes();
            String contentType = getContentType(path);

            FullHttpResponse response = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, HttpResponseStatus.OK,
                    Unpooled.wrappedBuffer(data));
            response.headers()
                    .set(HttpHeaderNames.CONTENT_TYPE, contentType)
                    .set(HttpHeaderNames.CONTENT_LENGTH, data.length)
                    .set(HttpHeaderNames.CACHE_CONTROL, "public, max-age=3600");
            ctx.writeAndFlush(response).addListener(ChannelFutureListener.CLOSE);
        } catch (Exception e) {
            sendError(ctx, HttpResponseStatus.INTERNAL_SERVER_ERROR, "读取静态文件失败");
        }
    }

    private static String getContentType(String path) {
        if (path.endsWith(".css")) return "text/css; charset=utf-8";
        if (path.endsWith(".js")) return "application/javascript; charset=utf-8";
        if (path.endsWith(".html")) return "text/html; charset=utf-8";
        if (path.endsWith(".png")) return "image/png";
        if (path.endsWith(".jpg") || path.endsWith(".jpeg")) return "image/jpeg";
        if (path.endsWith(".svg")) return "image/svg+xml";
        if (path.endsWith(".woff2")) return "font/woff2";
        if (path.endsWith(".woff")) return "font/woff";
        return "application/octet-stream";
    }

    // ============================================================
    // Response helpers
    // ============================================================

    public static void sendRedirect(ChannelHandlerContext ctx, String location) {
        FullHttpResponse response = new DefaultFullHttpResponse(
                HttpVersion.HTTP_1_1, HttpResponseStatus.FOUND);
        response.headers().set(HttpHeaderNames.LOCATION, location);
        response.headers().set(HttpHeaderNames.CONTENT_LENGTH, 0);
        applySetCookie(response);
        ctx.writeAndFlush(response).addListener(ChannelFutureListener.CLOSE);
    }

    public static void sendJson(ChannelHandlerContext ctx, int statusCode, String json) {
        FullHttpResponse response = new DefaultFullHttpResponse(
                HttpVersion.HTTP_1_1,
                HttpResponseStatus.valueOf(statusCode),
                Unpooled.copiedBuffer(json, CharsetUtil.UTF_8));
        response.headers()
                .set(HttpHeaderNames.CONTENT_TYPE, "application/json; charset=utf-8")
                .set(HttpHeaderNames.CONTENT_LENGTH, response.content().readableBytes());
        applySecurityHeaders(response);
        applySetCookie(response);
        ctx.writeAndFlush(response).addListener(ChannelFutureListener.CLOSE);
    }

    public static void sendHtml(ChannelHandlerContext ctx, String html) {
        FullHttpResponse response = new DefaultFullHttpResponse(
                HttpVersion.HTTP_1_1, HttpResponseStatus.OK,
                Unpooled.copiedBuffer(html, CharsetUtil.UTF_8));
        response.headers()
                .set(HttpHeaderNames.CONTENT_TYPE, "text/html; charset=utf-8")
                .set(HttpHeaderNames.CONTENT_LENGTH, response.content().readableBytes());
        applySecurityHeaders(response);
        applySetCookie(response);
        ctx.writeAndFlush(response).addListener(ChannelFutureListener.CLOSE);
    }

    /**
     * 将当前请求上由 AuthController 设置的 X-Set-Cookie 头转为标准 Set-Cookie 响应头。
     * X-Set-Cookie 机制让登录/注销逻辑无需直接访问响应对象即可下发 Cookie。
     */
    private static void applySetCookie(FullHttpResponse response) {
        FullHttpRequest request = CURRENT_REQUEST.get();
        if (request == null) return;
        String setCookie = request.headers().get("X-Set-Cookie");
        if (setCookie != null && !setCookie.isEmpty()) {
            response.headers().set(HttpHeaderNames.SET_COOKIE, setCookie);
        }
    }

    private static void applySecurityHeaders(FullHttpResponse response) {
        for (var entry : SECURITY_HEADERS.entrySet()) {
            response.headers().set(entry.getKey(), entry.getValue());
        }
    }

    public static void sendFile(ChannelHandlerContext ctx, String filePath, String fileName) {
        try {
            java.io.File file = new java.io.File(filePath);
            if (!file.exists() || !file.isFile()) {
                sendError(ctx, HttpResponseStatus.NOT_FOUND, "文件不存在");
                return;
            }
            byte[] content = java.nio.file.Files.readAllBytes(file.toPath());
            FullHttpResponse response = new DefaultFullHttpResponse(
                    HttpVersion.HTTP_1_1, HttpResponseStatus.OK,
                    Unpooled.wrappedBuffer(content));
            response.headers()
                    .set(HttpHeaderNames.CONTENT_TYPE, "application/octet-stream")
                    .set(HttpHeaderNames.CONTENT_DISPOSITION, "attachment; filename=\"" +
                            new String(fileName.getBytes(java.nio.charset.StandardCharsets.UTF_8),
                                    java.nio.charset.StandardCharsets.ISO_8859_1) + "\"")
                    .set(HttpHeaderNames.CONTENT_LENGTH, content.length);
            ctx.writeAndFlush(response).addListener(ChannelFutureListener.CLOSE);
        } catch (Exception e) {
            sendError(ctx, HttpResponseStatus.INTERNAL_SERVER_ERROR, "文件下载失败: " + e.getMessage());
        }
    }

    public static void sendError(ChannelHandlerContext ctx, HttpResponseStatus status, String message) {
        String safeMsg = message != null ? message : "内部服务器错误";
        String json = "{\"error\":\"" + escapeJson(safeMsg) + "\"}";
        sendJson(ctx, status.code(), json);
    }

    // ============================================================
    // Request helpers
    // ============================================================

    public static String sanitizePath(String uri) {
        int queryIdx = uri.indexOf('?');
        return queryIdx >= 0 ? uri.substring(0, queryIdx) : uri;
    }

    public static String getQueryParam(FullHttpRequest request, String key) {
        String uri = request.uri();
        int queryIdx = uri.indexOf('?');
        if (queryIdx < 0) return null;

        String query = uri.substring(queryIdx + 1);
        for (String part : query.split("&")) {
            String[] kv = part.split("=", 2);
            if (kv.length == 2 && kv[0].equals(key)) {
                return urlDecode(kv[1]);
            }
        }
        return null;
    }

    public static String getFormParam(FullHttpRequest request, String key) {
        String contentType = request.headers().get(HttpHeaderNames.CONTENT_TYPE);
        if (contentType == null || !contentType.contains("application/x-www-form-urlencoded")) {
            return null;
        }

        String body = request.content().toString(CharsetUtil.UTF_8);
        for (String part : body.split("&")) {
            String[] kv = part.split("=", 2);
            if (kv.length == 2 && kv[0].equals(key)) {
                return urlDecode(kv[1]);
            }
        }
        return null;
    }

    public static String getParam(FullHttpRequest request, String key) {
        // Check path params first (from Router)
        Map<String, String> pathParams = PATH_PARAMS.get();
        if (pathParams != null && pathParams.containsKey(key)) {
            return pathParams.get(key);
        }
        // Then form
        String val = getFormParam(request, key);
        if (val != null) return val;
        // Then query
        return getQueryParam(request, key);
    }

    public static String getPathParam(String key) {
        Map<String, String> pathParams = PATH_PARAMS.get();
        return pathParams != null ? pathParams.get(key) : null;
    }

    private static String escapeJson(String s) {
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

    private static String urlDecode(String s) {
        try {
            return java.net.URLDecoder.decode(s, CharsetUtil.UTF_8);
        } catch (Exception e) {
            return s;
        }
    }
}