package com.rd.robot.web.interceptor;

import com.rd.robot.model.User;
import com.rd.robot.repository.MetaStore;
import io.netty.channel.ChannelHandlerContext;
import io.netty.handler.codec.http.FullHttpRequest;
import io.netty.handler.codec.http.HttpHeaderNames;
import io.netty.util.CharsetUtil;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Set;

/**
 * Interceptor that logs API calls for API token users.
 * Records request body, response body, status code, and error messages.
 */
public class ApiCallLogInterceptor {

    private static final Logger log = LoggerFactory.getLogger(ApiCallLogInterceptor.class);

    // Sensitive paths: request body should not be logged
    private static final Set<String> SENSITIVE_PATHS = Set.of(
            "/api/login",
            "/api/user/password",
            "/api/users"
    );

    private final MetaStore metaStore;

    public ApiCallLogInterceptor(MetaStore metaStore) {
        this.metaStore = metaStore;
    }

    /**
     * Log an API call. Call this after the response is sent.
     *
     * @param ctx        channel context
     * @param request    the HTTP request
     * @param user       the authenticated user (may be null)
     * @param statusCode HTTP status code
     * @param responseBody the response body
     */
    public void logCall(ChannelHandlerContext ctx, FullHttpRequest request,
                        User user, int statusCode, String responseBody) {
        if (user == null) return;

        // Only log for API token users (role API)
        if (user.getRole() != User.ROLE_API) return;

        String authHeader = request.headers().get(HttpHeaderNames.AUTHORIZATION);
        if (authHeader == null || !authHeader.startsWith("Bearer ")) return;

        String path = request.uri();
        int queryIdx = path.indexOf('?');
        if (queryIdx >= 0) path = path.substring(0, queryIdx);

        String reqBody;
        // Redact sensitive paths
        if (SENSITIVE_PATHS.contains(path)) {
            reqBody = "[敏感数据已脱敏]";
        } else {
            reqBody = request.content().toString(CharsetUtil.UTF_8);
            if (reqBody.length() > 1000) reqBody = reqBody.substring(0, 1000) + "...";
        }

        String respBody = responseBody;
        if (respBody.length() > 1000) respBody = respBody.substring(0, 1000) + "...";

        String errMsg = "";
        if (statusCode >= 400) errMsg = respBody;

        metaStore.saveApiCallLog(user.getUserName(), path, request.method().name(),
                reqBody, respBody, statusCode, errMsg);
    }
}