package rerank

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"kb-chat-flow/internal/model"
)

// 高并发 HTTP 连接池
var rerankHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	},
	Timeout: 60 * time.Second,
}

// Client Rerank 客户端（OpenAI/Cohere 兼容接口）
type Client struct {
	BaseURL   string
	APIKey    string
	ModelName string
	httpCli   *http.Client
}

// New 创建 Rerank 客户端
func New(baseURL, apiKey, modelName string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		ModelName: modelName,
		httpCli:   rerankHTTPClient,
	}
}

// Rerank 对候选文档进行重排序，返回排序后的索引及分数
func (c *Client) Rerank(query string, documents []string, topN int) ([]model.RerankResult, error) {
	if len(documents) == 0 {
		return nil, nil
	}

	reqBody := model.RerankRequest{
		Model:     c.ModelName,
		Query:     query,
		Documents: documents,
		TopN:      topN,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化 rerank 请求失败: %w", err)
	}

	url := c.BaseURL + "/rerank"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建 rerank 请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 rerank API 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 rerank 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 300 {
			bodyStr = bodyStr[:300] + "..."
		}
		slog.Error("rerank_api_non_200_status", "status", resp.StatusCode, "body", bodyStr)
		return nil, fmt.Errorf("rerank API 返回 %d", resp.StatusCode)
	}

	var result model.RerankResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析 rerank 响应失败: %w", err)
	}

	return result.Results, nil
}
