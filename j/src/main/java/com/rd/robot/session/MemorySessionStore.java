package com.rd.robot.session;

import com.rd.robot.model.ChatHistory;
import com.rd.robot.model.ChatMessage;
import com.rd.robot.repository.MetaStore;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * 进程内存会话存储（单例模式）。
 * 内存为主，异步落盘到 DB。
 */
public class MemorySessionStore implements SessionStore {

    private static final Logger log = LoggerFactory.getLogger(MemorySessionStore.class);
    private static final int MAX_HISTORY_ROUNDS = 5;
    private static final long SESSION_TIMEOUT_MS = 30 * 60 * 1000L;
    private static final int PERSIST_LOAD_LIMIT = 20;

    private final ConcurrentHashMap<String, ChatHistory> sessions = new ConcurrentHashMap<>();
    private final ScheduledExecutorService cleanupScheduler;
    private final MetaStore store;

    public MemorySessionStore(MetaStore store) {
        this.store = store;
        cleanupScheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "session-cleanup");
            t.setDaemon(true);
            return t;
        });
        cleanupScheduler.scheduleWithFixedDelay(this::cleanup, 10, 10, TimeUnit.MINUTES);
    }

    @Override
    public List<ChatMessage> getHistory(String uid) {
        ChatHistory history = sessions.get(uid);
        if (history != null) {
            synchronized (history) {
                return new ArrayList<>(history.getMessages());
            }
        }

        // Fallback: load from DB
        if (store != null) {
            try {
                List<ChatMessage> msgs = store.getChatMessages(uid, PERSIST_LOAD_LIMIT);
                if (msgs != null && !msgs.isEmpty()) {
                    ChatHistory loaded = new ChatHistory(uid);
                    loaded.setMessages(new ArrayList<>(msgs));
                    loaded.setUpdatedAt(System.currentTimeMillis());
                    sessions.put(uid, loaded);
                    log.info("session_memory_restore_history uid={} count={}", uid, msgs.size());
                    return msgs;
                }
            } catch (Exception e) {
                log.warn("session_memory_load_history_failed uid={} error={}", uid, e.getMessage());
            }
        }

        return Collections.emptyList();
    }

    @Override
    public void addMessage(String uid, String role, String content) {
        ChatHistory history = sessions.computeIfAbsent(uid, k -> new ChatHistory(uid));

        synchronized (history) {
            history.getMessages().add(new ChatMessage(role, content));
            history.setUpdatedAt(System.currentTimeMillis());

            int maxMessages = MAX_HISTORY_ROUNDS * 2;
            if (history.getMessages().size() > maxMessages) {
                int start = history.getMessages().size() - maxMessages;
                history.setMessages(new ArrayList<>(history.getMessages().subList(start, history.getMessages().size())));
            }
        }

        // Async persist to DB
        if (store != null) {
            final String u = uid;
            new Thread(() -> {
                try {
                    store.saveChatMessage(u, role, content);
                } catch (Exception e) {
                    log.warn("session_memory_persist_message_failed uid={} error={}", u, e.getMessage());
                }
            }).start();
        }
    }

    @Override
    public void clear(String uid) {
        sessions.remove(uid);
        if (store != null) {
            try {
                store.clearChatMessages(uid);
            } catch (Exception e) {
                log.warn("session_memory_clear_history_failed uid={} error={}", uid, e.getMessage());
            }
        }
    }

    @Override
    public void stop() {
        cleanupScheduler.shutdown();
    }

    private void cleanup() {
        long now = System.currentTimeMillis();
        Iterator<Map.Entry<String, ChatHistory>> it = sessions.entrySet().iterator();
        while (it.hasNext()) {
            Map.Entry<String, ChatHistory> entry = it.next();
            ChatHistory history = entry.getValue();
            synchronized (history) {
                if (now - history.getUpdatedAt() > SESSION_TIMEOUT_MS) {
                    it.remove();
                }
            }
        }
    }
}