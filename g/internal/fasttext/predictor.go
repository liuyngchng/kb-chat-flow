package fasttext

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"kb-chat-flow/internal/model"
)

// 置信度阈值：低于此值视为不匹配，fallthrough 到下一层
const ConfidenceThreshold = 0.5

// PredictTopK fastText predict-prob 返回的候选数。
// 多意图识别场景下取 5 个候选，由上层按优先级路由。
const PredictTopK = 5

// installHint 系统未安装 fasttext 二进制时的安装提示。
// 训练（supervised / quantize）和预测（predict-prob）都依赖它。
const installHint = `系统未安装 fasttext 命令行工具，意图分类的 fastText 层将被跳过。
请安装该工具后重启服务：
  sudo apt-get install -y fasttext`

// binaryChecked 缓存二进制探测结果（避免每次调用都 exec.LookPath）。
var binaryChecked bool
var binaryAvailCache bool

// binaryAvailable 探测 fasttext 可执行文件是否可用，结果缓存。
func binaryAvailable() bool {
	if !binaryChecked {
		_, err := exec.LookPath("fasttext")
		binaryAvailCache = err == nil
		binaryChecked = true
	}
	return binaryAvailCache
}

// 预测结果
type Result struct {
	Label      model.IntentType
	Confidence float64
}

// Predictor fastText 意图分类器。
// 从类别关键词+描述自动生成训练数据，训练模型，执行预测。
type Predictor struct {
	mu        sync.Mutex
	workDir   string
	modelHash string // 当前已训练模型的类别 hash，用于检测变更
}

const defaultWorkDir = "./dt/ft"

// New 创建 fastText 预测器
func New() *Predictor {
	os.MkdirAll(defaultWorkDir, 0755)
	return &Predictor{workDir: defaultWorkDir}
}

// Train 根据类别定义训练 fastText 模型。
// 如果类别未变更（与上次训练的 hash 相同）则跳过训练。
// 系统未安装 fasttext 二进制时会返回带安装提示的错误。
func (p *Predictor) Train(categories []model.IntentCategory, prompt string) error {
	// 提前探测二进制，未安装时直接给出安装提示，避免走到 exec 才报错
	if !binaryAvailable() {
		return fmt.Errorf("fastText 训练模型失败: %s", installHint)
	}

	// 模型已存在：直接信任现有模型，跳过训练。
	// 注意：modelHash 是进程内存变量，每次启动都为空，若不判断模型文件
	// 存在与否，会导致每次启动都重新训练（耗时几十秒、覆盖已有模型）。
	modelPath := filepath.Join(p.workDir, "model.ftz")
	if _, err := os.Stat(modelPath); err == nil {
		p.modelHash = hashCategories(categories, prompt)
		slog.Info("fasttext_model_exists_skip_training", "model", modelPath, "categories", len(categories))
		return nil
	}

	hash := hashCategories(categories, prompt)
	if hash == p.modelHash {
		return nil // 类别未变，无需重新训练
	}

	// 序列化训练，避免并发训练冲突
	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	if hash == p.modelHash {
		return nil
	}

	// 生成训练数据
	trainPath := filepath.Join(p.workDir, "train.txt")
	if err := generateTrainData(trainPath, categories); err != nil {
		return fmt.Errorf("fastText: 生成训练数据失败: %w", err)
	}

	// 训练 + 量化
	if err := trainModel(trainPath, modelPath); err != nil {
		return fmt.Errorf("fastText: 训练模型失败: %w", err)
	}

	p.modelHash = hash
	slog.Info("fasttext_model_trained", "categories", len(categories), "model", modelPath)
	return nil
}

// Predict 预测用户 query 的意图类别，返回多个候选（按置信度降序）。
// 模型未训练时返回空切片。已过滤 none 标签和低于阈值的候选。
func (p *Predictor) Predict(query string) []Result {
	modelPath := filepath.Join(p.workDir, "model.ftz")
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil
	}

	// 二进制不可用：记录提示并跳过，避免每次预测都报 exec not found
	if !binaryAvailable() {
		slog.Warn("fasttext_predict_skipped_not_installed", "hint", installHint)
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	tokens := tokenize(query)

	// 调用 fasttext predict-prob，拿 TopK 个候选
	cmd := exec.Command("fasttext", "predict-prob", modelPath, "-", strconv.Itoa(PredictTopK))
	cmd.Stdin = strings.NewReader(tokens + "\n")
	output, err := cmd.Output()
	if err != nil {
		slog.Warn("fasttext_predict_failed", "error", err, "hint", installHint, "query", query[:min(50, len(query))])
		return nil
	}

	// 解析输出: "__label__xxx 0.999 __label__yyy 0.888"
	results := parsePredictOutputs(string(output))
	if len(results) == 0 {
		slog.Info("fasttext_predict_below_threshold", "query", query[:min(50, len(query))])
	}

	return results
}

// IsTrained 判断模型是否已训练
func (p *Predictor) IsTrained() bool {
	modelPath := filepath.Join(p.workDir, "model.ftz")
	_, err := os.Stat(modelPath)
	return err == nil
}

// ============================================================
// 内部函数
// ============================================================

// tokenize 按字切分中文文本（空格分隔每个字符）
func tokenize(s string) string {
	runes := []rune(s)
	parts := make([]string, len(runes))
	for i, r := range runes {
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}

// noneSamples 与燃气业务无关的输入样本，训练模型拒绝无关请求
var noneSamples = []string{
	"今天天气真好", "明天会下雨吗", "附近有什么好吃的",
	"帮我写首诗", "讲个笑话", "几点了",
	"你是谁", "你会做什么", "你好啊",
	"播放音乐", "设置闹钟", "帮我查快递",
	"翻译一下", "什么是人工智能", "怎么做红烧肉",
	"股票涨了", "最近有什么电影",
}

// generateTrainData 从类别定义生成训练数据
func generateTrainData(path string, categories []model.IntentCategory) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, cat := range categories {
		label := string(cat.Name)
		// 关键词作为训练样本
		for _, kw := range cat.Keywords {
			fmt.Fprintf(f, "__label__%s %s\n", label, tokenize(kw))
		}
		// 描述作为训练样本
		if desc := strings.TrimSpace(cat.Description); desc != "" {
			fmt.Fprintf(f, "__label__%s %s\n", label, tokenize(desc))
		}
	}

	// none 类别：教模型拒绝不相关的输入
	for _, s := range noneSamples {
		fmt.Fprintf(f, "__label__none %s\n", tokenize(s))
	}

	return nil
}

// trainModel 调用 fastText CLI 训练并量化模型
func trainModel(trainPath, modelPath string) error {
	outputPrefix := strings.TrimSuffix(modelPath, ".ftz")

	// 第一步：训练
	cmd := exec.Command("fasttext", "supervised",
		"-input", trainPath,
		"-output", outputPrefix,
		"-epoch", "200",
		"-lr", "0.8",
		"-wordNgrams", "3",
		"-dim", "50",
		"-minCount", "1",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fasttext supervised failed: %w", err)
	}

	// 第二步：量化压缩（大幅减小模型体积）
	quantCmd := exec.Command("fasttext", "quantize",
		"-input", trainPath,
		"-output", outputPrefix,
		"-qnorm",
		"-retrain",
		"-epoch", "25",
		"-cutoff", "50000",
	)
	if err := quantCmd.Run(); err != nil {
		return fmt.Errorf("fasttext quantize failed: %w", err)
	}

	// 确认量化模型存在
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model file not created: %s", modelPath)
	}

	// 清理未量化的 .bin 和 .vec 文件
	os.Remove(outputPrefix + ".bin")
	os.Remove(outputPrefix + ".vec")

	return nil
}

// parsePredictOutputs 解析 fasttext predict-prob (k>1) 输出。
// 格式: "__label__xxx 0.999 __label__yyy 0.888"
// 过滤 none 标签和低于阈值的候�选，按置信度降序返回。
func parsePredictOutputs(output string) []Result {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	parts := strings.Fields(output)
	// 需要偶数个：label confidence label confidence ...
	if len(parts) < 2 || len(parts)%2 != 0 {
		return nil
	}

	var results []Result
	for i := 0; i < len(parts); i += 2 {
		label := strings.TrimPrefix(parts[i], "__label__")
		if label == parts[i] || label == "none" {
			continue // 去掉无前缀的和 none 标签
		}

		var confidence float64
		fmt.Sscanf(parts[i+1], "%f", &confidence)

		if confidence >= ConfidenceThreshold {
			results = append(results, Result{
				Label:      model.IntentType(label),
				Confidence: confidence,
			})
		}
	}

	return results
}

// hashCategories 基于类别配置生成 hash，用于检测变更
func hashCategories(categories []model.IntentCategory, prompt string) string {
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("|")
	for _, cat := range categories {
		b.WriteString(string(cat.Name))
		b.WriteString(":")
		b.WriteString(cat.Description)
		b.WriteString(":")
		b.WriteString(strings.Join(cat.Keywords, ","))
		b.WriteString(";")
	}
	return b.String()
}
