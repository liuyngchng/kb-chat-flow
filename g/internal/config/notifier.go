package config

import (
	"context"
	"log/slog"
	"time"

	redisclient "kb-chat-flow/internal/redis"
)

const (
	configChangeChannel = "kb:config:change"
)

// ChangeNotifier 配置变更通知接口。
// 单例模式：NoopNotifier（无需同步）。
// 集群模式：RedisNotifier（Pub/Sub 广播，其他节点收到后重新加载）。
type ChangeNotifier interface {
	// NotifyChange 通知所有节点配置已变更
	NotifyChange() error

	// SubscribeChanges 返回一个 channel，配置变更时收到信号
	SubscribeChanges() <-chan struct{}

	// Stop 停止订阅
	Stop()
}

// NoopNotifier 单例模式：空操作。
type NoopNotifier struct{}

// NotifyChange 空操作
func (n *NoopNotifier) NotifyChange() error { return nil }

// SubscribeChanges 返回 nil（单例模式不需要监听）
func (n *NoopNotifier) SubscribeChanges() <-chan struct{} { return nil }

// Stop 空操作
func (n *NoopNotifier) Stop() {}

// RedisNotifier 集群模式：Redis Pub/Sub 实现。
type RedisNotifier struct {
	client *redisclient.Client
	stopCh chan struct{}
}

// NewRedisNotifier 创建 Redis 配置通知器
func NewRedisNotifier(client *redisclient.Client) *RedisNotifier {
	slog.Info("config_notifier_using_redis_pubsub")
	return &RedisNotifier{
		client: client,
		stopCh: make(chan struct{}),
	}
}

// NotifyChange 发布配置变更消息
func (n *RedisNotifier) NotifyChange() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return n.client.Publish(ctx, configChangeChannel, "reload")
}

// SubscribeChanges 订阅配置变更，返回信号 channel。
// 在后台 goroutine 中持续监听，收到消息后向返回的 channel 发送信号。
func (n *RedisNotifier) SubscribeChanges() <-chan struct{} {
	ch := make(chan struct{}, 1)

	go func() {
		defer close(ch)

		for {
			ctx := context.Background()
			msgCh, pubsub, err := n.client.Subscribe(ctx, configChangeChannel)
			if err != nil {
				slog.Warn("config_notifier_subscribe_failed_retrying", "error", err)
				select {
				case <-n.stopCh:
					return
				case <-time.After(10 * time.Second):
					continue
				}
			}

			slog.Info("config_notifier_listener_started")

		innerLoop:
			for {
				select {
				case <-n.stopCh:
					if err := pubsub.Close(); err != nil {
						slog.Warn("config_notifier_pubsub_close_error", "error", err)
					}
					return
				case msg, ok := <-msgCh:
					if !ok {
						// channel closed, reconnect
						slog.Warn("config_notifier_subscription_lost")
						break innerLoop
					}
					if msg == "reload" {
						select {
						case ch <- struct{}{}:
						default:
							// channel full, skip (已有待处理信号)
						}
					}
				}
			}
		}
	}()

	return ch
}

// Stop 停止订阅
func (n *RedisNotifier) Stop() {
	close(n.stopCh)
}
