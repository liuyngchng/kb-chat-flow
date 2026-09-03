package session

import (
	"log/slog"
	"sync"
	"time"

	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"
)

// memorySessionEntry 每个会话独立锁
type memorySessionEntry struct {
	mu sync.Mutex
	h  *model.ChatHistory
}

// MemoryStore 单例模式会话存储：进程内存 + 异步 DB 落盘。
type MemoryStore struct {
	sessions sync.Map // key: uid → *memorySessionEntry
	db       store.MetaStore
	stopCh   chan struct{}
}

// NewMemoryStore 创建内存会话存储，启动清理和持久化后台任务。
func NewMemoryStore(db store.MetaStore) *MemoryStore {
	s := &MemoryStore{
		db:     db,
		stopCh: make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// Stop 停止后台任务
func (s *MemoryStore) Stop() {
	close(s.stopCh)
}

// GetHistory 获取会话历史（优先内存，fallback DB）
func (s *MemoryStore) GetHistory(uid string) []model.ChatMessage {
	v, ok := s.sessions.Load(uid)
	if ok {
		entry := v.(*memorySessionEntry)
		entry.mu.Lock()
		result := make([]model.ChatMessage, len(entry.h.Messages))
		copy(result, entry.h.Messages)
		entry.mu.Unlock()
		return result
	}

	// 内存没有，尝试从 DB 加载
	if s.db != nil {
		msgs, err := s.db.GetChatMessages(uid, PersistLoadLimit)
		if err != nil {
			slog.Warn("session_memory_load_history_failed", "uid", uid, "error", err)
			return nil
		}
		if len(msgs) > 0 {
			entry := &memorySessionEntry{
				h: &model.ChatHistory{
					UID:       uid,
					Messages:  msgs,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
			}
			s.sessions.Store(uid, entry)
			slog.Info("session_memory_restore_history", "uid", uid, "count", len(msgs))
			return msgs
		}
	}

	return nil
}

// AddMessage 添加消息到会话历史（异步持久化到 DB）
func (s *MemoryStore) AddMessage(uid, role, content string) {
	entry := &memorySessionEntry{
		h: &model.ChatHistory{
			UID:       uid,
			Messages:  make([]model.ChatMessage, 0),
			CreatedAt: time.Now(),
		},
	}

	actual, loaded := s.sessions.LoadOrStore(uid, entry)
	if loaded {
		entry = actual.(*memorySessionEntry)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.h.Messages = append(entry.h.Messages, model.ChatMessage{
		Role:    role,
		Content: content,
	})
	entry.h.UpdatedAt = time.Now()

	if len(entry.h.Messages) > MaxMessages {
		entry.h.Messages = entry.h.Messages[len(entry.h.Messages)-MaxMessages:]
	}

	// 异步持久化到 DB
	if s.db != nil {
		go func() {
			if err := s.db.SaveChatMessage(uid, role, content); err != nil {
				slog.Warn("session_memory_persist_message_failed", "uid", uid, "error", err)
			}
		}()
	}
}

// Clear 清空会话历史（内存 + DB）
func (s *MemoryStore) Clear(uid string) {
	s.sessions.Delete(uid)
	if s.db != nil {
		if err := s.db.ClearChatMessages(uid); err != nil {
			slog.Warn("session_memory_clear_history_failed", "uid", uid, "error", err)
		}
	}
}

// cleanupLoop 定期清理过期会话
func (s *MemoryStore) cleanupLoop() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			s.sessions.Range(func(key, value any) bool {
				entry := value.(*memorySessionEntry)
				entry.mu.Lock()
				if now.Sub(entry.h.UpdatedAt) > SessionTimeout {
					entry.mu.Unlock()
					s.sessions.Delete(key)
				} else {
					entry.mu.Unlock()
				}
				return true
			})
		}
	}
}
