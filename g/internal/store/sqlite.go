package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"time"

	"kb-chat-flow/internal/model"

	_ "modernc.org/sqlite"
)

// SQLiteStore SQLite 元数据存储
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore 打开 SQLite 数据库（文件必须已存在）
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	// 检查数据库文件是否存在，不允许自动创建
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("数据库文件 %s 不存在，请从 cfg.db.template 复制", dbPath)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// WAL 模式：读不阻塞写
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("启用 WAL 失败: %w", err)
	}

	// 读多写少场景，允许多个并发读
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return store, nil
}

// Close 关闭数据库连接
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// migrate 创建表结构
func (s *SQLiteStore) migrate() error {
	schema := `
		CREATE TABLE IF NOT EXISTS vdb_info (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			uid TEXT NOT NULL DEFAULT '',
			is_public INTEGER NOT NULL DEFAULT 0,
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

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

		CREATE TABLE IF NOT EXISTS prompt_template (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			value TEXT NOT NULL,
			uid INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS sys_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			config_key TEXT NOT NULL UNIQUE,
			config_value TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

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

		CREATE TABLE IF NOT EXISTS api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_name TEXT NOT NULL,
			token_preview TEXT NOT NULL DEFAULT '',
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

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

		-- 会话历史持久化表（TODO: 后续迁移至 Redis）
		CREATE TABLE IF NOT EXISTS chat_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uid TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- 索引
		CREATE INDEX IF NOT EXISTS idx_vdb_info_uid ON vdb_info(uid);
		CREATE INDEX IF NOT EXISTS idx_vdb_file_info_vdb_id ON vdb_file_info(vdb_id);
		CREATE INDEX IF NOT EXISTS idx_sys_config_key ON sys_config(config_key);
		CREATE INDEX IF NOT EXISTS idx_users_name ON users(user_name);
		CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_name);
		CREATE INDEX IF NOT EXISTS idx_api_call_log_user ON api_call_log(user_name);
		CREATE INDEX IF NOT EXISTS idx_faq_questions_entry ON faq_questions(entry_id);
		CREATE INDEX IF NOT EXISTS idx_chat_sessions_uid ON chat_sessions(uid, created_at);
		`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}

	if err := s.seedUsers(); err != nil {
		return err
	}
	return s.seedDefaultAgent()
}

// ============================================================
// 知识库 (vdb_info) CRUD
// ============================================================

// CreateVdb 创建知识库
func (s *SQLiteStore) CreateVdb(name, uid string, isPublic bool) (int64, error) {
	pubVal := 0
	if isPublic {
		pubVal = 1
	}

	result, err := s.db.Exec(
		"INSERT INTO vdb_info (name, uid, is_public) VALUES (?, ?, ?)",
		name, uid, pubVal,
	)
	if err != nil {
		return 0, fmt.Errorf("创建知识库失败: %w", err)
	}
	return result.LastInsertId()
}

// GetVdbByID 根据 ID 获取知识库
func (s *SQLiteStore) GetVdbByID(id int64) (*model.VdbInfo, error) {
	row := s.db.QueryRow(
		"SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE id = ?", id,
	)
	return scanVdbInfo(row)
}

// GetUserVdbs 获取用户的所有知识库
func (s *SQLiteStore) GetUserVdbs(uid string) ([]model.VdbInfo, error) {
	rows, err := s.db.Query(
		"SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE uid = ? ORDER BY created_at DESC",
		uid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanVdbInfoList(rows)
}

// GetPublicVdbs 获取所有公开的知识库
func (s *SQLiteStore) GetPublicVdbs(excludeUID string) ([]model.VdbInfo, error) {
	rows, err := s.db.Query(
		"SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE is_public = 1 AND uid != ? ORDER BY created_at DESC",
		excludeUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanVdbInfoList(rows)
}

// DeleteVdb 删除知识库及其所有文件
func (s *SQLiteStore) DeleteVdb(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 先删文件记录
	if _, err := tx.Exec("DELETE FROM vdb_file_info WHERE vdb_id = ?", id); err != nil {
		return err
	}
	// 再删知识库
	if _, err := tx.Exec("DELETE FROM vdb_info WHERE id = ?", id); err != nil {
		return err
	}

	return tx.Commit()
}

// SetDefaultVdb 设置默认知识库
func (s *SQLiteStore) SetDefaultVdb(id int64, uid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 先取消该用户所有默认
	if _, err := tx.Exec("UPDATE vdb_info SET is_default = 0 WHERE uid = ?", uid); err != nil {
		return err
	}
	// 设置新的默认
	if _, err := tx.Exec("UPDATE vdb_info SET is_default = 1 WHERE id = ? AND uid = ?", id, uid); err != nil {
		return err
	}

	return tx.Commit()
}

// CheckVdbNameExists 检查知识库名称是否已存在
func (s *SQLiteStore) CheckVdbNameExists(name, uid string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM vdb_info WHERE name = ? AND uid = ?", name, uid,
	).Scan(&count)
	return count > 0, err
}

// GetDefaultVdbID 获取用户的默认知识库 ID
func (s *SQLiteStore) GetDefaultVdbID(uid string) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		"SELECT id FROM vdb_info WHERE uid = ? AND is_default = 1 LIMIT 1", uid,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// ============================================================
// 文件 (vdb_file_info) CRUD
// ============================================================

// CreateFileInfo 创建文件记录
func (s *SQLiteStore) CreateFileInfo(info *model.VdbFileInfo) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO vdb_file_info (name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		info.Name, info.UID, info.VdbID, info.TaskID, info.FilePath, info.Percent, info.ProcessInfo, info.FileMD5,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetFilesByVdbID 获取知识库下的所有文件
func (s *SQLiteStore) GetFilesByVdbID(vdbID int64) ([]model.VdbFileInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at
		 FROM vdb_file_info WHERE vdb_id = ? ORDER BY created_at DESC`, vdbID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.VdbFileInfo
	for rows.Next() {
		var f model.VdbFileInfo
		if err := rows.Scan(&f.ID, &f.Name, &f.UID, &f.VdbID, &f.TaskID,
			&f.FilePath, &f.Percent, &f.ProcessInfo, &f.FileMD5, &f.CreateTime); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// GetFileByID 根据 ID 获取文件信息
func (s *SQLiteStore) GetFileByID(id int64) (*model.VdbFileInfo, error) {
	row := s.db.QueryRow(
		`SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at
		 FROM vdb_file_info WHERE id = ?`, id,
	)
	var f model.VdbFileInfo
	err := row.Scan(&f.ID, &f.Name, &f.UID, &f.VdbID, &f.TaskID,
		&f.FilePath, &f.Percent, &f.ProcessInfo, &f.FileMD5, &f.CreateTime)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// GetUnprocessedFiles 获取未处理的文件
func (s *SQLiteStore) GetUnprocessedFiles() ([]model.VdbFileInfo, error) {
	rows, err := s.db.Query(
		`SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at
		 FROM vdb_file_info WHERE percent != 100 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.VdbFileInfo
	for rows.Next() {
		var f model.VdbFileInfo
		if err := rows.Scan(&f.ID, &f.Name, &f.UID, &f.VdbID, &f.TaskID,
			&f.FilePath, &f.Percent, &f.ProcessInfo, &f.FileMD5, &f.CreateTime); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// UpdateFileProgress 更新文件处理进度
func (s *SQLiteStore) UpdateFileProgress(id int64, percent float64, info string) error {
	_, err := s.db.Exec(
		"UPDATE vdb_file_info SET percent = ?, process_info = ? WHERE id = ?",
		percent, info, id,
	)
	return err
}

// DeleteFile 删除文件记录
func (s *SQLiteStore) DeleteFile(id int64) error {
	_, err := s.db.Exec("DELETE FROM vdb_file_info WHERE id = ?", id)
	return err
}

// CheckFileMD5Exists 检查同一知识库下的文件 MD5 是否已存在
func (s *SQLiteStore) CheckFileMD5Exists(vdbID int64, md5 string) (*model.VdbFileInfo, error) {
	row := s.db.QueryRow(
		`SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at
		 FROM vdb_file_info WHERE vdb_id = ? AND file_md5 = ?`, vdbID, md5,
	)
	var f model.VdbFileInfo
	err := row.Scan(&f.ID, &f.Name, &f.UID, &f.VdbID, &f.TaskID,
		&f.FilePath, &f.Percent, &f.ProcessInfo, &f.FileMD5, &f.CreateTime)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ============================================================
// 提示词模板
// ============================================================

// GetPrompt 根据名称获取提示词模板，不存在则返回空字符串
func (s *SQLiteStore) GetPrompt(name string) (string, error) {
	var value string
	err := s.db.QueryRow(
		"SELECT value FROM prompt_template WHERE name = ?", name,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// UpsertPrompt 插入或更新提示词模板
func (s *SQLiteStore) UpsertPrompt(name, value string, uid int) error {
	_, err := s.db.Exec(
		`INSERT INTO prompt_template (name, value, uid) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET value = excluded.value, uid = excluded.uid`,
		name, value, uid,
	)
	return err
}

// ============================================================
// 系统配置 (sys_config)
// ============================================================

// ============================================================
// 用户 (users)
// ============================================================

// GetUserByLogin 按用户名查询用户（密码验证由 handler 层用 bcrypt 完成）
func (s *SQLiteStore) GetUserByLogin(userName string) (*model.User, error) {
	row := s.db.QueryRow(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?",
		userName,
	)
	return scanUser(row)
}

// GetUserByName 按用户名查询用户
func (s *SQLiteStore) GetUserByName(userName string) (*model.User, error) {
	row := s.db.QueryRow(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?",
		userName,
	)
	return scanUser(row)
}

// scanUser 扫描 users 行，解析 pwd_expires_at（空字符串 = 无过期限制）
func scanUser(row *sql.Row) (*model.User, error) {
	var u model.User
	var expiresAt string
	err := row.Scan(&u.UID, &u.UserName, &u.UserPwd, &u.Role, &u.Note, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			u.PwdExpiresAt = t
		}
	}
	return &u, nil
}

// seedUsers 种子内置用户：
//   - admin：内置管理员，密码随机生成、启动后 2 小时内有效，登录后强制修改密码。
//     仅当 users 表中不存在 admin 时创建（此前用 count>0 判断导致模板里的 api0 使
//     admin 永远不会被插入，一旦 admin 被注册函数抢注为普通用户，系统就失去管理员）。
//   - api0：内置 API 调用用户，仅当不存在时创建。
func (s *SQLiteStore) seedUsers() error {
	if err := s.seedAdminIfMissing(); err != nil {
		return err
	}
	return s.seedAPI0IfMissing()
}

// seedAdminIfMissing 当 users 表中不存在 admin 用户时，创建内置管理员。
// 随机密码打印到控制台和日志文件；登录后 2 小时内未修改则密码过期，需重置 cfg.db。
func (s *SQLiteStore) seedAdminIfMissing() error {
	row := s.db.QueryRow("SELECT user_name FROM users WHERE user_name = ?", "admin")
	var name string
	switch err := row.Scan(&name); err {
	case nil:
		return nil // 已有 admin，直接使用
	case sql.ErrNoRows:
		// 继续创建
	default:
		return err
	}

	// 生成随机初始密码（12 位字母数字）
	adminPwd, err := randomPassword(12)
	if err != nil {
		return fmt.Errorf("生成 admin 随机密码失败: %w", err)
	}

	pwdHash, err := hashPassword(adminPwd)
	if err != nil {
		return fmt.Errorf("admin 密码哈希失败: %w", err)
	}

	// admin 密码 2 小时后过期，登录后强制修改（修改密码会清除过期时间）
	expiresAt := time.Now().Add(adminPwdExpiry).Format(time.RFC3339)
	if _, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note, pwd_expires_at) VALUES (?, ?, ?, ?, ?)",
		"admin", pwdHash, model.RoleAdmin, "内置管理员", expiresAt,
	); err != nil {
		return fmt.Errorf("种子用户 admin 插入失败: %w", err)
	}

	// 随机密码打印到控制台和日志文件
	slog.Info("store_sqlite_admin_account_created", "user_name", "admin", "initial_password", adminPwd, "expires_in", adminPwdExpiry.String())
	fmt.Printf("\n========================================\n")
	fmt.Printf("  首次运行已创建管理员账号 admin\n")
	fmt.Printf("  初始密码: %s\n", adminPwd)
	fmt.Printf("  该密码 %s 内有效，登录后需立即修改密码\n", adminPwdExpiryText)
	fmt.Printf("  若忘记初始密码，请删除 cfg.db 并从 cfg.db.template 重置后重启\n")
	fmt.Printf("========================================\n\n")

	return nil
}

// seedAPI0IfMissing 当 users 表中不存在 api0 用户时，创建内置 API 调用用户
func (s *SQLiteStore) seedAPI0IfMissing() error {
	row := s.db.QueryRow("SELECT user_name FROM users WHERE user_name = ?", "api0")
	var name string
	switch err := row.Scan(&name); err {
	case nil:
		return nil
	case sql.ErrNoRows:
		// 继续创建
	default:
		return err
	}

	apiPwdHash, err := hashPassword("api0")
	if err != nil {
		return fmt.Errorf("api0 密码哈希失败: %w", err)
	}
	if _, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)",
		"api0", apiPwdHash, model.RoleAPI, "内置API调用用户",
	); err != nil {
		return fmt.Errorf("种子用户 api0 插入失败: %w", err)
	}
	return nil
}

// randomPassword 生成指定长度的随机字母数字密码（crypto/rand，密码学安全）
func randomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

// ============================================================
// 用户管理
// ============================================================

// ListUsers 获取所有用户列表
func (s *SQLiteStore) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users ORDER BY uid",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		var expiresAt string
		if err := rows.Scan(&u.UID, &u.UserName, &u.UserPwd, &u.Role, &u.Note, &expiresAt); err != nil {
			return nil, err
		}
		if expiresAt != "" {
			if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
				u.PwdExpiresAt = t
			}
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateUser 创建新用户
func (s *SQLiteStore) CreateUser(userName, userPwd string, role int, note string) error {
	_, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)",
		userName, userPwd, role, note,
	)
	return err
}

// DeleteUserByName 按用户名删除用户
func (s *SQLiteStore) DeleteUserByName(userName string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE user_name = ?", userName)
	return err
}

// ResetPassword 重置用户密码（直接设置哈希后的密码）
func (s *SQLiteStore) ResetPassword(userName, pwdHash string) error {
	_, err := s.db.Exec(
		"UPDATE users SET user_pwd = ? WHERE user_name = ?",
		pwdHash, userName,
	)
	return err
}

// UpdatePassword 修改密码并清除密码过期时间
func (s *SQLiteStore) UpdatePassword(userName, newPwdHash string) error {
	_, err := s.db.Exec(
		"UPDATE users SET user_pwd = ?, pwd_expires_at = '' WHERE user_name = ?",
		newPwdHash, userName,
	)
	if err != nil {
		return err
	}
	return nil
}

// ClearPwdExpiry 清除密码过期时间（修改密码后同步调用）
func (s *SQLiteStore) ClearPwdExpiry(userName string) error {
	_, err := s.db.Exec(
		"UPDATE users SET pwd_expires_at = '' WHERE user_name = ?",
		userName,
	)
	return err
}

// ============================================================
// API Token
// ============================================================

// SaveApiToken 保存 API token 记录
func (s *SQLiteStore) SaveApiToken(userName, tokenPreview string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO api_tokens (user_name, token_preview, expires_at) VALUES (?, ?, ?)",
		userName, tokenPreview, expiresAt,
	)
	return err
}

// GetUserApiTokens 获取用户的有效 API token
func (s *SQLiteStore) GetUserApiTokens(userName string) ([]model.ApiToken, error) {
	rows, err := s.db.Query(
		`SELECT id, user_name, token_preview, expires_at, created_at
		 FROM api_tokens WHERE user_name = ? AND expires_at > datetime('now')
		 ORDER BY created_at DESC`, userName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []model.ApiToken
	for rows.Next() {
		var t model.ApiToken
		if err := rows.Scan(&t.ID, &t.UserName, &t.TokenPreview, &t.ExpiresAt, &t.CreateTime); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// ============================================================
// API 调用日志
// ============================================================

// SaveApiCallLog 保存 API 调用记录
func (s *SQLiteStore) SaveApiCallLog(userName, apiPath, method, reqBody, respBody string, statusCode int, errMsg string) error {
	_, err := s.db.Exec(
		`INSERT INTO api_call_log (user_name, api_path, method, request_body, response_body, status_code, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userName, apiPath, method, reqBody, respBody, statusCode, errMsg,
	)
	return err
}

// GetUserApiCallLogs 获取用户的 API 调用记录（最近 100 条）
func (s *SQLiteStore) GetUserApiCallLogs(userName string) ([]model.ApiCallLog, error) {
	rows, err := s.db.Query(
		`SELECT id, user_name, api_path, method, request_body, response_body, status_code, error_msg, created_at
		 FROM api_call_log WHERE user_name = ? ORDER BY created_at DESC LIMIT 100`, userName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.ApiCallLog
	for rows.Next() {
		var l model.ApiCallLog
		if err := rows.Scan(&l.ID, &l.UserName, &l.APIPath, &l.Method, &l.RequestBody,
			&l.ResponseBody, &l.StatusCode, &l.ErrorMsg, &l.CreateTime); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// ============================================================
// Agent (agent_def) CRUD
// ============================================================

// seedDefaultAgent 当 agent_def 表为空时插入默认 Agent
func (s *SQLiteStore) seedDefaultAgent() error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM agent_def").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	_, err = s.db.Exec(
		`INSERT INTO agent_def (name, description, system_prompt, vdb_ids)
		 VALUES (?, ?, ?, '[]')`,
		"通用客服", "默认智能体，负责解答客户咨询",
		defaultAgentPrompt,
	)
	return err
}

// CreateAgent 创建智能体
func (s *SQLiteStore) CreateAgent(a *model.AgentDef) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO agent_def (name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Description, a.SystemPrompt, a.ModelName, a.Temperature, a.TopP, a.MaxTokens, a.VdbIDs,
	)
	if err != nil {
		return 0, fmt.Errorf("创建智能体失败: %w", err)
	}
	return result.LastInsertId()
}

// GetAgent 根据 ID 获取智能体
func (s *SQLiteStore) GetAgent(id int64) (*model.AgentDef, error) {
	row := s.db.QueryRow(
		`SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at
		 FROM agent_def WHERE id = ?`, id,
	)
	return scanAgentDef(row)
}

// ListAgents 获取所有智能体列表（不含完整 system_prompt）
func (s *SQLiteStore) ListAgents() ([]model.AgentDef, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at
		 FROM agent_def ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []model.AgentDef
	for rows.Next() {
		var a model.AgentDef
		var temp, topP sql.NullFloat64
		var maxTok sql.NullInt64
		err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.ModelName,
			&temp, &topP, &maxTok, &a.VdbIDs, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if temp.Valid {
			a.Temperature = &temp.Float64
		}
		if topP.Valid {
			a.TopP = &topP.Float64
		}
		if maxTok.Valid {
			mt := int(maxTok.Int64)
			a.MaxTokens = &mt
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// UpdateAgent 更新智能体
func (s *SQLiteStore) UpdateAgent(a *model.AgentDef) error {
	_, err := s.db.Exec(
		`UPDATE agent_def SET name=?, description=?, system_prompt=?, model_name=?, temperature=?, top_p=?, max_tokens=?, vdb_ids=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		a.Name, a.Description, a.SystemPrompt, a.ModelName, a.Temperature, a.TopP, a.MaxTokens, a.VdbIDs, a.ID,
	)
	if err != nil {
		return fmt.Errorf("更新智能体失败: %w", err)
	}
	return nil
}

// DeleteAgent 删除智能体
func (s *SQLiteStore) DeleteAgent(id int64) error {
	_, err := s.db.Exec("DELETE FROM agent_def WHERE id = ?", id)
	return err
}

// ============================================================
// Workflow (workflow_def) CRUD
// ============================================================

// CreateWorkflow 创建工作流
func (s *SQLiteStore) CreateWorkflow(w *model.WorkflowDef) (int64, error) {
	nodesJSON, err := json.Marshal(w.Nodes)
	if err != nil {
		return 0, fmt.Errorf("序列化工作流节点失败: %w", err)
	}
	classifierJSON, err := json.Marshal(w.Classifier)
	if err != nil {
		return 0, fmt.Errorf("序列化分类器失败: %w", err)
	}
	if string(classifierJSON) == "null" {
		classifierJSON = []byte("")
	}

	result, err := s.db.Exec(
		"INSERT INTO workflow_def (name, description, classifier, nodes) VALUES (?, ?, ?, ?)",
		w.Name, w.Description, string(classifierJSON), string(nodesJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("创建工作流失败: %w", err)
	}
	return result.LastInsertId()
}

// GetWorkflow 根据 ID 获取工作流
func (s *SQLiteStore) GetWorkflow(id int64) (*model.WorkflowDef, error) {
	row := s.db.QueryRow(
		"SELECT id, name, description, classifier, nodes, created_at, updated_at FROM workflow_def WHERE id = ?", id,
	)
	return scanWorkflowDef(row)
}

// ListWorkflows 获取所有工作流列表（不含节点详情）
func (s *SQLiteStore) ListWorkflows() ([]model.WorkflowDef, error) {
	rows, err := s.db.Query(
		"SELECT id, name, description, classifier, nodes, created_at, updated_at FROM workflow_def ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []model.WorkflowDef
	for rows.Next() {
		var w model.WorkflowDef
		var classifierJSON, nodesJSON string
		err := rows.Scan(&w.ID, &w.Name, &w.Description, &classifierJSON, &nodesJSON, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if classifierJSON != "" {
			var c model.ClassifierDef
			if err := json.Unmarshal([]byte(classifierJSON), &c); err == nil {
				w.Classifier = &c
			}
		}
		if err := json.Unmarshal([]byte(nodesJSON), &w.Nodes); err != nil {
			w.Nodes = nil
		}
		workflows = append(workflows, w)
	}
	return workflows, rows.Err()
}

// UpdateWorkflow 更新工作流
func (s *SQLiteStore) UpdateWorkflow(w *model.WorkflowDef) error {
	nodesJSON, err := json.Marshal(w.Nodes)
	if err != nil {
		return fmt.Errorf("序列化工作流节点失败: %w", err)
	}
	classifierJSON, err := json.Marshal(w.Classifier)
	if err != nil {
		return fmt.Errorf("序列化分类器失败: %w", err)
	}
	if string(classifierJSON) == "null" {
		classifierJSON = []byte("")
	}

	_, err = s.db.Exec(
		"UPDATE workflow_def SET name=?, description=?, classifier=?, nodes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		w.Name, w.Description, string(classifierJSON), string(nodesJSON), w.ID,
	)
	if err != nil {
		return fmt.Errorf("更新工作流失败: %w", err)
	}
	return nil
}

// DeleteWorkflow 删除工作流
func (s *SQLiteStore) DeleteWorkflow(id int64) error {
	_, err := s.db.Exec("DELETE FROM workflow_def WHERE id = ?", id)
	return err
}

// GetConfig 获取单个配置值
func (s *SQLiteStore) GetConfig(key string) (string, error) {
	var value string
	err := s.db.QueryRow(
		"SELECT config_value FROM sys_config WHERE config_key = ?", key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetConfig 设置单个配置值（插入或更新）
func (s *SQLiteStore) SetConfig(key, value, description string) error {
	_, err := s.db.Exec(
		`INSERT INTO sys_config (config_key, config_value, description, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(config_key) DO UPDATE SET
		 config_value = excluded.config_value,
		 description = excluded.description,
		 updated_at = CURRENT_TIMESTAMP`,
		key, value, description,
	)
	return err
}

// GetAllConfigs 获取所有配置项
func (s *SQLiteStore) GetAllConfigs() (map[string]string, error) {
	rows, err := s.db.Query("SELECT config_key, config_value FROM sys_config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

// SeedDefaultConfigs 初始化默认配置（仅当 sys_config 表为空时执行）
func (s *SQLiteStore) SeedDefaultConfigs(sysName string) error {
	// 检查是否已有配置
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM sys_config").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // 已有配置，跳过种子
	}

	entries := []struct{ key, value, desc string }{
		{"sys.name", sysName, "系统名称"},
		{"sys.api_auth", "true", "是否启用接口认证 (true/false)"},
		// 知识库参数
		{"kb.chunk_size", "300", "文本分片大小（字符数）"},
		{"kb.chunk_overlap", "80", "文本分片重叠大小（字符数）"},
		{"kb.top_k", "3", "检索返回条数"},
		{"kb.score_threshold", "0.1", "检索相似度阈值"},
		{"kb.rerank_enabled", "false", "是否启用 Rerank 重排序"},
		{"kb.rerank_retrieve_n", "15", "Rerank 预检索条数"},
		// LLM 参数
		{"llm.temperature", "0.7", "LLM 温度参数 (0-2)"},
		{"llm.top_p", "0.9", "LLM Top-P 采样参数 (0-1)"},
		{"llm.max_tokens", "2048", "LLM 最大生成 Token 数"},
		// FAQ 参数
		{"faq.match_threshold", "0.85", "FAQ 匹配阈值 (0~1)"},
		// csm 客服流程分支绑定的知识库 id（JSON 数组）
		{"csm.billing_vdb_ids", "[3]", "账单分支检索的知识库 id（JSON 数组）"},
		{"csm.repair_vdb_ids", "[3]", "维修分支检索的知识库 id（JSON 数组）"},
		{"csm.faq_vdb_ids", "[3]", "FAQ分支检索的知识库 id（JSON 数组）"},
	}

	for _, e := range entries {
		if err := s.SetConfig(e.key, e.value, e.desc); err != nil {
			return err
		}
	}
	return nil
}

// DefaultChatPrompt 返回默认聊天提示词模板（简单聊天模式使用）
func DefaultChatPrompt() string {
	return defaultChatPrompt
}

// defaultAgentPrompt 默认智能体系统提示词（工作流引擎使用，{{sys.xxx}} 变量语法）
const defaultAgentPrompt = `你是专业的对话机器人，负责解答客户咨询。你必须基于以下知识库信息回答用户问题。
如果知识库中没有相关信息，请引导用户转接人工客服。

今日日期：{{sys.cur_date}}（星期{{sys.cur_week}}）

知识库内容：
---
{{sys.kb_context}}
---

历史对话：
{{sys.history}}

用户问题：{{sys.user_query}}

请用亲切、专业的中文回答：`

// defaultChatPrompt 默认聊天提示词模板（简单聊天模式使用，{xxx} 变量语法）
const defaultChatPrompt = `你是专业的对话机器人，负责解答客户咨询。你必须基于以下知识库信息回答用户问题。
如果知识库中没有相关信息，请引导用户转接人工客服。

今日日期：{cur_date}（星期{cur_week}）

知识库内容：
---
{context}
---

历史对话：
{history}

用户问题：{question}

请用亲切、专业的中文回答：`

// ============================================================
// FAQ 条目管理
// ============================================================

// CreateFaqEntry 创建 FAQ 条目，返回条目 ID
func (s *SQLiteStore) CreateFaqEntry(answer, sourceFile string) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO faq_entries (answer, source_file) VALUES (?, ?)",
		answer, sourceFile,
	)
	if err != nil {
		return 0, fmt.Errorf("创建 FAQ 条目失败: %w", err)
	}
	return result.LastInsertId()
}

// CreateFaqQuestion 创建 FAQ 问题记录
func (s *SQLiteStore) CreateFaqQuestion(entryID int64, question, embeddingJSON string) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO faq_questions (entry_id, question, embedding) VALUES (?, ?, ?)",
		entryID, question, embeddingJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("创建 FAQ 问题失败: %w", err)
	}
	return result.LastInsertId()
}

// GetFaqEntries 获取所有 FAQ 条目（不含问题详情）
func (s *SQLiteStore) GetFaqEntries() ([]model.FaqEntry, error) {
	rows, err := s.db.Query(
		"SELECT id, answer, source_file, created_at FROM faq_entries ORDER BY id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.FaqEntry
	for rows.Next() {
		var e model.FaqEntry
		if err := rows.Scan(&e.ID, &e.Answer, &e.SourceFile, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	// 为每个条目加载问题列表
	for i := range entries {
		questions, err := s.GetFaqQuestionsByEntryID(entries[i].ID)
		if err != nil {
			return nil, err
		}
		entries[i].Questions = questions
	}

	return entries, rows.Err()
}

// GetFaqQuestionsByEntryID 获取某个 FAQ 条目的所有问题
func (s *SQLiteStore) GetFaqQuestionsByEntryID(entryID int64) ([]model.FaqQuestion, error) {
	rows, err := s.db.Query(
		"SELECT id, entry_id, question, created_at FROM faq_questions WHERE entry_id = ? ORDER BY id",
		entryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var questions []model.FaqQuestion
	for rows.Next() {
		var q model.FaqQuestion
		if err := rows.Scan(&q.ID, &q.EntryID, &q.Question, &q.CreatedAt); err != nil {
			return nil, err
		}
		questions = append(questions, q)
	}
	if questions == nil {
		questions = []model.FaqQuestion{}
	}
	return questions, rows.Err()
}

// GetAllFaqQuestionsWithEmbedding 获取所有 FAQ 问题及其向量
func (s *SQLiteStore) GetAllFaqQuestionsWithEmbedding() ([]model.FaqQuestionWithEmbedding, error) {
	rows, err := s.db.Query(
		"SELECT id, entry_id, question, embedding FROM faq_questions",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var questions []model.FaqQuestionWithEmbedding
	for rows.Next() {
		var q model.FaqQuestionWithEmbedding
		var embJSON string
		if err := rows.Scan(&q.ID, &q.EntryID, &q.Question, &embJSON); err != nil {
			return nil, err
		}
		if embJSON != "" {
			if err := json.Unmarshal([]byte(embJSON), &q.Embedding); err != nil {
				// 向量解析失败，跳过该条
				continue
			}
		}
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

// DeleteFaqEntry 删除 FAQ 条目及其所有问题
func (s *SQLiteStore) DeleteFaqEntry(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM faq_questions WHERE entry_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM faq_entries WHERE id = ?", id); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateFaqEntry 更新 FAQ 条目答案
func (s *SQLiteStore) UpdateFaqEntry(id int64, answer string) error {
	_, err := s.db.Exec("UPDATE faq_entries SET answer = ? WHERE id = ?", answer, id)
	return err
}

// DeleteFaqQuestionsByEntryID 删除某个条目的所有问题
func (s *SQLiteStore) DeleteFaqQuestionsByEntryID(entryID int64) error {
	_, err := s.db.Exec("DELETE FROM faq_questions WHERE entry_id = ?", entryID)
	return err
}

// ClearAllFaq 清空所有 FAQ 数据
func (s *SQLiteStore) ClearAllFaq() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM faq_questions"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM faq_entries"); err != nil {
		return err
	}
	return tx.Commit()
}

// ============================================================
// 会话历史 (chat_sessions) — TODO: 后续迁移至 Redis
// ============================================================

// SaveChatMessage 保存一条聊天消息
func (s *SQLiteStore) SaveChatMessage(uid, role, content string) error {
	_, err := s.db.Exec(
		"INSERT INTO chat_sessions (uid, role, content) VALUES (?, ?, ?)",
		uid, role, content,
	)
	return err
}

// GetChatMessages 获取用户最近 limit 条聊天消息
func (s *SQLiteStore) GetChatMessages(uid string, limit int) ([]model.ChatMessage, error) {
	rows, err := s.db.Query(
		"SELECT role, content FROM chat_sessions WHERE uid = ? ORDER BY created_at DESC LIMIT ?",
		uid, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.ChatMessage
	for rows.Next() {
		var m model.ChatMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// 反转回时间正序
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

// ClearChatMessages 清空用户聊天记录
func (s *SQLiteStore) ClearChatMessages(uid string) error {
	_, err := s.db.Exec("DELETE FROM chat_sessions WHERE uid = ?", uid)
	return err
}

// ============================================================
// 辅助函数
// ============================================================

func scanVdbInfo(row *sql.Row) (*model.VdbInfo, error) {
	var v model.VdbInfo
	var isPublic, isDefault int
	err := row.Scan(&v.ID, &v.Name, &v.UID, &isPublic, &isDefault, &v.CreateTime)
	if err != nil {
		return nil, err
	}
	v.IsPublic = isPublic != 0
	v.IsDefault = isDefault != 0
	return &v, nil
}

func scanVdbInfoList(rows *sql.Rows) ([]model.VdbInfo, error) {
	var list []model.VdbInfo
	for rows.Next() {
		var v model.VdbInfo
		var isPublic, isDefault int
		if err := rows.Scan(&v.ID, &v.Name, &v.UID, &isPublic, &isDefault, &v.CreateTime); err != nil {
			return nil, err
		}
		v.IsPublic = isPublic != 0
		v.IsDefault = isDefault != 0
		list = append(list, v)
	}
	return list, rows.Err()
}

func scanAgentDef(row *sql.Row) (*model.AgentDef, error) {
	var a model.AgentDef
	var temp, topP sql.NullFloat64
	var maxTok sql.NullInt64
	err := row.Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.ModelName,
		&temp, &topP, &maxTok, &a.VdbIDs, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if temp.Valid {
		a.Temperature = &temp.Float64
	}
	if topP.Valid {
		a.TopP = &topP.Float64
	}
	if maxTok.Valid {
		mt := int(maxTok.Int64)
		a.MaxTokens = &mt
	}
	return &a, nil
}

func scanAgentList(rows *sql.Rows) ([]model.AgentDef, error) {
	var agents []model.AgentDef
	for rows.Next() {
		var a model.AgentDef
		var temp, topP sql.NullFloat64
		var maxTok sql.NullInt64
		err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.SystemPrompt, &a.ModelName,
			&temp, &topP, &maxTok, &a.VdbIDs, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if temp.Valid {
			a.Temperature = &temp.Float64
		}
		if topP.Valid {
			a.TopP = &topP.Float64
		}
		if maxTok.Valid {
			mt := int(maxTok.Int64)
			a.MaxTokens = &mt
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func scanWorkflowDef(row *sql.Row) (*model.WorkflowDef, error) {
	var w model.WorkflowDef
	var classifierJSON, nodesJSON string
	err := row.Scan(&w.ID, &w.Name, &w.Description, &classifierJSON, &nodesJSON, &w.CreatedAt, &w.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if classifierJSON != "" {
		var c model.ClassifierDef
		if err := json.Unmarshal([]byte(classifierJSON), &c); err == nil {
			w.Classifier = &c
		}
	}
	if err := json.Unmarshal([]byte(nodesJSON), &w.Nodes); err != nil {
		w.Nodes = nil
	}
	return &w, nil
}
