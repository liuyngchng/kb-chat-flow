package session

import (
	"time"

	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"
)

const (
	MaxHistoryRounds = 5 // 最多保留 5 轮（10 条消息）
	MaxMessages      = MaxHistoryRounds * 2
	SessionTimeout   = 30 * time.Minute
	CleanupInterval  = 10 * time.Minute
	PersistLoadLimit = 20 // 从 DB 加载时最多取多少条
)

// Manager 会话管理器，委托给 SessionStore 实现。
// 单例模式 → MemoryStore（进程内存 + 异步 DB 落盘）。
// 集群模式 → RedisStore（Redis 存储 + 自动过期）。
type Manager struct {
	store SessionStore
}

// NewManager 创建会话管理器（单例模式）。
func NewManager(db store.MetaStore) *Manager {
	return &Manager{
		store: NewMemoryStore(db),
	}
}

// NewManagerWithStore 创建会话管理器（指定底层存储实现）。
func NewManagerWithStore(s SessionStore) *Manager {
	return &Manager{store: s}
}

// GetHistory 获取会话历史
func (m *Manager) GetHistory(uid string) []model.ChatMessage {
	return m.store.GetHistory(uid)
}

// AddMessage 添加消息到会话历史
func (m *Manager) AddMessage(uid, role, content string) {
	m.store.AddMessage(uid, role, content)
}

// Clear 清空会话历史
func (m *Manager) Clear(uid string) {
	m.store.Clear(uid)
}

// Stop 停止后台任务
func (m *Manager) Stop() {
	m.store.Stop()
}
