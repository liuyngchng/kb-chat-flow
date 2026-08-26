package model

import "time"

// ============================================================
// 配置相关
// ============================================================

// ServerMode 服务运行模式
type ServerMode string

const (
	ModeSingleton ServerMode = "singleton" // 单例模式：内存存储 + SQLite + 本地文件
	ModeCluster   ServerMode = "cluster"   // 集群模式：Redis + MySQL + Milvus/Qdrant + 共享存储
)

// ServerRole 服务角色（集群模式下区分路由面）
type ServerRole string

const (
	SvcRoleAll   ServerRole = "all"   // 全功能（默认，兼容现有行为）
	SvcRoleAdmin ServerRole = "admin" // 仅管理面：系统管理 + 文件处理 Worker
	SvcRoleChat  ServerRole = "chat"  // 仅用户面：聊天 API
)

// Config 应用配置
type Config struct {
	Server ServerConfig `yaml:"server"`
	Sys    SysConfig    `yaml:"sys"`
	API    APIConfig    `yaml:"api"`
	Store  StoreConfig  `yaml:"store"`
	Vector VectorConfig `yaml:"vector"`
	Milvus MilvusConfig `yaml:"milvus"`
	Qdrant QdrantConfig `yaml:"qdrant"`
	MySQL  MySQLConfig  `yaml:"mysql"`
	Redis  RedisConfig  `yaml:"redis"`
	OSS    OSSConfig    `yaml:"oss"`
	KB     KBConfig     `yaml:"kb"`
	LLM    LLMParams    `yaml:"llm"`
	Faq    FaqConfig    `yaml:"faq"`
	// Prompts 从数据库加载，不再从 YAML 读取
}

// FaqConfig FAQ 匹配参数
type FaqConfig struct {
	MatchThreshold float64 `json:"match_threshold"`
}

// StoreConfig 元数据存储配置
type StoreConfig struct {
	Backend string `yaml:"backend"` // "sqlite" (默认) 或 "mysql"
}

// VectorConfig 向量存储配置
type VectorConfig struct {
	Backend string `yaml:"backend"` // "local" (默认), "milvus", "qdrant"
}

// KBConfig 知识库参数配置
type KBConfig struct {
	ChunkSize       int     `json:"chunk_size"`
	ChunkOverlap    int     `json:"chunk_overlap"`
	TopK            int     `json:"top_k"`
	ScoreThreshold  float64 `json:"score_threshold"`
	RerankEnabled   bool    `json:"rerank_enabled"`
	RerankRetrieveN int     `json:"rerank_retrieve_n"` // 预检索条数，rerank 后再取 TopK
}

// LLMParams LLM 模型参数配置
type LLMParams struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port        int        `yaml:"port"`
	Debug       bool       `yaml:"debug"`
	Mode        ServerMode `yaml:"mode"`         // "singleton" (默认) 或 "cluster"
	Role        ServerRole `yaml:"role"`         // "all" (默认) | "admin" | "chat"
	TokenSecret string     `yaml:"token_secret"` // HMAC 签名密钥，多节点部署时需一致
}

// 工作模式枚举
const (
	WorkModeKB      = 0 // 知识库问答（FAQ + 检索 + LLM）
	WorkModeCSM     = 1 // CSM 硬编码工作流
	WorkModeDynamic = 2 // 动态加载数据库工作流配置
)

// SysConfig 系统配置
type SysConfig struct {
	Name              string `yaml:"name"`
	Auth              bool   `yaml:"auth"`
	ApiAuth           bool   `yaml:"api_auth"`
	WorkMode          int    `json:"work_mode"`           // 工作模式: 0=KB, 1=CSM, 2=动态工作流
	DefaultWorkflowID int64  `json:"default_workflow_id"` // 动态工作流模式下使用的工作流 ID
}

// APIConfig API 配置
type APIConfig struct {
	LLMAPIURI          string `yaml:"llm_api_uri"`
	LLMAPIKey          string `yaml:"llm_api_key"`
	LLMModelName       string `yaml:"llm_model_name"`
	EmbeddingAPIURI    string `yaml:"embedding_api_uri"`
	EmbeddingAPIKey    string `yaml:"embedding_api_key"`
	EmbeddingModelName string `yaml:"embedding_model_name"`
	RerankAPIURI       string `yaml:"rerank_api_uri"`
	RerankAPIKey       string `yaml:"rerank_api_key"`
	RerankModelName    string `yaml:"rerank_model_name"`
}

// MilvusConfig Milvus 配置
// 认证二选一：
//   - token:            API Key 认证（云服务商提供的 token）
//   - username/password: 用户名/密码认证（自建 Milvus 默认 root + 密码）
//
// 两者都留空则走免认证连接。
type MilvusConfig struct {
	URI      string `yaml:"uri"`
	Token    string `yaml:"token"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// QdrantConfig Qdrant 向量数据库配置
type QdrantConfig struct {
	Host   string `yaml:"host"`    // 例如 "localhost"
	Port   int    `yaml:"port"`    // 例如 6334 (gRPC 端口)
	APIKey string `yaml:"api_key"` // 可选，认证用
	UseTLS bool   `yaml:"use_tls"` // 是否启用 TLS
}

// MySQLConfig MySQL 数据库配置
type MySQLConfig struct {
	DSN string `yaml:"dsn"` // 例如 "user:password@tcp(127.0.0.1:3306)/kb-chat-flow?charset=utf8mb4&parseTime=True&loc=Local"
}

// RedisConfig Redis 配置（集群模式使用）
type RedisConfig struct {
	Addr     string `yaml:"addr"`     // 例如 "localhost:6379"
	Password string `yaml:"password"` // 密码，可选
	DB       int    `yaml:"db"`       // 数据库编号，默认 0
}

// OSSConfig 对象存储配置（集群模式使用）
type OSSConfig struct {
	Type      string `yaml:"type"` // "minio" | "s3" | "aliyun"
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
}

// ============================================================
// 知识库相关
// ============================================================

// VdbInfo 知识库元数据
type VdbInfo struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	UID        string    `json:"uid"`
	IsPublic   bool      `json:"is_public"`
	IsDefault  bool      `json:"is_default"`
	CreateTime time.Time `json:"create_time"`
}

// VdbFileInfo 知识库文件信息
type VdbFileInfo struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	UID         string    `json:"uid"`
	VdbID       int64     `json:"vdb_id"`
	TaskID      string    `json:"task_id"`
	FilePath    string    `json:"file_path"`
	Percent     float64   `json:"percent"`
	ProcessInfo string    `json:"process_info"`
	FileMD5     string    `json:"file_md5"`
	CreateTime  time.Time `json:"create_time"`
}

// ============================================================
// 聊天相关
// ============================================================

// ChatMessage 聊天消息
type ChatMessage struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Msg       string `form:"msg" json:"msg" binding:"required"`
	UID       string `form:"uid" json:"uid"`
	AppSource string `form:"app_source" json:"app_source"`
}

// LoginRequest 登录请求
type LoginRequest struct {
	UserName string `json:"user_name" binding:"required"`
	UserPwd  string `json:"user_pwd" binding:"required"`
}

// VdbCreateRequest 创建知识库请求
type VdbCreateRequest struct {
	Name     string `json:"name" binding:"required"`
	IsPublic bool   `json:"is_public"`
}

// VdbSearchRequest 知识库搜索请求
type VdbSearchRequest struct {
	VdbID  int64   `json:"vdb_id"`            // 单个知识库 ID（兼容旧版），与 VdbIDs 二选一
	VdbIDs []int64 `json:"vdb_ids,omitempty"` // 多个知识库 ID 列表，为空则搜索所有可访问知识库
	Query  string  `json:"query" binding:"required"`
}

// FaqMatchRequest FAQ 匹配请求
type FaqMatchRequest struct {
	Query string `json:"query" binding:"required"`
}

// ChatHistory 会话历史
type ChatHistory struct {
	UID       string        `json:"uid"`
	Messages  []ChatMessage `json:"messages"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// ============================================================
// LLM / Embedding 相关
// ============================================================

// ChatCompletionRequest OpenAI 兼容的聊天请求
type ChatCompletionRequest struct {
	Model       string              `json:"model"`
	Messages    []ChatCompletionMsg `json:"messages"`
	Stream      bool                `json:"stream"`
	Temperature *float64            `json:"temperature,omitempty"`
	TopP        *float64            `json:"top_p,omitempty"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
}

// ChatCompletionMsg 消息
type ChatCompletionMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionChunk 流式响应片段
type ChatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// EmbeddingRequest 向量化请求
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingResponse 向量化响应
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// RerankRequest 重排序请求 (OpenAI/Cohere 兼容格式)
type RerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

// RerankResponse 重排序响应
type RerankResponse struct {
	Results []RerankResult `json:"results"`
}

// RerankResult 单条重排序结果
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// ============================================================
// 向量存储相关
// ============================================================

// SearchResult 检索结果
type SearchResult struct {
	ID      string            `json:"id"`
	Content string            `json:"content"`
	Meta    map[string]string `json:"metadata"`
	Score   float64           `json:"score"`
}

// VectorRecord 向量记录（用于插入）
type VectorRecord struct {
	ID      string            `json:"id"`
	Vector  []float64         `json:"vector"`
	Content string            `json:"content"`
	Meta    map[string]string `json:"metadata"`
}

// ============================================================
// 系统配置相关
// ============================================================

// ============================================================
// 用户相关
// ============================================================

// Role 常量
const (
	RoleNormal = 0 // 普通用户
	RoleAdmin  = 1 // 管理员
	RoleAgent  = 2 // 客服座席
	RoleAPI    = 3 // API 调用用户
)

// User 用户信息
type User struct {
	UID          int64     `json:"uid"`
	UserName     string    `json:"user_name"`
	UserPwd      string    `json:"-"` // bcrypt 哈希，不返回给客户端
	Role         int       `json:"role"`
	Note         string    `json:"note"`
	PwdExpiresAt time.Time `json:"-"` // 密码过期时间，零值 = 无过期限制
}

// ApiToken API 调用 token 记录
type ApiToken struct {
	ID           int64     `json:"id"`
	UserName     string    `json:"user_name"`
	TokenPreview string    `json:"token_preview"`
	ExpiresAt    time.Time `json:"expires_at"`
	CreateTime   time.Time `json:"create_time"`
}

// ApiCallLog API 调用记录
type ApiCallLog struct {
	ID           int64     `json:"id"`
	UserName     string    `json:"user_name"`
	APIPath      string    `json:"api_path"`
	Method       string    `json:"method"`
	RequestBody  string    `json:"request_body"`
	ResponseBody string    `json:"response_body"`
	StatusCode   int       `json:"status_code"`
	ErrorMsg     string    `json:"error_msg"`
	CreateTime   time.Time `json:"create_time"`
}

// ============================================================
// API 请求结构体
// ============================================================

// CreateUserRequest 管理员创建用户请求
type CreateUserRequest struct {
	UserName string `json:"user_name" binding:"required"`
	UserPwd  string `json:"user_pwd" binding:"required"`
	Role     int    `json:"role"`
	Note     string `json:"note"`
}

// ResetPwdRequest 重置密码请求
type ResetPwdRequest struct {
	UserPwd string `json:"user_pwd" binding:"required"`
}

// ChangePwdRequest 修改自己密码请求
type ChangePwdRequest struct {
	OldPwd string `json:"old_pwd" binding:"required"`
	NewPwd string `json:"new_pwd" binding:"required"`
}

// RegisterRequest 用户自助注册请求
type RegisterRequest struct {
	UserName string `json:"user_name" binding:"required"`
	UserPwd  string `json:"user_pwd" binding:"required"`
}

// ConfigEntry 系统配置项（用于 API 响应）
type ConfigEntry struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// ============================================================
// 多智能体工作流相关
// ============================================================

// AgentDef 智能体定义
type AgentDef struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"` // 该 Agent 的系统提示词
	ModelName    string    `json:"model_name"`    // 可选，为空则用全局默认
	Temperature  *float64  `json:"temperature"`   // nil 表示用全局默认
	TopP         *float64  `json:"top_p"`
	MaxTokens    *int      `json:"max_tokens"`
	VdbIDs       string    `json:"vdb_ids"` // JSON 数组字符串, e.g. "[1,3,5]"
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateAgentRequest 创建/更新智能体请求
type CreateAgentRequest struct {
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	ModelName    string   `json:"model_name"`
	Temperature  *float64 `json:"temperature"`
	TopP         *float64 `json:"top_p"`
	MaxTokens    *int     `json:"max_tokens"`
	VdbIDs       []int64  `json:"vdb_ids"` // 前端传数组
}

// AgentListItem 列表项（不含完整 prompt）
type AgentListItem struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ModelName   string    `json:"model_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// WorkflowNode 工作流中的一个步骤节点
type WorkflowNode struct {
	ID            string     `json:"id"`                       // 节点唯一 ID，前端生成
	AgentID       int64      `json:"agent_id"`                 // 引用哪个 Agent
	AgentName     string     `json:"agent_name"`               // 冗余展示用
	InputTemplate string     `json:"input_template"`           // 输入模板，用 {{var}} 引用上游变量
	OutputVar     string     `json:"output_var"`               // 本节点输出变量名
	OrderIndex    int        `json:"order_index"`              // 执行顺序，0-based（线性模式使用）
	IsFinal       bool       `json:"is_final"`                 // 是否最终输出节点
	Condition     IntentType `json:"condition,omitempty"`      // 分类路由条件：匹配 Classifier 输出的 category name，空 = 无条件执行
	NextNodes     []string   `json:"next_nodes,omitempty"`     // DAG 模式：下游节点 ID 列表，空 = 线性模式
	ParallelGroup string     `json:"parallel_group,omitempty"` // DAG 模式：相同 group 的节点并行执行
}

// IntentType 意图分类枚举类型
type IntentType string

const (
	IntentEmergency IntentType = "emergency"
	IntentBilling   IntentType = "billing"
	IntentBusiness  IntentType = "business"
	IntentRepair    IntentType = "repair"
	IntentFaq       IntentType = "faq"
)

// ClassifySource 意图分类来源层级
type ClassifySource string

const (
	SourceKeyword  ClassifySource = "keyword"
	SourceFastText ClassifySource = "fasttext"
	SourceSemantic ClassifySource = "semantic"
	SourceLLM      ClassifySource = "llm"
	SourceFallback ClassifySource = "fallback"
)

// ClassifiedIntent 带置信度的意图分类结果
type ClassifiedIntent struct {
	Intent     IntentType     `json:"intent"`
	Confidence float64        `json:"confidence"` // 0.0~1.0
	Source     ClassifySource `json:"source"`
}

// IntentCategory 意图分类中的一个类别
type IntentCategory struct {
	Name        IntentType `json:"name"`        // 类别标识，如 "emergency"
	Description string     `json:"description"` // 类别描述，如 "燃气泄漏等紧急情况"
	Keywords    []string   `json:"keywords"`    // 关键词列表，如 ["漏气","燃气味","报警"]
}

// ClassifierDef 意图分类器定义
type ClassifierDef struct {
	Prompt     string           `json:"prompt"`     // LLM 分类 prompt
	OutputVar  string           `json:"output_var"` // 分类结果存到哪个变量，默认 "intent"
	Categories []IntentCategory `json:"categories"` // 类别列表
}

// WorkflowDef 工作流定义
type WorkflowDef struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Classifier  *ClassifierDef `json:"classifier,omitempty"` // 意图分类器，nil = 老式线性流程
	Nodes       []WorkflowNode `json:"nodes"`                // 步骤列表
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// CreateWorkflowRequest 创建工作流请求
type CreateWorkflowRequest struct {
	Name        string         `json:"name" binding:"required"`
	Description string         `json:"description"`
	Classifier  *ClassifierDef `json:"classifier,omitempty"`
	Nodes       []WorkflowNode `json:"nodes" binding:"required"`
}

// ============================================================
// FAQ 相关
// ============================================================

// FaqEntry FAQ 条目（一个答案 + 多个问法）
type FaqEntry struct {
	ID         int64         `json:"id"`
	Questions  []FaqQuestion `json:"questions"`
	Answer     string        `json:"answer"`
	SourceFile string        `json:"source_file"`
	CreatedAt  time.Time     `json:"created_at"`
}

// FaqQuestion FAQ 问题（带向量）
type FaqQuestion struct {
	ID        int64     `json:"id"`
	EntryID   int64     `json:"entry_id"`
	Question  string    `json:"question"`
	CreatedAt time.Time `json:"created_at"`
}

// FaqQuestionWithEmbedding 带向量的 FAQ 问题（内部使用）
type FaqQuestionWithEmbedding struct {
	ID        int64     `json:"id"`
	EntryID   int64     `json:"entry_id"`
	Question  string    `json:"question"`
	Embedding []float64 `json:"embedding"`
}

// CreateFaqRequest 创建 FAQ 条目请求
type CreateFaqRequest struct {
	Questions []string `json:"questions" binding:"required"` // 至少一个问法
	Answer    string   `json:"answer" binding:"required"`
}

// UpdateFaqRequest 更新 FAQ 条目请求
type UpdateFaqRequest struct {
	Questions []string `json:"questions" binding:"required"`
	Answer    string   `json:"answer" binding:"required"`
}
