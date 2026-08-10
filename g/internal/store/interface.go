package store

import (
	"time"

	"kb-chat-flow/internal/model"
)

// MetaStore 元数据存储接口，支持 SQLite 和 MySQL 两种实现
type MetaStore interface {
	// ============================================================
	// 知识库 (vdb_info) CRUD
	// ============================================================

	CreateVdb(name, uid string, isPublic bool) (int64, error)
	GetVdbByID(id int64) (*model.VdbInfo, error)
	GetUserVdbs(uid string) ([]model.VdbInfo, error)
	GetPublicVdbs(excludeUID string) ([]model.VdbInfo, error)
	DeleteVdb(id int64) error
	SetDefaultVdb(id int64, uid string) error
	CheckVdbNameExists(name, uid string) (bool, error)
	GetDefaultVdbID(uid string) (int64, error)

	// ============================================================
	// 文件 (vdb_file_info) CRUD
	// ============================================================

	CreateFileInfo(info *model.VdbFileInfo) (int64, error)
	GetFilesByVdbID(vdbID int64) ([]model.VdbFileInfo, error)
	GetFileByID(id int64) (*model.VdbFileInfo, error)
	GetUnprocessedFiles() ([]model.VdbFileInfo, error)
	UpdateFileProgress(id int64, percent float64, info string) error
	DeleteFile(id int64) error
	CheckFileMD5Exists(vdbID int64, md5 string) (*model.VdbFileInfo, error)

	// ============================================================
	// 提示词模板
	// ============================================================

	GetPrompt(name string) (string, error)
	UpsertPrompt(name, value string, uid int) error

	// ============================================================
	// 用户 (users)
	// ============================================================

	GetUserByLogin(userName string) (*model.User, error)
	GetUserByName(userName string) (*model.User, error)
	ListUsers() ([]model.User, error)
	CreateUser(userName string, userPwdHash string, role int, note string) error
	DeleteUserByName(userName string) error
	ResetPassword(userName string, userPwdHash string) error
	UpdatePassword(userName string, newPwdHash string) error

	// ============================================================
	// API Token
	// ============================================================

	SaveApiToken(userName, tokenPreview string, expiresAt time.Time) error
	GetUserApiTokens(userName string) ([]model.ApiToken, error)

	// ============================================================
	// API 调用日志
	// ============================================================

	SaveApiCallLog(userName, apiPath, method, reqBody, respBody string, statusCode int, errMsg string) error
	GetUserApiCallLogs(userName string) ([]model.ApiCallLog, error)

	// ============================================================
	// Agent (agent_def) CRUD
	// ============================================================

	CreateAgent(a *model.AgentDef) (int64, error)
	GetAgent(id int64) (*model.AgentDef, error)
	ListAgents() ([]model.AgentDef, error)
	UpdateAgent(a *model.AgentDef) error
	DeleteAgent(id int64) error

	// ============================================================
	// Workflow (workflow_def) CRUD
	// ============================================================

	CreateWorkflow(w *model.WorkflowDef) (int64, error)
	GetWorkflow(id int64) (*model.WorkflowDef, error)
	ListWorkflows() ([]model.WorkflowDef, error)
	UpdateWorkflow(w *model.WorkflowDef) error
	DeleteWorkflow(id int64) error

	// ============================================================
	// FAQ 条目管理
	// ============================================================

	CreateFaqEntry(answer, sourceFile string) (int64, error)
	CreateFaqQuestion(entryID int64, question, embeddingJSON string) (int64, error)
	GetFaqEntries() ([]model.FaqEntry, error)
	GetFaqQuestionsByEntryID(entryID int64) ([]model.FaqQuestion, error)
	GetAllFaqQuestionsWithEmbedding() ([]model.FaqQuestionWithEmbedding, error)
	DeleteFaqEntry(id int64) error
	UpdateFaqEntry(id int64, answer string) error
	DeleteFaqQuestionsByEntryID(entryID int64) error
	ClearAllFaq() error

	// ============================================================
	// 系统配置 (sys_config)
	// ============================================================

	GetConfig(key string) (string, error)
	SetConfig(key, value, description string) error
	GetAllConfigs() (map[string]string, error)
	SeedDefaultConfigs(sysName string) error

	// ============================================================
	// 会话历史 (chat_sessions) — 持久化聊天记录
	// ============================================================

	SaveChatMessage(uid, role, content string) error
	GetChatMessages(uid string, limit int) ([]model.ChatMessage, error)
	ClearChatMessages(uid string) error

	// ============================================================
	// 生命周期
	// ============================================================

	Close() error
}
