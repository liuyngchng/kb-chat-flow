package vdb

import (
	"context"
	"fmt"
	"log/slog"

	"kb-chat-flow/internal/model"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	qdrant "github.com/qdrant/go-client/qdrant"
)

const qdrantCollectionPrefix = "kb_"

// QdrantStore Qdrant 向量存储
type QdrantStore struct {
	ctx               context.Context
	conn              *grpc.ClientConn
	pointsClient      qdrant.PointsClient
	collectionsClient qdrant.CollectionsClient
	collectionName    string
	vdbID             int64
}

// NewQdrantStore 创建 Qdrant 向量存储连接
func NewQdrantStore(host string, port int, apiKey string, useTLS bool, vdbID int64) (*QdrantStore, error) {
	ctx := context.Background()
	addr := fmt.Sprintf("%s:%d", host, port)

	var opts []grpc.DialOption
	if useTLS {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// 如果有 API Key，添加拦截器
	if apiKey != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			md := metadata.Pairs("api-key", apiKey)
			ctx = metadata.NewOutgoingContext(ctx, md)
			return invoker(ctx, method, req, reply, cc, opts...)
		}))
	}

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("连接 Qdrant 失败: %w", err)
	}

	collectionName := fmt.Sprintf("%s%d", qdrantCollectionPrefix, vdbID)

	return &QdrantStore{
		ctx:               ctx,
		conn:              conn,
		pointsClient:      qdrant.NewPointsClient(conn),
		collectionsClient: qdrant.NewCollectionsClient(conn),
		collectionName:    collectionName,
		vdbID:             vdbID,
	}, nil
}

// EnsureCollection 确保 collection 存在，不存在则创建
func (s *QdrantStore) EnsureCollection(dimension int) error {
	// 检查 collection 是否存在
	_, err := s.collectionsClient.Get(s.ctx, &qdrant.GetCollectionInfoRequest{
		CollectionName: s.collectionName,
	})
	if err == nil {
		slog.Info("vdb_qdrant_collection_exists", "name", s.collectionName)
		return nil
	}

	// 创建 collection
	_, err = s.collectionsClient.Create(s.ctx, &qdrant.CreateCollection{
		CollectionName: s.collectionName,
		VectorsConfig: &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_Params{
				Params: &qdrant.VectorParams{
					Size:     uint64(dimension),
					Distance: qdrant.Distance_Cosine,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("创建 Qdrant collection 失败: %w", err)
	}

	slog.Info("vdb_qdrant_collection_created", "name", s.collectionName, "dim", dimension)
	return nil
}

// Insert 批量插入向量
func (s *QdrantStore) Insert(records []model.VectorRecord) error {
	if len(records) == 0 {
		return nil
	}

	points := make([]*qdrant.PointStruct, len(records))
	for i, r := range records {
		pointID := qdrant.NewIDNum(uint64(i + 1))

		source := ""
		if r.Meta != nil {
			source = r.Meta["source"]
		}

		points[i] = &qdrant.PointStruct{
			Id: pointID,
			Vectors: &qdrant.Vectors{
				VectorsOptions: &qdrant.Vectors_Vector{
					Vector: &qdrant.Vector{
						Data: float64ToFloat32(r.Vector),
					},
				},
			},
			Payload: map[string]*qdrant.Value{
				"doc_id":  qdrant.NewValueString(r.ID),
				"content": qdrant.NewValueString(r.Content),
				"source":  qdrant.NewValueString(source),
			},
		}
	}

	_, err := s.pointsClient.Upsert(s.ctx, &qdrant.UpsertPoints{
		CollectionName: s.collectionName,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("Qdrant 插入向量失败: %w", err)
	}

	return nil
}

// Search 向量检索
func (s *QdrantStore) Search(queryVector []float64, topK int, scoreThreshold float64) ([]model.SearchResult, error) {
	if topK <= 0 {
		topK = 3
	}

	limit := uint64(topK)
	threshold := float32(scoreThreshold)

	resp, err := s.pointsClient.Search(s.ctx, &qdrant.SearchPoints{
		CollectionName: s.collectionName,
		Vector:         float64ToFloat32(queryVector),
		Limit:          limit,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
		ScoreThreshold: &threshold,
	})
	if err != nil {
		return nil, fmt.Errorf("Qdrant 检索失败: %w", err)
	}

	var results []model.SearchResult
	for _, scoredPoint := range resp.Result {
		payload := scoredPoint.Payload

		docID := getPayloadString(payload, "doc_id")
		content := getPayloadString(payload, "content")
		source := getPayloadString(payload, "source")

		results = append(results, model.SearchResult{
			ID:      docID,
			Content: content,
			Meta:    map[string]string{"source": source},
			Score:   float64(scoredPoint.Score),
		})
	}

	return results, nil
}

// DeleteByIDs 根据 ID 列表删除（通过 payload doc_id 匹配）
func (s *QdrantStore) DeleteByIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	matchConditions := make([]*qdrant.Condition, len(ids))
	for i, id := range ids {
		matchConditions[i] = newMatchCondition("doc_id", id)
	}

	_, err := s.pointsClient.Delete(s.ctx, &qdrant.DeletePoints{
		CollectionName: s.collectionName,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Should: matchConditions,
		}),
	})
	if err != nil {
		return fmt.Errorf("Qdrant 删除向量失败: %w", err)
	}

	return nil
}

// DeleteBySource 根据 source 字段删除
func (s *QdrantStore) DeleteBySource(source string) error {
	_, err := s.pointsClient.Delete(s.ctx, &qdrant.DeletePoints{
		CollectionName: s.collectionName,
		Points: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
			Must: []*qdrant.Condition{
				newMatchCondition("source", source),
			},
		}),
	})
	if err != nil {
		return fmt.Errorf("Qdrant 按 source 删除失败: %w", err)
	}

	return nil
}

// ListBySource 根据 source 列出所有 chunks（Qdrant 暂不支持，待后续实现）
func (s *QdrantStore) ListBySource(source string) ([]model.SearchResult, error) {
	return nil, nil
}

// Purge 清空当前 store 的所有数据
func (s *QdrantStore) Purge() error {
	_, err := s.collectionsClient.Delete(s.ctx, &qdrant.DeleteCollection{
		CollectionName: s.collectionName,
	})
	if err != nil {
		slog.Warn("vdb_qdrant_delete_collection_failed", "name", s.collectionName, "error", err)
	}
	return nil
}

// Close 关闭连接
func (s *QdrantStore) Close() error {
	return s.conn.Close()
}

// ============================================================
// 辅助函数
// ============================================================

// newMatchCondition 构建一个 Field 匹配条件（keyword match on payload）
func newMatchCondition(field, value string) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: field,
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Keyword{Keyword: value},
				},
			},
		},
	}
}

func float64ToFloat32(f []float64) []float32 {
	v := make([]float32, len(f))
	for i, val := range f {
		v[i] = float32(val)
	}
	return v
}

func getPayloadString(payload map[string]*qdrant.Value, key string) string {
	if v, ok := payload[key]; ok {
		if sv := v.GetStringValue(); sv != "" {
			return sv
		}
	}
	return ""
}
