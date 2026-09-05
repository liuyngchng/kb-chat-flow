package redis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kb-chat-flow/internal/model"

	goredis "github.com/redis/go-redis/v9"
)

// Client Redis 客户端封装
type Client struct {
	rdb *goredis.Client
}

var defaultTimeout = 5 * time.Second

// New 创建 Redis 客户端。
// 集群模式下 cfg.Redis.Addr 为空时返回错误。
func New(cfg *model.Config) (*Client, error) {
	if cfg.Redis.Addr == "" {
		return nil, fmt.Errorf("redis addr is empty (required in cluster mode)")
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	slog.Info("redis_connected", "addr", cfg.Redis.Addr, "db", cfg.Redis.DB)
	return &Client{rdb: rdb}, nil
}

// NewOptional 创建 Redis 客户端（不强制要求连接成功）。
// 用于单例模式下可能不存在 Redis 的场景，返回 nil。
func NewOptional(cfg *model.Config) *Client {
	if cfg.Redis.Addr == "" {
		return nil
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis_not_available_fallback", "error", err)
		return nil
	}

	slog.Info("redis_connected_optional", "addr", cfg.Redis.Addr, "db", cfg.Redis.DB)
	return &Client{rdb: rdb}
}

// RDB 返回底层 go-redis 客户端（供高级操作使用）
func (c *Client) RDB() *goredis.Client {
	return c.rdb
}

// Close 关闭 Redis 连接
func (c *Client) Close() error {
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// ============================================================
// 基础操作封装
// ============================================================

// Set 设置键值（带过期时间）
func (c *Client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.rdb.Set(ctx, key, value, expiration).Err()
}

// Get 获取键值
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

// Del 删除键
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// HSet 设置 Hash 字段
func (c *Client) HSet(ctx context.Context, key string, values ...interface{}) error {
	return c.rdb.HSet(ctx, key, values...).Err()
}

// HGetAll 获取 Hash 所有字段
func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, key).Result()
}

// HDel 删除 Hash 字段
func (c *Client) HDel(ctx context.Context, key string, fields ...string) error {
	return c.rdb.HDel(ctx, key, fields...).Err()
}

// Exists 检查键是否存在
func (c *Client) Exists(ctx context.Context, keys ...string) (int64, error) {
	return c.rdb.Exists(ctx, keys...).Result()
}

// SetNX 仅在键不存在时设置（用于分布式锁）
func (c *Client) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, expiration).Result()
}

// LPush 左侧推入列表
func (c *Client) LPush(ctx context.Context, key string, values ...interface{}) error {
	return c.rdb.LPush(ctx, key, values...).Err()
}

// RPop 右侧弹出列表
func (c *Client) RPop(ctx context.Context, key string) (string, error) {
	return c.rdb.RPop(ctx, key).Result()
}

// Publish 发布消息到频道
func (c *Client) Publish(ctx context.Context, channel string, message interface{}) error {
	return c.rdb.Publish(ctx, channel, message).Err()
}

// LRange 获取列表范围内的元素
func (c *Client) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.rdb.LRange(ctx, key, start, stop).Result()
}

// LTrim 裁剪列表，只保留指定范围内的元素
func (c *Client) LTrim(ctx context.Context, key string, start, stop int64) error {
	return c.rdb.LTrim(ctx, key, start, stop).Err()
}

// Expire 设置键过期时间
func (c *Client) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return c.rdb.Expire(ctx, key, expiration).Err()
}

// Subscribe 订阅频道，返回消息通道
func (c *Client) Subscribe(ctx context.Context, channels ...string) (<-chan string, *goredis.PubSub, error) {
	pubsub := c.rdb.Subscribe(ctx, channels...)

	// 等待订阅确认
	if _, err := pubsub.Receive(ctx); err != nil {
		return nil, nil, fmt.Errorf("subscribe failed: %w", err)
	}

	msgCh := make(chan string, 10)
	go func() {
		defer close(msgCh)
		for msg := range pubsub.Channel() {
			msgCh <- msg.Payload
		}
	}()

	return msgCh, pubsub, nil
}
