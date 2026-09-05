package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"kb-chat-flow/internal/config"
	"kb-chat-flow/internal/engine"
	"kb-chat-flow/internal/kb"
	"kb-chat-flow/internal/model"
	"kb-chat-flow/internal/store"

	"github.com/gin-gonic/gin"
)

// VdbHandler 知识库管理 API 处理器
type VdbHandler struct {
	cfg      *model.Config
	kbMgr    *kb.Manager
	store    store.MetaStore
	engine   *engine.Engine
	notifier config.ChangeNotifier
}

// NewVdbHandler 创建知识库处理器
func NewVdbHandler(cfg *model.Config, kbMgr *kb.Manager, metaStore store.MetaStore, eng *engine.Engine) *VdbHandler {
	return &VdbHandler{
		cfg:    cfg,
		kbMgr:  kbMgr,
		store:  metaStore,
		engine: eng,
	}
}

// SetNotifier 注入配置变更通知器
func (h *VdbHandler) SetNotifier(n config.ChangeNotifier) {
	h.notifier = n
}

// MyList 获取用户的知识库列表 GET /api/vdb
func (h *VdbHandler) MyList(c *gin.Context) {
	uid := getAuthUID(c)
	list, err := h.kbMgr.GetUserKBs(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []model.VdbInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// PubList 获取公开知识库列表 GET /api/vdb/pub
func (h *VdbHandler) PubList(c *gin.Context) {
	uid := getAuthUID(c)
	list, err := h.kbMgr.GetPublicKBs(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []model.VdbInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// FileList 获取知识库文件列表 GET /api/vdb/files?id=
func (h *VdbHandler) FileList(c *gin.Context) {
	vdbID := getQueryIntParam(c, "id")
	files, err := h.kbMgr.GetFiles(vdbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if files == nil {
		files = []model.VdbFileInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"data": files})
}

// SetDefault 设置默认知识库 PUT /api/vdb/default?id=
func (h *VdbHandler) SetDefault(c *gin.Context) {
	uid := getAuthUID(c)
	vdbID := getQueryIntParam(c, "id")
	if err := h.kbMgr.SetDefaultKB(vdbID, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Create 创建知识库 POST /api/vdb
func (h *VdbHandler) Create(c *gin.Context) {
	uid := getAuthUID(c)

	var req model.VdbCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "知识库名称不能为空"})
		return
	}

	id, err := h.kbMgr.CreateKB(req.Name, uid, req.IsPublic)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": id})
}

// Delete 删除知识库 DELETE /api/vdb?id=
func (h *VdbHandler) Delete(c *gin.Context) {
	uid := getAuthUID(c)
	vdbID := getQueryIntParam(c, "id")
	if err := h.kbMgr.DeleteKB(vdbID, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Upload 上传文件到知识库 POST /api/vdb/upload?id= (multipart/form-data)
func (h *VdbHandler) Upload(c *gin.Context) {
	uid := getAuthUID(c)
	vdbID := getQueryIntParam(c, "id")

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择文件"})
		return
	}

	// 检查文件类型
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowedExts := map[string]bool{
		".txt": true, ".md": true,
		".pdf": true, ".docx": true,
		".xlsx": true,
	}
	if !allowedExts[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的文件格式，支持: txt, md, pdf, docx, xlsx"})
		return
	}

	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "打开文件失败"})
		return
	}
	defer f.Close()

	finfo, err := h.kbMgr.UploadFile(vdbID, uid, file.Filename, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "file": finfo})
}

// ProcessInfo 获取文件处理进度 GET /api/vdb/file/progress?file_id=
func (h *VdbHandler) ProcessInfo(c *gin.Context) {
	fileID := getQueryIntParam(c, "file_id")
	finfo, err := h.store.GetFileByID(fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": finfo})
}

// Search 在知识库中检索 POST /api/vdb/search
func (h *VdbHandler) Search(c *gin.Context) {
	uid := getAuthUID(c)

	var req model.VdbSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query 不能为空"})
		return
	}

	// 优先级: vdb_ids > vdb_id > 搜索全部可访问知识库
	if len(req.VdbIDs) > 0 {
		result, err := h.kbMgr.SearchInKBs(req.Query, req.VdbIDs, uid, h.cfg.KB.TopK, h.cfg.KB.ScoreThreshold)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
		return
	}

	if req.VdbID > 0 {
		result, err := h.kbMgr.SearchInKB(req.Query, req.VdbID, uid, h.cfg.KB.TopK, h.cfg.KB.ScoreThreshold)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result})
		return
	}

	// 未指定知识库：搜索所有可访问的
	result := h.kbMgr.SearchAllKBs(req.Query, uid, h.cfg.KB.TopK, h.cfg.KB.ScoreThreshold)
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// Chunks 获取文件的分块列表 GET /api/vdb/file/chunks?file_id=
func (h *VdbHandler) Chunks(c *gin.Context) {
	fileID := getQueryIntParam(c, "file_id")
	if fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文件 ID"})
		return
	}

	// 鉴权：检查文件是否属于当前用户
	uid := getAuthUID(c)
	finfo, err := h.store.GetFileByID(fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if finfo == nil || finfo.UID != uid {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}

	chunks, err := h.kbMgr.GetFileChunks(fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if chunks == nil {
		chunks = []model.SearchResult{}
	}

	c.JSON(http.StatusOK, gin.H{"data": chunks})
}

// Download 下载文件 GET /api/vdb/file/download?file_id=
func (h *VdbHandler) Download(c *gin.Context) {
	fileID := getQueryIntParam(c, "file_id")
	if fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文件 ID"})
		return
	}

	uid := getAuthUID(c)
	finfo, err := h.store.GetFileByID(fileID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if finfo == nil || finfo.UID != uid {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}

	c.FileAttachment(finfo.FilePath, finfo.Name)
}

// FileDelete 删除文件 DELETE /api/vdb/file?file_id=
func (h *VdbHandler) FileDelete(c *gin.Context) {
	uid := getAuthUID(c)
	fileID := getQueryIntParam(c, "file_id")
	if err := h.kbMgr.DeleteFile(fileID, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// getQueryIntParam 从 URL query 参数中解析 int64
func getQueryIntParam(c *gin.Context, key string) int64 {
	val := c.Query(key)
	n, _ := strconv.ParseInt(val, 10, 64)
	return n
}

// getPathIntParam 从 URL 路径参数中解析 int64
func getPathIntParam(c *gin.Context, key string) int64 {
	val := c.Param(key)
	n, _ := strconv.ParseInt(val, 10, 64)
	return n
}

// ============================================================
// csm 业务分支知识库绑定（仅管理员）
// ============================================================

// csmBindingConfig csm 各分支绑定的知识库 id（JSON 数组字符串存储于 sys_config）
type csmBindingConfig struct {
	Billing []int64 `json:"billing"`
	Repair  []int64 `json:"repair"`
	Faq     []int64 `json:"faq"`
}

// BindingGet 获取 csm 各分支当前绑定的知识库 id GET /api/vdb/bindings
func (h *VdbHandler) BindingGet(c *gin.Context) {
	cfg := csmBindingConfig{
		Billing: h.engine.BillingVdbIDs(),
		Repair:  h.engine.RepairVdbIDs(),
		Faq:     h.engine.FaqVdbIDs(),
	}
	c.JSON(http.StatusOK, gin.H{"data": cfg})
}

// BindingPut 保存 csm 各分支绑定的知识库 id，并热加载生效 PUT /api/vdb/bindings
func (h *VdbHandler) BindingPut(c *gin.Context) {
	var req csmBindingConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	// 写库
	if err := h.store.SetConfig("csm.billing_vdb_ids", mustJSON(req.Billing), "账单分支检索的知识库 id"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败: " + err.Error()})
		return
	}
	if err := h.store.SetConfig("csm.repair_vdb_ids", mustJSON(req.Repair), "维修分支检索的知识库 id"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败: " + err.Error()})
		return
	}
	if err := h.store.SetConfig("csm.faq_vdb_ids", mustJSON(req.Faq), "FAQ分支检索的知识库 id"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败: " + err.Error()})
		return
	}

	// 热加载即时生效
	h.engine.ReloadVdbBindings()

	// 通知其他节点重新加载绑定
	if h.notifier != nil {
		h.notifier.NotifyChange()
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// mustJSON 将值序列化为 JSON 字符串（忽略错误，仅用于内部配置）
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}
