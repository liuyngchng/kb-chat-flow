package com.rd.robot.security;

import com.rd.robot.model.User;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.InvalidKeyException;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.util.Base64;
import java.util.HexFormat;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * HMAC-SHA256 token authentication.
 * Token format: base64(user_name|role|expiry_timestamp|hmac_signature)
 *
 * 单例模式下使用默认密钥，集群模式下可通过 initSecret() 设置统一密钥。
 */
public class TokenProvider {

    private static final Logger log = LoggerFactory.getLogger(TokenProvider.class);
    private static final byte[] DEFAULT_SECRET = "kb-chat-flow_secret_2026".getBytes(StandardCharsets.UTF_8);
    private static final long TOKEN_TTL_SECONDS = 2 * 3600; // 2 hours
    private static final long API_TOKEN_TTL_SECONDS = 2 * 3600; // 2 hours

    /** Token blacklist: signature -> expiry time */
    private static final Map<String, Instant> TOKEN_BLACKLIST = new ConcurrentHashMap<>();
    private static final Object CLEANUP_LOCK = new Object();
    private static Thread cleanupThread = null;

    /** 运行时密钥（集群模式下从配置注入，单例模式为空则使用默认值） */
    private static byte[] secret = null;

    private TokenProvider() {}

    /**
     * 初始化 token 签名密钥（集群模式下在启动时调用一次）。
     * 不调用则使用默认密钥。
     */
    public static void initSecret(String tokenSecret) {
        if (tokenSecret != null && !tokenSecret.isEmpty()) {
            secret = tokenSecret.getBytes(StandardCharsets.UTF_8);
        }
    }

    private static byte[] getSecret() {
        return secret != null ? secret : DEFAULT_SECRET;
    }

    /**
     * Generate an HMAC-signed token.
     */
    public static String generateToken(String userName, int role, Instant expiry) {
        long expiryUnix = expiry.getEpochSecond();
        String payload = userName + "|" + role + "|" + expiryUnix;

        // HMAC-SHA256, full hex signature
        String sig = hmacSha256(payload);
        String full = payload + "|" + sig;

        return Base64.getUrlEncoder().withoutPadding().encodeToString(full.getBytes(StandardCharsets.UTF_8));
    }

    /**
     * Generate a token with default TTL (2 hours).
     */
    public static String generateToken(String userName, int role) {
        return generateToken(userName, role, Instant.now().plusSeconds(TOKEN_TTL_SECONDS));
    }

    /**
     * Generate an API token with default TTL.
     */
    public static String generateApiToken(String userName, int role) {
        return generateToken(userName, role, Instant.now().plusSeconds(API_TOKEN_TTL_SECONDS));
    }

    /**
     * Parse and validate a token. Returns null if invalid, expired, or blacklisted.
     */
    public static User parseToken(String tokenStr) {
        if (tokenStr == null || tokenStr.isEmpty()) return null;

        try {
            byte[] data = Base64.getUrlDecoder().decode(tokenStr);
            String decoded = new String(data, StandardCharsets.UTF_8);

            String[] parts = decoded.split("\\|", 4);
            if (parts.length != 4) return null;

            String userName = parts[0];
            int role = Integer.parseInt(parts[1]);
            long expiryUnix = Long.parseLong(parts[2]);
            String sig = parts[3];

            // Check if token is blacklisted (logged out)
            if (TOKEN_BLACKLIST.containsKey(sig)) return null;

            // Check expiry
            if (Instant.now().getEpochSecond() > expiryUnix) return null;

            // Verify signature
            String payload = userName + "|" + role + "|" + expiryUnix;
            String expectedSig = hmacSha256(payload);

            if (!sig.equals(expectedSig)) return null;

            User user = new User();
            user.setUserName(userName);
            user.setRole(role);
            return user;

        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Blacklist a token after logout so it cannot be reused.
     *
     * @param tokenStr the raw token string
     */
    public static void blacklistToken(String tokenStr) {
        if (tokenStr == null || tokenStr.isEmpty()) return;
        try {
            byte[] data = Base64.getUrlDecoder().decode(tokenStr);
            String decoded = new String(data, StandardCharsets.UTF_8);
            String[] parts = decoded.split("\\|", 4);
            if (parts.length == 4) {
                long expiryUnix = Long.parseLong(parts[2]);
                TOKEN_BLACKLIST.put(parts[3], Instant.ofEpochSecond(expiryUnix));
                ensureCleanupStarted();
            }
        } catch (Exception ignored) {
            // Invalid token, nothing to blacklist
        }
    }

    /**
     * Start a background thread to clean up expired blacklist entries.
     */
    private static void ensureCleanupStarted() {
        synchronized (CLEANUP_LOCK) {
            if (cleanupThread == null || !cleanupThread.isAlive()) {
                cleanupThread = new Thread(() -> {
                    while (!Thread.currentThread().isInterrupted()) {
                        try {
                            Thread.sleep(10 * 60 * 1000); // every 10 minutes
                        } catch (InterruptedException e) {
                            break;
                        }
                        Instant now = Instant.now();
                        TOKEN_BLACKLIST.entrySet().removeIf(e -> now.isAfter(e.getValue()));
                    }
                }, "token-blacklist-cleanup");
                cleanupThread.setDaemon(true);
                cleanupThread.start();
            }
        }
    }

    /**
     * Extract token from Cookie header only (frontend auth).
     * Looks for "auth_token" in the Cookie header value.
     */
    public static String extractTokenFromCookie(String cookieHeader) {
        if (cookieHeader == null || cookieHeader.isEmpty()) return null;
        return extractCookieValue(cookieHeader, "auth_token");
    }

    /**
     * Extract token from Authorization: Bearer header only (open_api auth).
     */
    public static String extractTokenFromBearer(String authHeader) {
        if (authHeader != null && authHeader.startsWith("Bearer ")) {
            return authHeader.substring(7);
        }
        return null;
    }

    /**
     * Extract the token string from an Authorization header, Cookie header, or URL parameter.
     * Priority: Cookie "auth_token" > URL param "t" > Authorization: Bearer header
     * @deprecated 新代码应使用 extractTokenFromCookie() 或 extractTokenFromBearer()
     */
    @Deprecated
    public static String extractToken(String authHeader, String cookieHeader, String queryParam) {
        // Cookie first (browser users)
        String tokenFromCookie = extractTokenFromCookie(cookieHeader);
        if (tokenFromCookie != null && !tokenFromCookie.isEmpty()) {
            return tokenFromCookie;
        }
        // URL param
        if (queryParam != null && !queryParam.isEmpty()) {
            return queryParam;
        }
        // Authorization header
        return extractTokenFromBearer(authHeader);
    }

    /**
     * Extract the token string from an Authorization header or URL parameter (backward-compatible).
     * @deprecated 新代码应使用 extractTokenFromCookie() 或 extractTokenFromBearer()
     */
    @Deprecated
    public static String extractToken(String authHeader, String queryParam) {
        return extractToken(authHeader, null, queryParam);
    }

    private static String extractCookieValue(String cookieHeader, String cookieName) {
        if (cookieHeader == null || cookieHeader.isEmpty()) return null;
        String[] cookies = cookieHeader.split(";");
        for (String cookie : cookies) {
            String[] kv = cookie.trim().split("=", 2);
            if (kv.length == 2 && kv[0].equals(cookieName)) {
                return kv[1];
            }
        }
        return null;
    }

    private static String hmacSha256(String data) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            SecretKeySpec keySpec = new SecretKeySpec(getSecret(), "HmacSHA256");
            mac.init(keySpec);
            byte[] sigBytes = mac.doFinal(data.getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(sigBytes);
        } catch (NoSuchAlgorithmException | InvalidKeyException e) {
            throw new RuntimeException("HMAC-SHA256 计算失败", e);
        }
    }
}