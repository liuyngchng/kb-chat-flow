package com.rd.robot.session;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.ChatMessage;
import com.rd.robot.redis.RedisClient;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;

/**
 * Redis 会话存储（集群模式）。
 * 使用 Redis List 存储消息历史，TTL 自动过期。
 */
public class RedisSessionStore implements SessionStore {

    private static final Logger log = LoggerFactory.getLogger(RedisSessionStore.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final int MAX_MESSAGES = 10; // 5 rounds * 2
    private static final long TTL_SECONDS = 30 * 60; // 30 min

    private final RedisClient redisClient;

    public RedisSessionStore(RedisClient redisClient) {
        this.redisClient = redisClient;
    }

    private String key(String uid) {
        return "session:" + uid;
    }

    @Override
    public List<ChatMessage> getHistory(String uid) {
        String json = redisClient.get(key(uid));
        if (json == null || json.isEmpty()) {
            return Collections.emptyList();
        }
        try {
            ChatMessage[] msgs = MAPPER.readValue(json, ChatMessage[].class);
            return Arrays.asList(msgs);
        } catch (Exception e) {
            log.warn("session_redis_parse_history_failed uid={}", uid, e);
            return Collections.emptyList();
        }
    }

    @Override
    public void addMessage(String uid, String role, String content) {
        List<ChatMessage> history = new ArrayList<>(getHistory(uid));
        history.add(new ChatMessage(role, content));

        // 保留最近的消息
        if (history.size() > MAX_MESSAGES) {
            history = history.subList(history.size() - MAX_MESSAGES, history.size());
        }

        try {
            String json = MAPPER.writeValueAsString(history);
            redisClient.setex(key(uid), TTL_SECONDS, json);
        } catch (JsonProcessingException e) {
            log.error("session_redis_serialize_history_failed uid={}", uid, e);
        }
    }

    @Override
    public void clear(String uid) {
        redisClient.del(key(uid));
    }

    @Override
    public void stop() {
        // nothing to do
    }
}