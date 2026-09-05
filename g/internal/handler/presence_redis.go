package handler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	redisclient "kb-chat-flow/internal/redis"

	"kb-chat-flow/internal/store"
)

const (
	presenceKeyPrefix = "presence:"
	presenceTTL       = 3 * time.Hour // 在线状态最长保留 3 小时（登录 token 有效期 2h，留余量）
)

// RedisPresence 集群模式在线状态存储：Redis 独立 key + TTL。
// 每个在线座席一个 key: presence:{userName} → loginTime Unix 时间戳。
type RedisPresence struct {
	client *redisclient.Client
	db     store.MetaStore
}

// NewRedisPresence 创建 Redis 在线状态存储
func NewRedisPresence(client *redisclient.Client, db store.MetaStore) *RedisPresence {
	slog.Info("presence_redis_store_init")
	return &RedisPresence{client: client, db: db}
}

// SetPresence 记录座席上线
func (p *RedisPresence) SetPresence(userName string, loginTime time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := presenceKeyPrefix + userName
	val := strconv.FormatInt(loginTime.Unix(), 10)

	if err := p.client.Set(ctx, key, val, presenceTTL); err != nil {
		slog.Warn("presence_redis_set_failed", "userName", userName, "error", err)
	}
}

// RemovePresence 移除座席在线状态
func (p *RedisPresence) RemovePresence(userName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := p.client.Del(ctx, presenceKeyPrefix+userName); err != nil {
		slog.Warn("presence_redis_del_failed", "userName", userName, "error", err)
	}
}

// GetOnlineAgents 获取所有在线座席列表（通过 SCAN 遍历 presence:* key）
func (p *RedisPresence) GetOnlineAgents() []OnlineAgent {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb := p.client.RDB()
	var agents []OnlineAgent

	// SCAN 遍历所有 presence:* 的 key
	var cursor uint64
	for {
		keys, nextCursor, err := rdb.Scan(ctx, cursor, presenceKeyPrefix+"*", 50).Result()
		if err != nil {
			slog.Warn("presence_redis_scan_failed", "error", err)
			break
		}

		for _, key := range keys {
			userName := key[len(presenceKeyPrefix):]

			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				continue
			}

			unixTime, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				continue
			}

			loginTime := time.Unix(unixTime, 0)

			note := ""
			if p.db != nil {
				user, err := p.db.GetUserByName(userName)
				if err == nil && user != nil {
					note = user.Note
				}
			}

			agents = append(agents, OnlineAgent{
				UserName:  userName,
				LoginTime: loginTime.Format("2006-01-02 15:04:05"),
				Note:      note,
			})
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return agents
}

// HasPresence 检查座席是否在线
func (p *RedisPresence) HasPresence(userName string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	n, err := p.client.Exists(ctx, presenceKeyPrefix+userName)
	if err != nil {
		slog.Warn("presence_redis_exists_failed", "userName", userName, "error", err)
		return false
	}
	return n > 0
}
