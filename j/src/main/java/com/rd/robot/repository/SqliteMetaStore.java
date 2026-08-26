package com.rd.robot.repository;

import com.alibaba.druid.pool.DruidDataSource;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.*;
import com.rd.robot.security.PasswordEncoder;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.File;
import java.sql.*;
import java.time.LocalDateTime;
import java.util.*;
import java.util.stream.Collectors;

public class SqliteMetaStore implements MetaStore {

    private static final Logger log = LoggerFactory.getLogger(SqliteMetaStore.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final DruidDataSource ds;

    public SqliteMetaStore(String dbPath) {
        File dbFile = new File(dbPath);
        if (!dbFile.exists()) {
            throw new RuntimeException("数据库文件 " + dbPath + " 不存在，请从 cfg.db.template 复制");
        }

        ds = new DruidDataSource();
        ds.setUrl("jdbc:sqlite:" + dbPath);
        ds.setMaxActive(4);
        ds.setMinIdle(0);
        ds.setMaxWait(5000);
        ds.setTestOnBorrow(true);
        ds.setValidationQuery("SELECT 1");

        try (Connection conn = ds.getConnection(); Statement stmt = conn.createStatement()) {
            stmt.execute("PRAGMA journal_mode=WAL");
        } catch (SQLException e) {
            close();
            throw new RuntimeException("启用 WAL 失败", e);
        }

        migrate();
        seedUsers();
        seedDefaultAgent();
        log.info("SQLite 数据库初始化完成");
    }

    // ============================================================
    // Schema migration
    // ============================================================

    private void migrate() {
        String schema = """
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
            CREATE TABLE IF NOT EXISTS faq_entries (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                answer TEXT NOT NULL,
                source_file TEXT NOT NULL DEFAULT '',
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            );
            CREATE TABLE IF NOT EXISTS faq_questions (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                entry_id INTEGER NOT NULL,
                question TEXT NOT NULL,
                embedding TEXT NOT NULL DEFAULT '',
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
                FOREIGN KEY (entry_id) REFERENCES faq_entries(id) ON DELETE CASCADE
            );
            CREATE INDEX IF NOT EXISTS idx_vdb_info_uid ON vdb_info(uid);
            CREATE INDEX IF NOT EXISTS idx_vdb_file_info_vdb_id ON vdb_file_info(vdb_id);
            CREATE INDEX IF NOT EXISTS idx_sys_config_key ON sys_config(config_key);
            CREATE INDEX IF NOT EXISTS idx_users_name ON users(user_name);
            CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_name);
            CREATE INDEX IF NOT EXISTS idx_api_call_log_user ON api_call_log(user_name);
            CREATE INDEX IF NOT EXISTS idx_faq_questions_entry ON faq_questions(entry_id);

            -- 会话历史持久化表（TODO: 后续迁移至 Redis）
            CREATE TABLE IF NOT EXISTS chat_sessions (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                uid TEXT NOT NULL,
                role TEXT NOT NULL,
                content TEXT NOT NULL,
                created_at DATETIME DEFAULT CURRENT_TIMESTAMP
            );
            CREATE INDEX IF NOT EXISTS idx_chat_sessions_uid ON chat_sessions(uid, created_at);
            """;
        try (Connection conn = ds.getConnection(); Statement stmt = conn.createStatement()) {
            stmt.executeUpdate(schema);
        } catch (SQLException e) {
            throw new RuntimeException("数据库迁移失败", e);
        }
    }

    private void seedUsers() {
    try (Connection conn = ds.getConnection()) {
        int count = queryInt(conn, "SELECT COUNT(*) FROM users");
        if (count > 0) return;

        // 生成随机初始密码（12 位字母数字）
        String adminPwd = randomPassword(12);
        String pwdHash = PasswordEncoder.hashPassword(adminPwd);

        // admin 密码 1 小时后过期，登录后强制修改
        String expiresAt = LocalDateTime.now().plusHours(1).toString();
        String sqlAdmin = "INSERT INTO users (user_name, user_pwd, role, note, pwd_expires_at) VALUES (?, ?, ?, ?, ?)";
        try (PreparedStatement ps = conn.prepareStatement(sqlAdmin)) {
            ps.setString(1, "admin");
            ps.setString(2, pwdHash);
            ps.setInt(3, User.ROLE_ADMIN);
            ps.setString(4, "内置管理员");
            ps.setString(5, expiresAt);
            ps.executeUpdate();
        }

        // 内置 API 调用用户
        String apiPwdHash = PasswordEncoder.hashPassword("api0");
        String sqlApi = "INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)";
        try (PreparedStatement ps = conn.prepareStatement(sqlApi)) {
            ps.setString(1, "api0");
            ps.setString(2, apiPwdHash);
            ps.setInt(3, User.ROLE_API);
            ps.setString(4, "内置API调用用户");
            ps.executeUpdate();
        }

        // 随机密码打印到控制台和日志文件
        log.info("首次运行已创建管理员账号 user_name=admin initial_password={} expires_in=1h", adminPwd);
        System.out.println("\n========================================");
        System.out.println("  首次运行已创建管理员账号 admin");
        System.out.println("  初始密码: " + adminPwd);
        System.out.println("  该密码 1 小时内有效，登录后需立即修改密码");
        System.out.println("========================================\n");
    } catch (SQLException e) {
        throw new RuntimeException("种子用户插入失败", e);
    }
}

/**
 * 生成指定长度的随机字母数字密码（crypto/rand，密码学安全）
 */
private static String randomPassword(int length) {
    String charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    java.security.SecureRandom random = new java.security.SecureRandom();
    StringBuilder sb = new StringBuilder(length);
    for (int i = 0; i < length; i++) {
        sb.append(charset.charAt(random.nextInt(charset.length())));
    }
    return sb.toString();
}

    private void seedDefaultAgent() {
        try (Connection conn = ds.getConnection()) {
            int count = queryInt(conn, "SELECT COUNT(*) FROM agent_def");
            if (count > 0) return;

            try (PreparedStatement ps = conn.prepareStatement(
                    "INSERT INTO agent_def (name, description, system_prompt, vdb_ids) VALUES (?, ?, ?, '[]')")) {
                ps.setString(1, "通用客服");
                ps.setString(2, "默认智能体，负责解答客户咨询");
                ps.setString(3, DEFAULT_AGENT_PROMPT);
                ps.executeUpdate();
            }
        } catch (SQLException e) {
            throw new RuntimeException("默认 Agent 插入失败", e);
        }
    }

    // ============================================================
    // VDB CRUD
    // ============================================================

    @Override
    public long createVdb(String name, String uid, boolean isPublic) {
        String sql = "INSERT INTO vdb_info (name, uid, is_public) VALUES (?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, name);
            ps.setString(2, uid);
            ps.setInt(3, isPublic ? 1 : 0);
            ps.executeUpdate();
            try (ResultSet rs = ps.getGeneratedKeys()) {
                if (rs.next()) return rs.getLong(1);
            }
            return 0;
        } catch (SQLException e) {
            throw new RuntimeException("创建知识库失败", e);
        }
    }

    @Override
    public VdbInfo getVdbByID(long id) {
        String sql = "SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE id = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, id);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapVdbInfo(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询知识库失败", e);
        }
    }

    @Override
    public List<VdbInfo> getUserVdbs(String uid) {
        String sql = "SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE uid = ? ORDER BY created_at DESC";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, uid);
            try (ResultSet rs = ps.executeQuery()) {
                return mapVdbInfoList(rs);
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询知识库列表失败", e);
        }
    }

    @Override
    public List<VdbInfo> getPublicVdbs(String excludeUid) {
        String sql = "SELECT id, name, uid, is_public, is_default, created_at FROM vdb_info WHERE is_public = 1 AND uid != ? ORDER BY created_at DESC";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, excludeUid);
            try (ResultSet rs = ps.executeQuery()) {
                return mapVdbInfoList(rs);
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询公开知识库失败", e);
        }
    }

    @Override
    public void deleteVdb(long id) {
        try (Connection conn = ds.getConnection()) {
            conn.setAutoCommit(false);
            try {
                execUpdate(conn, "DELETE FROM vdb_file_info WHERE vdb_id = ?", id);
                execUpdate(conn, "DELETE FROM vdb_info WHERE id = ?", id);
                conn.commit();
            } catch (SQLException e) {
                conn.rollback();
                throw e;
            } finally {
                conn.setAutoCommit(true);
            }
        } catch (SQLException e) {
            throw new RuntimeException("删除知识库失败", e);
        }
    }

    @Override
    public void setDefaultVdb(long id, String uid) {
        try (Connection conn = ds.getConnection()) {
            conn.setAutoCommit(false);
            try {
                execUpdate(conn, "UPDATE vdb_info SET is_default = 0 WHERE uid = ?", uid);
                execUpdate(conn, "UPDATE vdb_info SET is_default = 1 WHERE id = ? AND uid = ?", id, uid);
                conn.commit();
            } catch (SQLException e) {
                conn.rollback();
                throw e;
            } finally {
                conn.setAutoCommit(true);
            }
        } catch (SQLException e) {
            throw new RuntimeException("设置默认知识库失败", e);
        }
    }

    @Override
    public boolean checkVdbNameExists(String name, String uid) {
        String sql = "SELECT COUNT(*) FROM vdb_info WHERE name = ? AND uid = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, name);
            ps.setString(2, uid);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() && rs.getInt(1) > 0;
            }
        } catch (SQLException e) {
            throw new RuntimeException("检查知识库名称失败", e);
        }
    }

    @Override
    public long getDefaultVdbID(String uid) {
        String sql = "SELECT id FROM vdb_info WHERE uid = ? AND is_default = 1 LIMIT 1";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, uid);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? rs.getLong(1) : 0;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询默认知识库失败", e);
        }
    }

    // ============================================================
    // File CRUD
    // ============================================================

    @Override
    public long createFileInfo(VdbFileInfo info) {
        String sql = "INSERT INTO vdb_file_info (name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5) VALUES (?, ?, ?, ?, ?, ?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, info.getName());
            ps.setString(2, info.getUid());
            ps.setLong(3, info.getVdbId());
            ps.setString(4, info.getTaskId());
            ps.setString(5, info.getFilePath());
            ps.setDouble(6, info.getPercent());
            ps.setString(7, info.getProcessInfo());
            ps.setString(8, info.getFileMd5());
            ps.executeUpdate();
            try (ResultSet rs = ps.getGeneratedKeys()) {
                if (rs.next()) return rs.getLong(1);
            }
            return 0;
        } catch (SQLException e) {
            throw new RuntimeException("创建文件记录失败", e);
        }
    }

    @Override
    public List<VdbFileInfo> getFilesByVdbID(long vdbId) {
        String sql = "SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at FROM vdb_file_info WHERE vdb_id = ? ORDER BY created_at DESC";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, vdbId);
            try (ResultSet rs = ps.executeQuery()) {
                return mapFileInfoList(rs);
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询文件列表失败", e);
        }
    }

    @Override
    public VdbFileInfo getFileByID(long id) {
        String sql = "SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at FROM vdb_file_info WHERE id = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, id);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapFileInfo(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询文件失败", e);
        }
    }

    @Override
    public List<VdbFileInfo> getUnprocessedFiles() {
        String sql = "SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at FROM vdb_file_info WHERE percent != 100 ORDER BY created_at ASC";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                return mapFileInfoList(rs);
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询未处理文件失败", e);
        }
    }

    @Override
    public void updateFileProgress(long id, double percent, String info) {
        execUpdate("UPDATE vdb_file_info SET percent = ?, process_info = ? WHERE id = ?", percent, info, id);
    }

    @Override
    public void deleteFile(long id) {
        execUpdate("DELETE FROM vdb_file_info WHERE id = ?", id);
    }

    @Override
    public VdbFileInfo checkFileMD5Exists(long vdbId, String md5) {
        String sql = "SELECT id, name, uid, vdb_id, task_id, file_path, percent, process_info, file_md5, created_at FROM vdb_file_info WHERE vdb_id = ? AND file_md5 = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, vdbId);
            ps.setString(2, md5);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapFileInfo(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("检查文件MD5失败", e);
        }
    }

    // ============================================================
    // Prompt templates
    // ============================================================

    @Override
    public String getPrompt(String name) {
        String sql = "SELECT value FROM prompt_template WHERE name = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, name);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? rs.getString("value") : "";
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询提示词失败", e);
        }
    }

    @Override
    public void upsertPrompt(String name, String value, int uid) {
        String sql = "INSERT INTO prompt_template (name, value, uid) VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET value = excluded.value, uid = excluded.uid";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, name);
            ps.setString(2, value);
            ps.setInt(3, uid);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("保存提示词失败", e);
        }
    }

    // ============================================================
    // Users
    // ============================================================

    @Override
    public User getUserByLogin(String userName) {
        String sql = "SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapUser(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询用户失败", e);
        }
    }

    @Override
    public User getUserByName(String userName) {
        String sql = "SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users WHERE user_name = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapUser(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询用户失败", e);
        }
    }

    @Override
    public List<User> listUsers() {
        String sql = "SELECT uid, user_name, user_pwd, role, note, pwd_expires_at FROM users ORDER BY uid";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                List<User> list = new ArrayList<>();
                while (rs.next()) {
                    list.add(mapUser(rs));
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询用户列表失败", e);
        }
    }

    @Override
    public void createUser(String userName, String userPwd, int role, String note) {
        String sql = "INSERT INTO users (user_name, user_pwd, role, note) VALUES (?, ?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            ps.setString(2, userPwd);
            ps.setInt(3, role);
            ps.setString(4, note);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("创建用户失败", e);
        }
    }

    @Override
    public void deleteUserByName(String userName) {
        execUpdate("DELETE FROM users WHERE user_name = ?", userName);
    }

    @Override
    public void resetPassword(String userName, String pwdHash) {
        execUpdate("UPDATE users SET user_pwd = ? WHERE user_name = ?", pwdHash, userName);
    }

    @Override
    public void updatePassword(String userName, String newPwdHash) {
        execUpdate("UPDATE users SET user_pwd = ?, pwd_expires_at = '' WHERE user_name = ?", newPwdHash, userName);
    }

    @Override
    public void clearPwdExpiry(String userName) {
        execUpdate("UPDATE users SET pwd_expires_at = '' WHERE user_name = ?", userName);
    }

    // ============================================================
    // API tokens
    // ============================================================

    @Override
    public void saveApiToken(String userName, String tokenPreview, LocalDateTime expiresAt) {
        String sql = "INSERT INTO api_tokens (user_name, token_preview, expires_at) VALUES (?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            ps.setString(2, tokenPreview);
            ps.setString(3, expiresAt.toString());
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("保存 API token 失败", e);
        }
    }

    @Override
    public List<ApiToken> getUserApiTokens(String userName) {
        String sql = "SELECT id, user_name, token_preview, expires_at, created_at FROM api_tokens WHERE user_name = ? AND expires_at > datetime('now') ORDER BY created_at DESC";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            try (ResultSet rs = ps.executeQuery()) {
                List<ApiToken> list = new ArrayList<>();
                while (rs.next()) {
                    ApiToken t = new ApiToken();
                    t.setId(rs.getLong("id"));
                    t.setUserName(rs.getString("user_name"));
                    t.setTokenPreview(rs.getString("token_preview"));
                    t.setExpiresAt(LocalDateTime.parse(rs.getString("expires_at")));
                    t.setCreateTime(LocalDateTime.parse(rs.getString("created_at")));
                    list.add(t);
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询 API token 失败", e);
        }
    }

    // ============================================================
    // API call logs
    // ============================================================

    @Override
    public void saveApiCallLog(String userName, String apiPath, String method,
                               String reqBody, String respBody, int statusCode, String errMsg) {
        String sql = "INSERT INTO api_call_log (user_name, api_path, method, request_body, response_body, status_code, error_msg) VALUES (?, ?, ?, ?, ?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            ps.setString(2, apiPath);
            ps.setString(3, method);
            ps.setString(4, reqBody);
            ps.setString(5, respBody);
            ps.setInt(6, statusCode);
            ps.setString(7, errMsg);
            ps.executeUpdate();
        } catch (SQLException e) {
            log.error("保存 API 调用日志失败", e);
        }
    }

    @Override
    public List<ApiCallLog> getUserApiCallLogs(String userName) {
        String sql = "SELECT id, user_name, api_path, method, request_body, response_body, status_code, error_msg, created_at FROM api_call_log WHERE user_name = ? ORDER BY created_at DESC LIMIT 100";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, userName);
            try (ResultSet rs = ps.executeQuery()) {
                List<ApiCallLog> list = new ArrayList<>();
                while (rs.next()) {
                    ApiCallLog l = new ApiCallLog();
                    l.setId(rs.getLong("id"));
                    l.setUserName(rs.getString("user_name"));
                    l.setApiPath(rs.getString("api_path"));
                    l.setMethod(rs.getString("method"));
                    l.setRequestBody(rs.getString("request_body"));
                    l.setResponseBody(rs.getString("response_body"));
                    l.setStatusCode(rs.getInt("status_code"));
                    l.setErrorMsg(rs.getString("error_msg"));
                    l.setCreateTime(LocalDateTime.parse(rs.getString("created_at")));
                    list.add(l);
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询 API 调用日志失败", e);
        }
    }

    // ============================================================
    // Agent CRUD
    // ============================================================

    @Override
    public long createAgent(AgentDef a) {
        String sql = "INSERT INTO agent_def (name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids) VALUES (?, ?, ?, ?, ?, ?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, a.getName());
            ps.setString(2, a.getDescription());
            ps.setString(3, a.getSystemPrompt());
            ps.setString(4, a.getModelName());
            setDouble(ps, 5, a.getTemperature());
            setDouble(ps, 6, a.getTopP());
            setInt(ps, 7, a.getMaxTokens());
            ps.setString(8, a.getVdbIds());
            ps.executeUpdate();
            try (ResultSet rs = ps.getGeneratedKeys()) {
                return rs.next() ? rs.getLong(1) : 0;
            }
        } catch (SQLException e) {
            throw new RuntimeException("创建智能体失败", e);
        }
    }

    @Override
    public AgentDef getAgent(long id) {
        String sql = "SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at FROM agent_def WHERE id = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, id);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapAgent(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询智能体失败", e);
        }
    }

    @Override
    public List<AgentDef> listAgents() {
        String sql = "SELECT id, name, description, system_prompt, model_name, temperature, top_p, max_tokens, vdb_ids, created_at, updated_at FROM agent_def ORDER BY id";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                List<AgentDef> list = new ArrayList<>();
                while (rs.next()) {
                    list.add(mapAgent(rs));
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询智能体列表失败", e);
        }
    }

    @Override
    public void updateAgent(AgentDef a) {
        String sql = "UPDATE agent_def SET name=?, description=?, system_prompt=?, model_name=?, temperature=?, top_p=?, max_tokens=?, vdb_ids=?, updated_at=CURRENT_TIMESTAMP WHERE id=?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, a.getName());
            ps.setString(2, a.getDescription());
            ps.setString(3, a.getSystemPrompt());
            ps.setString(4, a.getModelName());
            setDouble(ps, 5, a.getTemperature());
            setDouble(ps, 6, a.getTopP());
            setInt(ps, 7, a.getMaxTokens());
            ps.setString(8, a.getVdbIds());
            ps.setLong(9, a.getId());
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("更新智能体失败", e);
        }
    }

    @Override
    public void deleteAgent(long id) {
        execUpdate("DELETE FROM agent_def WHERE id = ?", id);
    }

    // ============================================================
    // Workflow CRUD
    // ============================================================

    @Override
    public long createWorkflow(WorkflowDef w) {
        try {
            String nodesJson = MAPPER.writeValueAsString(w.getNodes());
            String classifierJson = w.getClassifier() != null ? MAPPER.writeValueAsString(w.getClassifier()) : "";

            String sql = "INSERT INTO workflow_def (name, description, classifier, nodes) VALUES (?, ?, ?, ?)";
            try (Connection conn = ds.getConnection();
                 PreparedStatement ps = conn.prepareStatement(sql)) {
                ps.setString(1, w.getName());
                ps.setString(2, w.getDescription());
                ps.setString(3, classifierJson);
                ps.setString(4, nodesJson);
                ps.executeUpdate();
                try (ResultSet rs = ps.getGeneratedKeys()) {
                    return rs.next() ? rs.getLong(1) : 0;
                }
            }
        } catch (JsonProcessingException e) {
            throw new RuntimeException("序列化工作流失败", e);
        } catch (SQLException e) {
            throw new RuntimeException("创建工作流失败", e);
        }
    }

    @Override
    public WorkflowDef getWorkflow(long id) {
        String sql = "SELECT id, name, description, classifier, nodes, created_at, updated_at FROM workflow_def WHERE id = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, id);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? mapWorkflow(rs) : null;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询工作流失败", e);
        }
    }

    @Override
    public List<WorkflowDef> listWorkflows() {
        String sql = "SELECT id, name, description, classifier, nodes, created_at, updated_at FROM workflow_def ORDER BY id";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                List<WorkflowDef> list = new ArrayList<>();
                while (rs.next()) {
                    list.add(mapWorkflow(rs));
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询工作流列表失败", e);
        }
    }

    @Override
    public void updateWorkflow(WorkflowDef w) {
        try {
            String nodesJson = MAPPER.writeValueAsString(w.getNodes());
            String classifierJson = w.getClassifier() != null ? MAPPER.writeValueAsString(w.getClassifier()) : "";

            String sql = "UPDATE workflow_def SET name=?, description=?, classifier=?, nodes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?";
            try (Connection conn = ds.getConnection();
                 PreparedStatement ps = conn.prepareStatement(sql)) {
                ps.setString(1, w.getName());
                ps.setString(2, w.getDescription());
                ps.setString(3, classifierJson);
                ps.setString(4, nodesJson);
                ps.setLong(5, w.getId());
                ps.executeUpdate();
            }
        } catch (JsonProcessingException e) {
            throw new RuntimeException("序列化工作流失败", e);
        } catch (SQLException e) {
            throw new RuntimeException("更新工作流失败", e);
        }
    }

    @Override
    public void deleteWorkflow(long id) {
        execUpdate("DELETE FROM workflow_def WHERE id = ?", id);
    }

    // ============================================================
    // FAQ
    // ============================================================

    @Override
    public long createFaqEntry(String answer, String sourceFile) {
        String sql = "INSERT INTO faq_entries (answer, source_file) VALUES (?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, answer);
            ps.setString(2, sourceFile);
            ps.executeUpdate();
            try (ResultSet rs = ps.getGeneratedKeys()) {
                return rs.next() ? rs.getLong(1) : 0;
            }
        } catch (SQLException e) {
            throw new RuntimeException("创建 FAQ 条目失败", e);
        }
    }

    @Override
    public long createFaqQuestion(long entryId, String question, String embeddingJson) {
        String sql = "INSERT INTO faq_questions (entry_id, question, embedding) VALUES (?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, entryId);
            ps.setString(2, question);
            ps.setString(3, embeddingJson);
            ps.executeUpdate();
            try (ResultSet rs = ps.getGeneratedKeys()) {
                return rs.next() ? rs.getLong(1) : 0;
            }
        } catch (SQLException e) {
            throw new RuntimeException("创建 FAQ 问题失败", e);
        }
    }

    @Override
    public List<FaqEntry> getFaqEntries() {
        String sql = "SELECT id, answer, source_file, created_at FROM faq_entries ORDER BY id";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                List<FaqEntry> entries = new ArrayList<>();
                while (rs.next()) {
                    FaqEntry e = new FaqEntry();
                    e.setId(rs.getLong("id"));
                    e.setAnswer(rs.getString("answer"));
                    e.setSourceFile(rs.getString("source_file"));
                    e.setCreatedAt(LocalDateTime.parse(rs.getString("created_at")));
                    e.setQuestions(getFaqQuestionsByEntryId(e.getId()));
                    entries.add(e);
                }
                return entries;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询 FAQ 列表失败", e);
        }
    }

    @Override
    public List<FaqQuestion> getFaqQuestionsByEntryId(long entryId) {
        String sql = "SELECT id, entry_id, question, created_at FROM faq_questions WHERE entry_id = ? ORDER BY id";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setLong(1, entryId);
            try (ResultSet rs = ps.executeQuery()) {
                List<FaqQuestion> list = new ArrayList<>();
                while (rs.next()) {
                    FaqQuestion q = new FaqQuestion();
                    q.setId(rs.getLong("id"));
                    q.setEntryId(rs.getLong("entry_id"));
                    q.setQuestion(rs.getString("question"));
                    q.setCreatedAt(LocalDateTime.parse(rs.getString("created_at")));
                    list.add(q);
                }
                return list;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询 FAQ 问题失败", e);
        }
    }

    @Override
    public List<FaqQuestionWithEmbedding> getAllFaqQuestionsWithEmbedding() {
        String sql = "SELECT id, entry_id, question, embedding FROM faq_questions";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                List<FaqQuestionWithEmbedding> list = new ArrayList<>();
                while (rs.next()) {
                    FaqQuestionWithEmbedding q = new FaqQuestionWithEmbedding();
                    q.setId(rs.getLong("id"));
                    q.setEntryId(rs.getLong("entry_id"));
                    q.setQuestion(rs.getString("question"));
                    String embJson = rs.getString("embedding");
                    if (embJson != null && !embJson.isEmpty()) {
                        List<Double> embList = MAPPER.readValue(embJson, List.class);
                        q.setEmbedding(embList.stream().mapToDouble(d -> d).toArray());
                    }
                    list.add(q);
                }
                return list;
            }
        } catch (Exception e) {
            throw new RuntimeException("查询 FAQ 向量失败", e);
        }
    }

    @Override
    public void deleteFaqEntry(long id) {
        try (Connection conn = ds.getConnection()) {
            conn.setAutoCommit(false);
            try {
                execUpdate(conn, "DELETE FROM faq_questions WHERE entry_id = ?", id);
                execUpdate(conn, "DELETE FROM faq_entries WHERE id = ?", id);
                conn.commit();
            } catch (SQLException e) {
                conn.rollback();
                throw e;
            } finally {
                conn.setAutoCommit(true);
            }
        } catch (SQLException e) {
            throw new RuntimeException("删除 FAQ 失败", e);
        }
    }

    @Override
    public void updateFaqEntry(long id, String answer) {
        execUpdate("UPDATE faq_entries SET answer = ? WHERE id = ?", answer, id);
    }

    @Override
    public void deleteFaqQuestionsByEntryId(long entryId) {
        execUpdate("DELETE FROM faq_questions WHERE entry_id = ?", entryId);
    }

    @Override
    public void clearAllFaq() {
        try (Connection conn = ds.getConnection()) {
            conn.setAutoCommit(false);
            try {
                execUpdate(conn, "DELETE FROM faq_questions");
                execUpdate(conn, "DELETE FROM faq_entries");
                conn.commit();
            } catch (SQLException e) {
                conn.rollback();
                throw e;
            } finally {
                conn.setAutoCommit(true);
            }
        } catch (SQLException e) {
            throw new RuntimeException("清空 FAQ 失败", e);
        }
    }

    // ============================================================
    // System config
    // ============================================================

    @Override
    public String getConfig(String key) {
        String sql = "SELECT config_value FROM sys_config WHERE config_key = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, key);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? rs.getString("config_value") : "";
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询配置失败", e);
        }
    }

    @Override
    public void setConfig(String key, String value, String description) {
        String sql = "INSERT INTO sys_config (config_key, config_value, description, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP) ON CONFLICT(config_key) DO UPDATE SET config_value = excluded.config_value, description = excluded.description, updated_at = CURRENT_TIMESTAMP";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, key);
            ps.setString(2, value);
            ps.setString(3, description != null ? description : "");
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("保存配置失败", e);
        }
    }

    @Override
    public Map<String, String> getAllConfigs() {
        String sql = "SELECT config_key, config_value FROM sys_config";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            try (ResultSet rs = ps.executeQuery()) {
                Map<String, String> map = new HashMap<>();
                while (rs.next()) {
                    map.put(rs.getString("config_key"), rs.getString("config_value"));
                }
                return map;
            }
        } catch (SQLException e) {
            throw new RuntimeException("查询所有配置失败", e);
        }
    }

    @Override
    public void seedDefaultConfigs() {
        int count = queryInt("SELECT COUNT(*) FROM sys_config");
        if (count > 0) return;

        for (var entry : DEFAULT_CONFIGS) {
            setConfig(entry.key, entry.value, entry.desc);
        }
    }

    // ============================================================
    // Chat sessions (persistence) — TODO: 后续迁移至 Redis
    // ============================================================

    @Override
    public void saveChatMessage(String uid, String role, String content) {
        String sql = "INSERT INTO chat_sessions (uid, role, content) VALUES (?, ?, ?)";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, uid);
            ps.setString(2, role);
            ps.setString(3, content);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("保存聊天消息失败: " + e.getMessage(), e);
        }
    }

    @Override
    public List<ChatMessage> getChatMessages(String uid, int limit) {
        String sql = "SELECT role, content FROM chat_sessions WHERE uid = ? ORDER BY created_at DESC LIMIT ?";
        List<ChatMessage> msgs = new ArrayList<>();
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, uid);
            ps.setInt(2, limit);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    msgs.add(new ChatMessage(rs.getString("role"), rs.getString("content")));
                }
            }
        } catch (SQLException e) {
            throw new RuntimeException("获取聊天消息失败: " + e.getMessage(), e);
        }
        // Reverse to time-ascending order
        java.util.Collections.reverse(msgs);
        return msgs;
    }

    @Override
    public void clearChatMessages(String uid) {
        String sql = "DELETE FROM chat_sessions WHERE uid = ?";
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            ps.setString(1, uid);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("清空聊天记录失败: " + e.getMessage(), e);
        }
    }

    // ============================================================
    // Lifecycle
    // ============================================================

    @Override
    public void close() {
        ds.close();
    }

    public Connection getConnection() throws SQLException {
        return ds.getConnection();
    }

    // ============================================================
    // Static helpers
    // ============================================================

    private int execUpdate(String sql, Object... args) {
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql)) {
            for (int i = 0; i < args.length; i++) {
                ps.setObject(i + 1, args[i]);
            }
            return ps.executeUpdate();
        } catch (SQLException e) {
            throw new RuntimeException("SQL 执行失败: " + sql, e);
        }
    }

    private void execUpdate(Connection conn, String sql, Object... args) throws SQLException {
        try (PreparedStatement ps = conn.prepareStatement(sql)) {
            for (int i = 0; i < args.length; i++) {
                ps.setObject(i + 1, args[i]);
            }
            ps.executeUpdate();
        }
    }

    private int queryInt(String sql) {
        try (Connection conn = ds.getConnection();
             PreparedStatement ps = conn.prepareStatement(sql);
             ResultSet rs = ps.executeQuery()) {
            return rs.next() ? rs.getInt(1) : 0;
        } catch (SQLException e) {
            throw new RuntimeException("查询失败", e);
        }
    }

    private int queryInt(Connection conn, String sql) throws SQLException {
        try (PreparedStatement ps = conn.prepareStatement(sql);
             ResultSet rs = ps.executeQuery()) {
            return rs.next() ? rs.getInt(1) : 0;
        }
    }

    private void setDouble(PreparedStatement ps, int idx, Double value) throws SQLException {
        if (value != null) {
            ps.setDouble(idx, value);
        } else {
            ps.setNull(idx, Types.DOUBLE);
        }
    }

    private void setInt(PreparedStatement ps, int idx, Integer value) throws SQLException {
        if (value != null) {
            ps.setInt(idx, value);
        } else {
            ps.setNull(idx, Types.INTEGER);
        }
    }

    // ============================================================
    // Row mapping
    // ============================================================

    private VdbInfo mapVdbInfo(ResultSet rs) throws SQLException {
        VdbInfo v = new VdbInfo();
        v.setId(rs.getLong("id"));
        v.setName(rs.getString("name"));
        v.setUid(rs.getString("uid"));
        v.setPublic(rs.getInt("is_public") != 0);
        v.setDefault(rs.getInt("is_default") != 0);
        v.setCreateTime(rs.getString("created_at"));
        return v;
    }

    private List<VdbInfo> mapVdbInfoList(ResultSet rs) throws SQLException {
        List<VdbInfo> list = new ArrayList<>();
        while (rs.next()) {
            list.add(mapVdbInfo(rs));
        }
        return list;
    }

    private VdbFileInfo mapFileInfo(ResultSet rs) throws SQLException {
        VdbFileInfo f = new VdbFileInfo();
        f.setId(rs.getLong("id"));
        f.setName(rs.getString("name"));
        f.setUid(rs.getString("uid"));
        f.setVdbId(rs.getLong("vdb_id"));
        f.setTaskId(rs.getString("task_id"));
        f.setFilePath(rs.getString("file_path"));
        f.setPercent(rs.getDouble("percent"));
        f.setProcessInfo(rs.getString("process_info"));
        f.setFileMd5(rs.getString("file_md5"));
        f.setCreateTime(rs.getString("created_at"));
        return f;
    }

    private List<VdbFileInfo> mapFileInfoList(ResultSet rs) throws SQLException {
        List<VdbFileInfo> list = new ArrayList<>();
        while (rs.next()) {
            list.add(mapFileInfo(rs));
        }
        return list;
    }

    private User mapUser(ResultSet rs) throws SQLException {
        User u = new User();
        u.setUid(rs.getLong("uid"));
        u.setUserName(rs.getString("user_name"));
        u.setUserPwd(rs.getString("user_pwd"));
        u.setRole(rs.getInt("role"));
        u.setNote(rs.getString("note"));
        String expiresAt = rs.getString("pwd_expires_at");
        if (expiresAt != null && !expiresAt.isEmpty()) {
            try {
                u.setPwdExpiresAt(LocalDateTime.parse(expiresAt));
            } catch (Exception ignored) {}
        }
        return u;
    }

    private AgentDef mapAgent(ResultSet rs) throws SQLException {
        AgentDef a = new AgentDef();
        a.setId(rs.getLong("id"));
        a.setName(rs.getString("name"));
        a.setDescription(rs.getString("description"));
        a.setSystemPrompt(rs.getString("system_prompt"));
        a.setModelName(rs.getString("model_name"));
        double temp = rs.getDouble("temperature");
        if (!rs.wasNull()) a.setTemperature(temp);
        double topP = rs.getDouble("top_p");
        if (!rs.wasNull()) a.setTopP(topP);
        int maxTok = rs.getInt("max_tokens");
        if (!rs.wasNull()) a.setMaxTokens(maxTok);
        a.setVdbIds(rs.getString("vdb_ids"));
        a.setCreatedAt(LocalDateTime.parse(rs.getString("created_at")));
        a.setUpdatedAt(LocalDateTime.parse(rs.getString("updated_at")));
        return a;
    }

    private WorkflowDef mapWorkflow(ResultSet rs) throws SQLException {
        WorkflowDef w = new WorkflowDef();
        w.setId(rs.getLong("id"));
        w.setName(rs.getString("name"));
        w.setDescription(rs.getString("description"));
        String classifierJson = rs.getString("classifier");
        if (classifierJson != null && !classifierJson.isEmpty()) {
            try {
                w.setClassifier(MAPPER.readValue(classifierJson, ClassifierDef.class));
            } catch (Exception ignored) {}
        }
        String nodesJson = rs.getString("nodes");
        if (nodesJson != null && !nodesJson.isEmpty()) {
            try {
                w.setNodes(MAPPER.readValue(nodesJson, MAPPER.getTypeFactory().constructCollectionType(List.class, WorkflowNode.class)));
            } catch (Exception ignored) {}
        }
        w.setCreatedAt(LocalDateTime.parse(rs.getString("created_at")));
        w.setUpdatedAt(LocalDateTime.parse(rs.getString("updated_at")));
        return w;
    }

    // ============================================================
    // Constants
    // ============================================================

    /**
     * Default agent system prompt for workflow engine (uses {{sys.xxx}} variable syntax).
     */
    public static final String DEFAULT_AGENT_PROMPT = """
        你是专业的对话机器人，负责解答客户咨询。你必须基于以下知识库信息回答用户问题。
        如果知识库中没有相关信息，请引导用户转接人工客服。

        今日日期：{{sys.cur_date}}（星期{{sys.cur_week}}）

        知识库内容：
        ---
        {{sys.kb_context}}
        ---

        历史对话：
        {{sys.history}}

        用户问题：{{sys.user_query}}

        请用亲切、专业的中文回答：""";

    /**
     * Default chat prompt for simple (non-workflow) chat mode (uses {xxx} syntax).
     */
    public static final String DEFAULT_CHAT_PROMPT = """
        你是专业的对话机器人，负责解答客户咨询。你必须基于以下知识库信息回答用户问题。
        如果知识库中没有相关信息，请引导用户转接人工客服。

        今日日期：{cur_date}（星期{cur_week}）

        知识库内容：
        ---
        {context}
        ---

        历史对话：
        {history}

        用户问题：{question}

        请用亲切、专业的中文回答：""";

    static final DefaultConfig[] DEFAULT_CONFIGS = {
        new DefaultConfig("sys.api_auth", "true", "是否启用接口认证 (true/false)"),
        new DefaultConfig("kb.chunk_size", "300", "文本分片大小（字符数）"),
        new DefaultConfig("kb.chunk_overlap", "80", "文本分片重叠大小（字符数）"),
        new DefaultConfig("kb.top_k", "3", "检索返回条数"),
        new DefaultConfig("kb.score_threshold", "0.1", "检索相似度阈值"),
        new DefaultConfig("kb.rerank_enabled", "false", "是否启用 Rerank 重排序"),
        new DefaultConfig("kb.rerank_retrieve_n", "15", "Rerank 预检索条数"),
        new DefaultConfig("llm.temperature", "0.7", "LLM 温度参数 (0-2)"),
        new DefaultConfig("llm.top_p", "0.9", "LLM Top-P 采样参数 (0-1)"),
        new DefaultConfig("llm.max_tokens", "2048", "LLM 最大生成 Token 数"),
        new DefaultConfig("faq.match_threshold", "0.85", "FAQ 匹配阈值 (0~1)"),
    };

    record DefaultConfig(String key, String value, String desc) {}
}