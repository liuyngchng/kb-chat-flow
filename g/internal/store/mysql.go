package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"kb-chat-flow/internal/model"

	_ "github.com/go-sql-driver/mysql"
)

// MySQLStore MySQL 元数据存储
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore 创建 MySQL 存储
func NewMySQLStore(dsn string) (*MySQLStore, error) {
	if dsn == "" {
		return nil, fmt.Errorf("MySQL DSN 不能为空")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}

	// 连接池配置
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	// 验证连接
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("MySQL 连接验证失败: %w", err)
	}

	store := &MySQLStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return store, nil
}

// Close 关闭数据库连接
func (s *MySQLStore) Close() error {
	return s.db.Close()
}

// migrate 创建表结构 (MySQL 语法)
func (s *MySQLStore) migrate() error {
	schema := `
		CREATE TABLE IF NOT EXISTS vdb_info (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			uid VARCHAR(255) NOT NULL DEFAULT '',
			is_public TINYINT NOT NULL DEFAULT 0,
			is_default TINYINT NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_vdb_info_uid (uid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS vdb_file_info (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(512) NOT NULL,
			uid VARCHAR(255) NOT NULL DEFAULT '',
			vdb_id BIGINT NOT NULL,
			task_id VARCHAR(64) NOT NULL DEFAULT '',
			file_path VARCHAR(1024) NOT NULL DEFAULT '',
			percent DOUBLE NOT NULL DEFAULT 0,
			process_info TEXT NOT NULL,
			file_md5 VARCHAR(64) NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_vdb_file_info_vdb_id (vdb_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS prompt_template (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL UNIQUE,
			value TEXT NOT NULL,
			uid BIGINT NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS sys_config (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			config_key VARCHAR(255) NOT NULL UNIQUE,
			config_value TEXT NOT NULL,
			description VARCHAR(512) NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_sys_config_key (config_key)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS users (
			uid BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_name VARCHAR(255) NOT NULL UNIQUE,
			user_pwd VARCHAR(255) NOT NULL DEFAULT '',
			role INT NOT NULL DEFAULT 0,
			note VARCHAR(512) NOT NULL DEFAULT '',
			pwd_expires_at DATETIME NULL DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_users_name (user_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS api_tokens (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_name VARCHAR(255) NOT NULL,
			token_preview VARCHAR(64) NOT NULL DEFAULT '',
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_api_tokens_user (user_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS api_call_log (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_name VARCHAR(255) NOT NULL,
			api_path VARCHAR(512) NOT NULL DEFAULT '',
			method VARCHAR(10) NOT NULL DEFAULT '',
			request_body TEXT NOT NULL,
			response_body TEXT NOT NULL,
			status_code INT NOT NULL DEFAULT 200,
			error_msg TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_api_call_log_user (user_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS agent_def (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT NOT NULL,
			system_prompt TEXT NOT NULL,
			model_name VARCHAR(255) NOT NULL DEFAULT '',
			temperature DOUBLE,
			top_p DOUBLE,
			max_tokens INT,
			vdb_ids TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		CREATE TABLE IF NOT EXISTS workflow_def (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			classifier TEXT NOT NULL DEFAULT '',
			nodes TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		-- FAQ 条目表
		CREATE TABLE IF NOT EXISTS faq_entries (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			answer TEXT NOT NULL,
			source_file VARCHAR(512) NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		-- FAQ 问题表
		CREATE TABLE IF NOT EXISTS faq_questions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			entry_id BIGINT NOT NULL,
			question TEXT NOT NULL,
			embedding TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_faq_questions_entry (entry_id),
			FOREIGN KEY (entry_id) REFERENCES faq_entries(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

		-- 会话历史持久化表（TODO: 后续迁移至 Redis）
		CREATE TABLE IF NOT EXISTS chat_sessions (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			uid VARCHAR(255) NOT NULL,
			role VARCHAR(16) NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_chat_sessions_uid (uid, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
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
func (s *MySQLStore) CreateVdb(name, uid string, isPublic bool) (int64, error) {
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
func (s *MySQLStore) GetVdbByID(id int64) (*model.VdbInfo, error) {
	row := s.db.QueryRow(
		"SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE id = ?", id,
	)
	return scanVdbInfo(row)
}

// GetUserVdbs 获取用户的所有知识库
func (s *MySQLStore) GetUserVdbs(uid string) ([]model.VdbInfo, error) {
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
func (s *MySQLStore) GetPublicVdbs(excludeUID string) ([]model.VdbInfo, error) {
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
func (s *MySQLStore) DeleteVdb(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM vdb_file_info WHERE vdb_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM vdb_info WHERE id = ?", id); err != nil {
		return err
	}

	return tx.Commit()
}

// SetDefaultVdb 设置默认知识库
func (s *MySQLStore) SetDefaultVdb(id int64, uid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE vdb_info SET is_default = 0 WHERE uid = ?", uid); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE vdb_info SET is_default = 1 WHERE id = ? AND uid = ?", id, uid); err != nil {
		return err
	}

	return tx.Commit()
}

// CheckVdbNameExists 检查知识库名称是否已存在
func (s *MySQLStore) CheckVdbNameExists(name, uid string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM vdb_info WHERE name = ? AND uid = ?", name, uid,
	).Scan(&count)
	return count > 0, err
}

// GetDefaultVdbID 获取用户的默认知识库 ID
func (s *MySQLStore) GetDefaultVdbID(uid string) (int64, error) {
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
func (s *MySQLStore) CreateFileInfo(info *model.VdbFileInfo) (int64, error) {
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
func (s *MySQLStore) GetFilesByVdbID(vdbID int64) ([]model.VdbFileInfo, error) {
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
func (s *MySQLStore) GetFileByID(id int64) (*model.VdbFileInfo, error) {
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
func (s *MySQLStore) GetUnprocessedFiles() ([]model.VdbFileInfo, error) {
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
func (s *MySQLStore) UpdateFileProgress(id int64, percent float64, info string) error {
	_, err := s.db.Exec(
		"UPDATE vdb_file_info SET percent = ?, process_info = ? WHERE id = ?",
		percent, info, id,
	)
	return err
}

// DeleteFile 删除文件记录
func (s *MySQLStore) DeleteFile(id int64) error {
	_, err := s.db.Exec("DELETE FROM vdb_file_info WHERE id = ?", id)
	return err
}

// CheckFileMD5Exists 检查同一知识库下的文件 MD5 是否已存在
func (s *MySQLStore) CheckFileMD5Exists(vdbID int64, md5 string) (*model.VdbFileInfo, error) {
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

// GetPrompt 根据名称获取提示词模板
func (s *MySQLStore) GetPrompt(name string) (string, error) {
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
func (s *MySQLStore) UpsertPrompt(name, value string, uid int) error {
	_, err := s.db.Exec(
		`INSERT INTO prompt_template (name, value, uid) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value), uid = VALUES(uid)`,
		name, value, uid,
	)
	return err
}

// ============================================================
// 用户 (users)
// ============================================================

// GetUserByLogin 按用户名查询用户（密码验证由 handler 层用 bcrypt 完成）
func (s *MySQLStore) GetUserByLogin(userName string) (*model.User, error) {
	row := s.db.QueryRow(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?",
		userName,
	)
	return scanMySQLUser(row)
}

// GetUserByName 按用户名查询用户
func (s *MySQLStore) GetUserByName(userName string) (*model.User, error) {
	row := s.db.QueryRow(
		"SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?",
		userName,
	)
	return scanMySQLUser(row)
}

// scanMySQLUser 扫描 users 行，解析 pwd_expires_at（NULL = 无过期限制）
func scanMySQLUser(row *sql.Row) (*model.User, error) {
	var u model.User
	var expiresAt sql.NullTime
	err := row.Scan(&u.UID, &u.UserName, &u.UserPwd, &u.Role, &u.Note, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		u.PwdExpiresAt = expiresAt.Time
	}
	return &u, nil
}

// seedUsers 种子内置用户：
//   - admin：内置管理员，密码随机生成、启动后 2 小时内有效，登录后强制修改密码。
//   - api0：内置 API 调用用户。
// 均按用户名检查，已存在则跳过，避免被注册功能抢注导致系统失去管理员。
func (s *MySQLStore) seedUsers() error {
	if err := s.seedAdminIfMissing(); err != nil {
		return err
	}
	return s.seedAPI0IfMissing()
}

// seedAdminIfMissing 当 users 表中不存在 admin 用户时，创建内置管理员。
// 随机密码打印到控制台和日志文件；登录后 2 小时内未修改则密码过期，需重置数据库。
func (s *MySQLStore) seedAdminIfMissing() error {
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
	expiresAt := time.Now().Add(adminPwdExpiry)
	if _, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note, pwd_expires_at) VALUES (?, ?, ?, ?, ?)",
		"admin", pwdHash, model.RoleAdmin, "内置管理员", expiresAt,
	); err != nil {
		return fmt.Errorf("种子用户 admin 插入失败: %w", err)
	}

	// 随机密码打印到控制台和日志文件
	slog.Info("首次运行已创建管理员账号", "user_name", "admin", "initial_password", adminPwd, "expires_in", adminPwdExpiry.String())
	fmt.Printf("\n========================================\n")
	fmt.Printf("  首次运行已创建管理员账号 admin\n")
	fmt.Printf("  初始密码: %s\n", adminPwd)
	fmt.Printf("  该密码 %s 内有效，登录后需立即修改密码\n", adminPwdExpiryText)
	fmt.Printf("  若忘记初始密码，请从 cfg.db.template 重置数据库后重启\n")
	fmt.Printf("========================================\n\n")

	return nil
}

// seedAPI0IfMissing 当 users 表中不存在 api0 用户时，创建内置 API 调用用户
func (s *MySQLStore) seedAPI0IfMissing() error {
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

// ============================================================
// 用户管理
// ============================================================

// ListUsers 获取所有用户列表
func (s *MySQLStore) ListUsers() ([]model.User, error) {
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
		var expiresAt sql.NullTime
		if err := rows.Scan(&u.UID, &u.UserName, &u.UserPwd, &u.Role, &u.Note, &expiresAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			u.PwdExpiresAt = expiresAt.Time
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateUser 创建新用户
func (s *MySQLStore) CreateUser(userName, userPwd string, role int, note string) error {
	_, err := s.db.Exec(
		"INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)",
		userName, userPwd, role, note,
	)
	return err
}

// DeleteUserByName 按用户名删除用户
func (s *MySQLStore) DeleteUserByName(userName string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE user_name = ?", userName)
	return err
}

// ResetPassword 重置用户密码（直接设置哈希后的密码）
func (s *MySQLStore) ResetPassword(userName, pwdHash string) error {
	_, err := s.db.Exec(
		"UPDATE users SET user_pwd = ? WHERE user_name = ?",
		pwdHash, userName,
	)
	return err
}

// UpdatePassword 修改密码并清除密码过期时间
func (s *MySQLStore) UpdatePassword(userName, newPwdHash string) error {
	_, err := s.db.Exec(
		"UPDATE users SET user_pwd = ?, pwd_expires_at = NULL WHERE user_name = ?",
		newPwdHash, userName,
	)
	if err != nil {
		return err
	}
	return nil
}

// ClearPwdExpiry 清除密码过期时间（修改密码后同步调用）
func (s *MySQLStore) ClearPwdExpiry(userName string) error {
	_, err := s.db.Exec(
		"UPDATE users SET pwd_expires_at = NULL WHERE user_name = ?",
		userName,
	)
	return err
}

// ============================================================
// API Token
// ============================================================

// SaveApiToken 保存 API token 记录
func (s *MySQLStore) SaveApiToken(userName, tokenPreview string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		"INSERT INTO api_tokens (user_name, token_preview, expires_at) VALUES (?, ?, ?)",
		userName, tokenPreview, expiresAt,
	)
	return err
}

// GetUserApiTokens 获取用户的有效 API token
func (s *MySQLStore) GetUserApiTokens(userName string) ([]model.ApiToken, error) {
	rows, err := s.db.Query(
		`SELECT id, user_name, token_preview, expires_at, created_at
		 FROM api_tokens WHERE user_name = ? AND expires_at > NOW()
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
func (s *MySQLStore) SaveApiCallLog(userName, apiPath, method, reqBody, respBody string, statusCode int, errMsg string) error {
	_, err := s.db.Exec(
		`INSERT INTO api_call_log (user_name, api_path, method, request_body, response_body, status_code, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userName, apiPath, method, reqBody, respBody, statusCode, errMsg,
	)
	return err
}

// GetUserApiCallLogs 获取用户的 API 调用记录（最近 100 条）
func (s *MySQLStore) GetUserApiCallLogs(userName string) ([]model.ApiCallLog, error) {
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
func (s *MySQLStore) seedDefaultAgent() error {
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
		 VALUES (?, ?, ?, ?)`,
		"通用客服", "默认智能体，负责解答客户咨询",
		defaultAgentPrompt, "[]",
	)
	return err
}

// CreateAgent 创建智能体
func (s *MySQLStore) CreateAgent(a *model.AgentDef) (int64, error) {
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
func (s *MySQLStore) GetAgent(id int64) (*model.AgentDef, error) {
	row := s.db.QueryRow(
		`SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at
		 FROM agent_def WHERE id = ?`, id,
	)
	return scanAgentDef(row)
}

// ListAgents 获取所有智能体列表
func (s *MySQLStore) ListAgents() ([]model.AgentDef, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at
		 FROM agent_def ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgentList(rows)
}

// UpdateAgent 更新智能体
func (s *MySQLStore) UpdateAgent(a *model.AgentDef) error {
	_, err := s.db.Exec(
		`UPDATE agent_def SET name=?, description=?, system_prompt=?, model_name=?, temperature=?, top_p=?, max_tokens=?, vdb_ids=?
		 WHERE id=?`,
		a.Name, a.Description, a.SystemPrompt, a.ModelName, a.Temperature, a.TopP, a.MaxTokens, a.VdbIDs, a.ID,
	)
	if err != nil {
		return fmt.Errorf("更新智能体失败: %w", err)
	}
	return nil
}

// DeleteAgent 删除智能体
func (s *MySQLStore) DeleteAgent(id int64) error {
	_, err := s.db.Exec("DELETE FROM agent_def WHERE id = ?", id)
	return err
}

// ============================================================
// Workflow (workflow_def) CRUD
// ============================================================

// CreateWorkflow 创建工作流
func (s *MySQLStore) CreateWorkflow(w *model.WorkflowDef) (int64, error) {
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
func (s *MySQLStore) GetWorkflow(id int64) (*model.WorkflowDef, error) {
	row := s.db.QueryRow(
		"SELECT id, name, description, classifier, nodes, created_at, updated_at FROM workflow_def WHERE id = ?", id,
	)
	return scanWorkflowDef(row)
}

// ListWorkflows 获取所有工作流列表
func (s *MySQLStore) ListWorkflows() ([]model.WorkflowDef, error) {
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
func (s *MySQLStore) UpdateWorkflow(w *model.WorkflowDef) error {
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
		"UPDATE workflow_def SET name=?, description=?, classifier=?, nodes=? WHERE id=?",
		w.Name, w.Description, string(classifierJSON), string(nodesJSON), w.ID,
	)
	if err != nil {
		return fmt.Errorf("更新工作流失败: %w", err)
	}
	return nil
}

// DeleteWorkflow 删除工作流
func (s *MySQLStore) DeleteWorkflow(id int64) error {
	_, err := s.db.Exec("DELETE FROM workflow_def WHERE id = ?", id)
	return err
}

// ============================================================
// 系统配置 (sys_config)
// ============================================================

// GetConfig 获取单个配置值
func (s *MySQLStore) GetConfig(key string) (string, error) {
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
func (s *MySQLStore) SetConfig(key, value, description string) error {
	_, err := s.db.Exec(
		`INSERT INTO sys_config (config_key, config_value, description, updated_at)
		 VALUES (?, ?, ?, NOW())
		 ON DUPLICATE KEY UPDATE
		 config_value = VALUES(config_value),
		 description = VALUES(description),
		 updated_at = NOW()`,
		key, value, description,
	)
	return err
}

// GetAllConfigs 获取所有配置项
func (s *MySQLStore) GetAllConfigs() (map[string]string, error) {
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
func (s *MySQLStore) SeedDefaultConfigs(sysName string) error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM sys_config").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	entries := []struct{ key, value, desc string }{
		{"sys.name", sysName, "系统名称"},
		{"sys.api_auth", "true", "是否启用接口认证 (true/false)"},
		{"kb.chunk_size", "300", "文本分片大小（字符数）"},
		{"kb.chunk_overlap", "80", "文本分片重叠大小（字符数）"},
		{"kb.top_k", "3", "检索返回条数"},
		{"kb.rerank_enabled", "false", "是否启用 Rerank 重排序"},
		{"kb.rerank_retrieve_n", "15", "Rerank 预检索条数"},
		{"kb.score_threshold", "0.1", "检索相似度阈值"},
		{"llm.temperature", "0.7", "LLM 温度参数 (0-2)"},
		{"llm.top_p", "0.9", "LLM Top-P 采样参数 (0-1)"},
		{"llm.max_tokens", "2048", "LLM 最大生成 Token 数"},
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

// ============================================================
// FAQ 条目管理
// ============================================================

// CreateFaqEntry 创建 FAQ 条目
func (s *MySQLStore) CreateFaqEntry(answer, sourceFile string) (int64, error) {
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
func (s *MySQLStore) CreateFaqQuestion(entryID int64, question, embeddingJSON string) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO faq_questions (entry_id, question, embedding) VALUES (?, ?, ?)",
		entryID, question, embeddingJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("创建 FAQ 问题失败: %w", err)
	}
	return result.LastInsertId()
}

// GetFaqEntries 获取所有 FAQ 条目
func (s *MySQLStore) GetFaqEntries() ([]model.FaqEntry, error) {
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
	for i := range entries {
		questions, err := s.GetFaqQuestionsByEntryID(entries[i].ID)
		if err != nil {
			return nil, err
		}
		entries[i].Questions = questions
	}
	return entries, rows.Err()
}

// GetFaqQuestionsByEntryID 获取某条目的所有问题
func (s *MySQLStore) GetFaqQuestionsByEntryID(entryID int64) ([]model.FaqQuestion, error) {
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
func (s *MySQLStore) GetAllFaqQuestionsWithEmbedding() ([]model.FaqQuestionWithEmbedding, error) {
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
				continue
			}
		}
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

// DeleteFaqEntry 删除 FAQ 条目及其问题
func (s *MySQLStore) DeleteFaqEntry(id int64) error {
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
func (s *MySQLStore) UpdateFaqEntry(id int64, answer string) error {
	_, err := s.db.Exec("UPDATE faq_entries SET answer = ? WHERE id = ?", answer, id)
	return err
}

// DeleteFaqQuestionsByEntryID 删除某条目的所有问题
func (s *MySQLStore) DeleteFaqQuestionsByEntryID(entryID int64) error {
	_, err := s.db.Exec("DELETE FROM faq_questions WHERE entry_id = ?", entryID)
	return err
}

// ClearAllFaq 清空所有 FAQ
func (s *MySQLStore) ClearAllFaq() error {
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
func (s *MySQLStore) SaveChatMessage(uid, role, content string) error {
	_, err := s.db.Exec(
		"INSERT INTO chat_sessions (uid, role, content) VALUES (?, ?, ?)",
		uid, role, content,
	)
	return err
}

// GetChatMessages 获取用户最近 limit 条聊天消息
func (s *MySQLStore) GetChatMessages(uid string, limit int) ([]model.ChatMessage, error) {
	rows, err := s.db.Query(
		"SELECT role, content FROM (SELECT role, content FROM chat_sessions WHERE uid = ? ORDER BY created_at DESC LIMIT ?) sub ORDER BY created_at ASC",
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
	return msgs, rows.Err()
}

// ClearChatMessages 清空用户聊天记录
func (s *MySQLStore) ClearChatMessages(uid string) error {
	_, err := s.db.Exec("DELETE FROM chat_sessions WHERE uid = ?", uid)
	return err
}

// ============================================================
// 辅助函数 (复用 sqlite.go 中的辅助)
// ============================================================
// scanVdbInfo, scanVdbInfoList, scanAgentDef, scanWorkflowDef
// 已在 sqlite.go 中定义，这里复用同包的辅助函数
