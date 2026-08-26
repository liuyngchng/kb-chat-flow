-- ============================================================
-- go_to_chat 数据库表结构 (SQLite 版本)
-- ============================================================

-- 知识库元数据表
CREATE TABLE IF NOT EXISTS vdb_info (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    uid TEXT NOT NULL DEFAULT '',
    is_public INTEGER NOT NULL DEFAULT 0,
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 知识库文件信息表
CREATE TABLE IF NOT EXISTS vdb_file_info (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    uid TEXT NOT NULL DEFAULT '',
    vdb_id INTEGER NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    file_path TEXT NOT NULL DEFAULT '',
    percent REAL NOT NULL DEFAULT 0,
    process_info TEXT NOT NULL DEFAULT '',
    file_md5 TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 提示词模板表
CREATE TABLE IF NOT EXISTS prompt_template (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL,
    uid INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 系统配置表 (key-value 存储)
CREATE TABLE IF NOT EXISTS sys_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    config_key TEXT NOT NULL UNIQUE,
    config_value TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 用户表
CREATE TABLE IF NOT EXISTS users (
    uid INTEGER PRIMARY KEY AUTOINCREMENT,
    user_name TEXT NOT NULL UNIQUE,
    user_pwd TEXT NOT NULL DEFAULT '',
    role INTEGER NOT NULL DEFAULT 0,
    note TEXT NOT NULL DEFAULT '',
    pwd_expires_at TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- API Token 表
CREATE TABLE IF NOT EXISTS api_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_name TEXT NOT NULL,
    token_preview TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- API 调用日志表
CREATE TABLE IF NOT EXISTS api_call_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_name TEXT NOT NULL,
    api_path TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    request_body TEXT NOT NULL DEFAULT '',
    response_body TEXT NOT NULL DEFAULT '',
    status_code INTEGER NOT NULL DEFAULT 200,
    error_msg TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 智能体定义表
CREATE TABLE IF NOT EXISTS agent_def (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    temperature REAL,
    top_p REAL,
    max_tokens INTEGER,
    vdb_ids TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 工作流定义表
CREATE TABLE IF NOT EXISTS workflow_def (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    classifier TEXT NOT NULL DEFAULT '',
    nodes TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FAQ 条目表
CREATE TABLE IF NOT EXISTS faq_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    answer TEXT NOT NULL,
    source_file TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FAQ 问题表（一对多，每个问法独立向量化）
CREATE TABLE IF NOT EXISTS faq_questions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL,
    question TEXT NOT NULL,
    embedding TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (entry_id) REFERENCES faq_entries(id) ON DELETE CASCADE
);

-- ============================================================
-- 索引
-- ============================================================
CREATE INDEX IF NOT EXISTS idx_vdb_info_uid ON vdb_info(uid);
CREATE INDEX IF NOT EXISTS idx_vdb_file_info_vdb_id ON vdb_file_info(vdb_id);
CREATE INDEX IF NOT EXISTS idx_sys_config_key ON sys_config(config_key);
CREATE INDEX IF NOT EXISTS idx_users_name ON users(user_name);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_name);
CREATE INDEX IF NOT EXISTS idx_api_call_log_user ON api_call_log(user_name);
CREATE INDEX IF NOT EXISTS idx_faq_questions_entry ON faq_questions(entry_id);
