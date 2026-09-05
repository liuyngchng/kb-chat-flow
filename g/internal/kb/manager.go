package kb

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"

	"kb-chat-flow/internal/embedding"
	"kb-chat-flow/internal/model"
	redisclient "kb-chat-flow/internal/redis"
	"kb-chat-flow/internal/rerank"
	"kb-chat-flow/internal/store"
	"kb-chat-flow/internal/vdb"
)

const (
	VdbDir           = "./vdb"
	UploadDir        = "./upload_doc"
	FilePollInterval = 5 * time.Second
	workerLockKey    = "worker:file_processor:lock"
	workerLockTTL    = 60 * time.Second // 锁有效期，防止 worker 崩溃后死锁
)

// Manager 知识库管理器
type Manager struct {
	cfg          *model.Config
	store        store.MetaStore
	fileStore    FileStore
	embClient    *embedding.Client
	rerankClient *rerank.Client
	redisClient  *redisclient.Client // nil = 单例模式，非 nil = 集群模式（分布式锁）
	stopCh       chan struct{}
	mu           sync.RWMutex
	stores       map[int64]vdb.VectorStore // key: vdbID -> store
}

// NewManager 创建知识库管理器（单例模式）
func NewManager(cfg *model.Config, metaStore store.MetaStore) *Manager {
	return NewManagerWithStore(cfg, metaStore, NewLocalFileStore(), nil)
}

// NewManagerWithStore 创建知识库管理器（指定文件存储和 Redis 客户端）
func NewManagerWithStore(cfg *model.Config, metaStore store.MetaStore, fileStore FileStore, redisClient *redisclient.Client) *Manager {
	embClient := embedding.New(
		cfg.API.EmbeddingAPIURI,
		cfg.API.EmbeddingAPIKey,
		cfg.API.EmbeddingModelName,
	)

	var rerankClient *rerank.Client
	if cfg.API.RerankAPIURI != "" && cfg.API.RerankModelName != "" {
		rerankClient = rerank.New(
			cfg.API.RerankAPIURI,
			cfg.API.RerankAPIKey,
			cfg.API.RerankModelName,
		)
	}

	return &Manager{
		cfg:          cfg,
		store:        metaStore,
		fileStore:    fileStore,
		embClient:    embClient,
		rerankClient: rerankClient,
		redisClient:  redisClient,
		stopCh:       make(chan struct{}),
		stores:       make(map[int64]vdb.VectorStore),
	}
}

// getEmbClient 从当前配置创建 embedding 客户端（共享连接池，开销极低）
func (m *Manager) getEmbClient() *embedding.Client {
	return embedding.New(
		m.cfg.API.EmbeddingAPIURI,
		m.cfg.API.EmbeddingAPIKey,
		m.cfg.API.EmbeddingModelName,
	)
}

// ============================================================
// 知识库 CRUD
// ============================================================

// CreateKB 创建知识库
func (m *Manager) CreateKB(name, uid string, isPublic bool) (int64, error) {
	exists, err := m.store.CheckVdbNameExists(name, uid)
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, fmt.Errorf("知识库名称已存在: %s", name)
	}

	id, err := m.store.CreateVdb(name, uid, isPublic)
	if err != nil {
		return 0, err
	}

	// 初始化向量存储
	vs, err := m.getOrCreateStore(id)
	if err != nil {
		return 0, fmt.Errorf("初始化向量存储失败: %w", err)
	}

	// 探测 embedding 维度并初始化 collection
	dim, err := m.getEmbClient().Dimension()
	if err != nil {
		slog.Error("kb_create_dimension_probe_failed",
			"kbName", name,
			"embedURI", m.cfg.API.EmbeddingAPIURI,
			"embedModel", m.cfg.API.EmbeddingModelName,
			"error", err,
		)
		return 0, fmt.Errorf("探测 embedding 维度失败: %w", err)
	}
	if err := vs.EnsureCollection(dim); err != nil {
		return 0, fmt.Errorf("创建向量 collection 失败: %w", err)
	}

	return id, nil
}

// DeleteKB 删除知识库
func (m *Manager) DeleteKB(id int64, uid string) error {
	vdbInfo, err := m.store.GetVdbByID(id)
	if err != nil {
		return err
	}
	if vdbInfo == nil || vdbInfo.UID != uid {
		return fmt.Errorf("无权删除该知识库")
	}

	// 删除向量数据
	m.mu.Lock()
	if vs, ok := m.stores[id]; ok {
		vs.Purge()
		vs.Close()
		delete(m.stores, id)
	}
	m.mu.Unlock()

	// 删除文件记录中的文件
	files, _ := m.store.GetFilesByVdbID(id)
	for _, f := range files {
		m.fileStore.Delete(f.FilePath)
	}

	return m.store.DeleteVdb(id)
}

// GetUserKBs 获取用户的所有知识库
func (m *Manager) GetUserKBs(uid string) ([]model.VdbInfo, error) {
	return m.store.GetUserVdbs(uid)
}

// GetPublicKBs 获取公开知识库
func (m *Manager) GetPublicKBs(uid string) ([]model.VdbInfo, error) {
	return m.store.GetPublicVdbs(uid)
}

// SetDefaultKB 设置默认知识库
func (m *Manager) SetDefaultKB(id int64, uid string) error {
	return m.store.SetDefaultVdb(id, uid)
}

// ============================================================
// 文件管理
// ============================================================

// UploadFile 上传文件到知识库
func (m *Manager) UploadFile(vdbID int64, uid, fileName string, reader io.Reader) (*model.VdbFileInfo, error) {
	// 检查知识库是否存在且属于该用户
	vdbInfo, err := m.store.GetVdbByID(vdbID)
	if err != nil {
		return nil, err
	}
	if vdbInfo == nil || vdbInfo.UID != uid {
		return nil, fmt.Errorf("知识库不存在")
	}

	// 确保上传目录存在
	if err := m.fileStore.MkdirAll(UploadDir); err != nil {
		return nil, err
	}

	taskID := fmt.Sprintf("%d", time.Now().UnixNano())
	savedName := fmt.Sprintf("%s_%s", taskID, fileName)
	savedPath := filepath.Join(UploadDir, savedName)

	// 计算 MD5 同时写入文件存储
	hash := md5.New()
	tee := io.TeeReader(reader, hash)
	if _, err := m.fileStore.Save(savedPath, tee); err != nil {
		return nil, fmt.Errorf("保存文件失败: %w", err)
	}
	fileMD5 := fmt.Sprintf("%x", hash.Sum(nil))

	// 检查重复
	existing, err := m.store.CheckFileMD5Exists(vdbID, fileMD5)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// 删除旧文件
		m.DeleteFile(existing.ID, uid)
	}

	// 创建数据库记录
	finfo := &model.VdbFileInfo{
		Name:        fileName,
		UID:         uid,
		VdbID:       vdbID,
		TaskID:      taskID,
		FilePath:    savedPath,
		Percent:     0,
		ProcessInfo: "文件已上传，等待处理",
		FileMD5:     fileMD5,
	}

	id, err := m.store.CreateFileInfo(finfo)
	if err != nil {
		m.fileStore.Delete(savedPath)
		return nil, err
	}
	finfo.ID = id

	return finfo, nil
}

// GetFiles 获取知识库下的所有文件
func (m *Manager) GetFiles(vdbID int64) ([]model.VdbFileInfo, error) {
	return m.store.GetFilesByVdbID(vdbID)
}

// GetFileChunks 获取文件的所有 chunk（从向量数据库中按 source 查询）
func (m *Manager) GetFileChunks(fileID int64) ([]model.SearchResult, error) {
	finfo, err := m.store.GetFileByID(fileID)
	if err != nil {
		return nil, fmt.Errorf("获取文件信息失败: %w", err)
	}
	if finfo == nil {
		return nil, fmt.Errorf("文件不存在")
	}

	vs, err := m.getOrCreateStore(finfo.VdbID)
	if err != nil {
		return nil, fmt.Errorf("获取向量存储失败: %w", err)
	}

	absPath, err := filepath.Abs(finfo.FilePath)
	if err != nil {
		absPath = finfo.FilePath
	}

	chunks, err := vs.ListBySource(absPath)
	if err != nil {
		return nil, fmt.Errorf("查询 chunks 失败: %w", err)
	}
	return chunks, nil
}

// DeleteFile 删除文件
func (m *Manager) DeleteFile(fileID int64, uid string) error {
	finfo, err := m.store.GetFileByID(fileID)
	if err != nil {
		return err
	}
	if finfo == nil || finfo.UID != uid {
		return fmt.Errorf("文件不存在")
	}

	// 从向量库删除
	absPath, _ := filepath.Abs(finfo.FilePath)
	m.deleteVectorsBySource(finfo.VdbID, absPath)

	// 删除文件
	m.fileStore.Delete(finfo.FilePath)

	return m.store.DeleteFile(fileID)
}

// ============================================================
// 检索
// ============================================================

// SearchInKB 在单个知识库中检索
func (m *Manager) SearchInKB(query string, vdbID int64, uid string, topK int, scoreThreshold float64) (string, error) {
	vs, err := m.getOrCreateStore(vdbID)
	if err != nil {
		return "", err
	}

	// 计算 query 向量
	queryVec, err := m.getEmbClient().EmbedSingle(query)
	if err != nil {
		return "", fmt.Errorf("query embedding 失败: %w", err)
	}

	// 确定检索条数：如果启用 rerank，先多检索一些
	retrieveN := topK
	useRerank := m.cfg.KB.RerankEnabled && m.rerankClient != nil
	if useRerank {
		retrieveN = m.cfg.KB.RerankRetrieveN
		if retrieveN <= topK {
			retrieveN = topK * 3
		}
		if retrieveN > 50 {
			retrieveN = 50 // 上限 50 条
		}
	}

	results, err := vs.Search(queryVec, retrieveN, scoreThreshold)
	if err != nil {
		return "", err
	}

	// Rerank 重排序
	if useRerank && len(results) > topK {
		// 提取文档内容列表
		docs := make([]string, len(results))
		for i, r := range results {
			docs[i] = r.Content
		}

		rerankResults, err := m.rerankClient.Rerank(query, docs, topK)
		if err != nil {
			slog.Warn("kb_rerank_failed_fallback", "error", err)
			// 回退：使用原始顺序的前 topK 条
			results = results[:topK]
		} else {
			// 按 rerank 结果重新排序
			reordered := make([]model.SearchResult, 0, len(rerankResults))
			for _, rr := range rerankResults {
				if rr.Index >= 0 && rr.Index < len(results) {
					reordered = append(reordered, results[rr.Index])
				}
			}
			results = reordered
		}
	}

	var sb strings.Builder
	for _, r := range results {
		content := strings.ReplaceAll(r.Content, "\n", "")
		if strings.Contains(content, ".......................") {
			continue
		}
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// SearchInKBs 在指定的多个知识库中检索
func (m *Manager) SearchInKBs(query string, vdbIDs []int64, uid string, topK int, scoreThreshold float64) (string, error) {
	var allContext strings.Builder
	for _, vdbID := range vdbIDs {
		ctx, err := m.SearchInKB(query, vdbID, uid, topK, scoreThreshold)
		if err != nil {
			slog.Error("kb_search_in_kb_failed", "vdb_id", vdbID, "error", err)
			continue
		}
		if ctx != "" {
			// 获取知识库名称
			vdbInfo, _ := m.store.GetVdbByID(vdbID)
			kbName := fmt.Sprintf("KB_%d", vdbID)
			if vdbInfo != nil {
				kbName = vdbInfo.Name
			}
			allContext.WriteString(fmt.Sprintf("[%s]\n", kbName))
			allContext.WriteString(ctx)
		}
	}
	return allContext.String(), nil
}

// SearchAllKBs 在用户所有知识库中检索
func (m *Manager) SearchAllKBs(query string, uid string, topK int, scoreThreshold float64) string {
	kbList, err := m.store.GetUserVdbs(uid)
	if err != nil {
		slog.Error("kb_get_list_failed", "error", err)
		return ""
	}

	var allContext strings.Builder
	for _, kb := range kbList {
		ctx, err := m.SearchInKB(query, kb.ID, uid, topK, scoreThreshold)
		if err != nil {
			slog.Error("kb_search_all_kbs_failed", "kb", kb.Name, "error", err)
			continue
		}
		if ctx != "" {
			allContext.WriteString(fmt.Sprintf("[%s]\n", kb.Name))
			allContext.WriteString(ctx)
		}
	}

	return allContext.String()
}

// ============================================================
// 文档处理 Worker
// ============================================================

// StartFileWorker 启动后台文件处理。
// 单例模式：直接轮询处理。
// 集群模式：通过 Redis 分布式锁确保同一时间只有一个节点处理。
func (m *Manager) StartFileWorker() {
	slog.Info("kb_file_worker_started", "cluster_mode", m.redisClient != nil)
	ticker := time.NewTicker(FilePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			slog.Info("kb_file_worker_stopped")
			return
		case <-ticker.C:
			// 集群模式：获取分布式锁
			if m.redisClient != nil {
				if !m.tryAcquireWorkerLock() {
					continue // 其他节点正在处理，跳过
				}
				m.processPendingFiles()
				m.releaseWorkerLock()
			} else {
				m.processPendingFiles()
			}
		}
	}
}

// tryAcquireWorkerLock 尝试获取文件处理分布式锁。
func (m *Manager) tryAcquireWorkerLock() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ok, err := m.redisClient.SetNX(ctx, workerLockKey, hostname(), workerLockTTL)
	if err != nil {
		slog.Warn("kb_worker_lock_acquire_failed", "error", err)
		return false
	}
	return ok
}

// releaseWorkerLock 释放文件处理分布式锁。
func (m *Manager) releaseWorkerLock() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := m.redisClient.Del(ctx, workerLockKey); err != nil {
		slog.Warn("kb_worker_lock_release_failed", "error", err)
	}
}

// hostname 返回主机名（用于分布式锁标识）
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// StopFileWorker 停止 worker
func (m *Manager) StopFileWorker() {
	close(m.stopCh)
}

// processPendingFiles 处理待处理的文件
func (m *Manager) processPendingFiles() {
	files, err := m.store.GetUnprocessedFiles()
	if err != nil {
		slog.Error("kb_get_pending_files_failed", "error", err)
		return
	}

	for _, f := range files {
		if err := m.processFile(&f); err != nil {
			slog.Error("kb_process_file_failed", "file", f.Name, "error", err)
			m.store.UpdateFileProgress(f.ID, 0, fmt.Sprintf("处理失败: %v", err))
		}
	}
}

// processFile 处理单个文件（向量化入库）
func (m *Manager) processFile(finfo *model.VdbFileInfo) error {
	slog.Info("kb_process_file_start", "name", finfo.Name, "id", finfo.ID)
	m.store.UpdateFileProgress(finfo.ID, 1, "开始处理文档")

	// 集群模式（S3）：先下载到本地临时文件，处理完后清理
	localPath := finfo.FilePath
	var cleanup func()
	if _, isS3 := m.fileStore.(*S3FileStore); isS3 {
		tmpPath, cleanFn, err := m.fileStore.(*S3FileStore).DownloadToTemp(finfo.FilePath)
		if err != nil {
			return fmt.Errorf("下载文件到本地失败: %w", err)
		}
		localPath = tmpPath
		cleanup = cleanFn
		defer cleanup()
	}

	// 根据后缀提取文本：pdf/docx/xlsx 需要解析，txt/md 直接读
	ext := strings.ToLower(filepath.Ext(localPath))
	var text string
	var err error
	switch ext {
	case ".pdf", ".docx", ".xlsx", ".xls":
		text, err = extractAndSaveText(localPath, m.fileStore, finfo.FilePath)
		if err != nil {
			return fmt.Errorf("提取文本失败: %w", err)
		}
	default:
		content, err := m.fileStore.ReadAll(finfo.FilePath)
		if err != nil {
			return fmt.Errorf("读取文件失败: %w", err)
		}
		text = string(content)
	}
	if strings.TrimSpace(text) == "" {
		m.store.UpdateFileProgress(finfo.ID, 100, "文件内容为空")
		return nil
	}

	// 文本切分
	chunks := splitText(text, m.cfg.KB.ChunkSize, m.cfg.KB.ChunkOverlap)
	if len(chunks) == 0 {
		m.store.UpdateFileProgress(finfo.ID, 100, "无可切分的文本内容")
		return nil
	}

	slog.Info("kb_file_chunked", "name", finfo.Name, "chunks", len(chunks))
	m.store.UpdateFileProgress(finfo.ID, 5, fmt.Sprintf("已切分为 %d 个文本块，开始向量化", len(chunks)))

	// 初始化向量存储
	vs, err := m.getOrCreateStore(finfo.VdbID)
	if err != nil {
		return fmt.Errorf("获取向量存储失败: %w", err)
	}

	dim, err := m.getEmbClient().Dimension()
	if err != nil {
		slog.Error("kb_process_file_dimension_probe_failed",
			"file", finfo.Name,
			"embedURI", m.cfg.API.EmbeddingAPIURI,
			"embedModel", m.cfg.API.EmbeddingModelName,
			"error", err,
		)
		return fmt.Errorf("探测 embedding 维度失败: %w", err)
	}
	if err := vs.EnsureCollection(dim); err != nil {
		return fmt.Errorf("初始化 collection 失败: %w", err)
	}

	// 批量向量化并插入
	batchSize := 10
	fileName := filepath.Base(finfo.FilePath)
	absPath, _ := filepath.Abs(finfo.FilePath)
	totalChunks := len(chunks)

	bar := progressbar.Default(int64(totalChunks), fileName)

	for i := 0; i < totalChunks; i += batchSize {
		end := i + batchSize
		if end > totalChunks {
			end = totalChunks
		}

		batch := chunks[i:end]
		batchTexts := make([]string, len(batch))
		for j, c := range batch {
			batchTexts[j] = c
		}

		// 批量 embedding
		embeddings, err := m.getEmbClient().Embed(batchTexts)
		if err != nil {
			bar.Finish()
			return fmt.Errorf("embedding 失败 (batch %d-%d): %w", i, end, err)
		}

		// 构建记录
		records := make([]model.VectorRecord, len(batch))
		for j := range batch {
			records[j] = model.VectorRecord{
				ID:      fmt.Sprintf("%s_chunk_%d", fileName, i+j),
				Vector:  embeddings[j],
				Content: batchTexts[j],
				Meta:    map[string]string{"source": absPath},
			}
		}

		// 插入向量存储
		if err := vs.Insert(records); err != nil {
			bar.Finish()
			return fmt.Errorf("插入向量失败: %w", err)
		}

		// 更新进度
		percent := float64(end) / float64(totalChunks) * 100
		if percent > 99 {
			percent = 99
		}
		m.store.UpdateFileProgress(finfo.ID, percent,
			fmt.Sprintf("已处理 %d/%d 个文本块", end, totalChunks))

		bar.Add(min(batchSize, totalChunks-i))
	}

	bar.Finish()

	m.store.UpdateFileProgress(finfo.ID, 100, fmt.Sprintf("处理完成，共 %d 个文本块", totalChunks))
	slog.Info("kb_process_file_done", "name", finfo.Name)
	return nil
}

// ============================================================
// 内部方法
// ============================================================

func (m *Manager) getOrCreateStore(vdbID int64) (vdb.VectorStore, error) {
	m.mu.RLock()
	vs, ok := m.stores[vdbID]
	m.mu.RUnlock()

	if ok {
		return vs, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查
	if vs, ok = m.stores[vdbID]; ok {
		return vs, nil
	}

	// 确保 vdb 目录存在
	os.MkdirAll(VdbDir, 0755)

	vs, err := vdb.New(m.cfg, vdbID)
	if err != nil {
		return nil, err
	}

	m.stores[vdbID] = vs
	return vs, nil
}

func (m *Manager) deleteVectorsBySource(vdbID int64, source string) {
	m.mu.RLock()
	vs, ok := m.stores[vdbID]
	m.mu.RUnlock()

	if ok {
		vs.DeleteBySource(source)
	}
}

// ============================================================
// 文本切分
// ============================================================

// splitText 简单文本切分
func splitText(text string, chunkSize, chunkOverlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 300
	}
	// 防止 chunkOverlap >= chunkSize 导致切分步长 <= 0 死循环
	if chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize / 3
	}
	// 同理防止步长为负
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}

	// 按段落分隔
	paragraphs := strings.Split(text, "\n")
	var chunks []string
	var current string

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		runes := []rune(para)
		if len(runes) <= chunkSize {
			if current == "" {
				current = para
			} else {
				combined := current + "\n" + para
				if len([]rune(combined)) <= chunkSize {
					current = combined
				} else {
					chunks = append(chunks, current)
					current = para
				}
			}
		} else {
			// 长段落需要切分
			if current != "" {
				chunks = append(chunks, current)
				current = ""
			}
			for i := 0; i < len(runes); i += chunkSize - chunkOverlap {
				end := i + chunkSize
				if end > len(runes) {
					end = len(runes)
				}
				chunks = append(chunks, string(runes[i:end]))
				if end == len(runes) {
					break
				}
			}
		}
	}

	if current != "" {
		chunks = append(chunks, current)
	}

	return chunks
}
