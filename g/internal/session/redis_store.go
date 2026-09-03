package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	redisclient "kb-chat-flow/internal/redis"

	"kb-chat-flow/internal/model"
)

const (
	redisSessionPrefix = "session:"
	redisSessionTTL    = SessionTimeout + 10*time.Minute // 比内存超时多一点
)

// RedisStore 集群模式会话存储：Redis List + 自动过期。
type RedisStore struct {
	client *redisclient.Client
}

// NewRedisStore 创建 Redis 会话存储
func NewRedisStore(client *redisclient.Client) *RedisStore {
	slog.Info("session_redis_store_init")
	return &RedisStore{client: client}
}

// GetHistory 从 Redis 获取会话历史
func (s *RedisStore) GetHistory(uid string) []model.ChatMessage {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := redisSessionPrefix + uid
	vals, err := s.client.LRange(ctx, key, 0, int64(MaxMessages)-1)
	if err != nil {
		slog.Warn("session_redis_get_history_failed", "uid", uid, "error", err)
		return nil
	}

	if len(vals) == 0 {
		return nil
	}

	// Redis List 是 LPUSH 的，最新的在前面，需要反转
	messages := make([]model.ChatMessage, 0, len(vals))
	for i := len(vals) - 1; i >= 0; i-- {
		var msg model.ChatMessage
		if err := json.Unmarshal([]byte(vals[i]), &msg); err != nil {
			slog.Warn("session_redis_unmarshal_message_failed", "uid", uid, "error", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages
}

// AddMessage 追加消息到 Redis，通过 LPUSH + LTRIM + EXPIRE 维护
func (s *RedisStore) AddMessage(uid, role, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	msg := model.ChatMessage{Role: role, Content: content}
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Warn("session_redis_marshal_message_failed", "uid", uid, "error", err)
		return
	}

	key := redisSessionPrefix + uid

	// LPUSH: 新消息放到最前面
	if err := s.client.LPush(ctx, key, string(data)); err != nil {
		slog.Warn("session_redis_lpush_failed", "uid", uid, "error", err)
		return
	}

	// LTRIM: 只保留最近 MaxMessages 条
	if err := s.client.LTrim(ctx, key, 0, int64(MaxMessages)-1); err != nil {
		slog.Warn("session_redis_ltrim_failed", "uid", uid, "error", err)
	}

	// EXPIRE: 每次写入刷新过期时间
	if err := s.client.Expire(ctx, key, redisSessionTTL); err != nil {
		slog.Warn("session_redis_expire_failed", "uid", uid, "error", err)
	}
}

// Clear 清空会话历史
func (s *RedisStore) Clear(uid string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := redisSessionPrefix + uid
	if err := s.client.Del(ctx, key); err != nil {
		slog.Warn("session_redis_del_failed", "uid", uid, "error", err)
	}
}

// Stop Redis 清理（关闭连接）
func (s *RedisStore) Stop() {
	if s.client != nil {
		if err := s.client.Close(); err != nil {
			slog.Warn("session_redis_close_failed", "error", err)
		}
	}
}

// FormatHistory 格式化历史消息为字符串（工具函数）
func FormatHistory(messages []model.ChatMessage) string {
	if len(messages) == 0 {
		return "（无历史对话）"
	}

	var result string
	for _, msg := range messages {
		if msg.Role == "user" {
			result += "用户：" + msg.Content + "\n"
		} else {
			result += fmt.Sprintf("机器人：%s\n", msg.Content)
		}
	}
	return result
}
