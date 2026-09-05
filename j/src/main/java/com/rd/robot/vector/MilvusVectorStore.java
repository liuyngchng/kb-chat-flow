package com.rd.robot.vector;

import com.rd.robot.model.SearchResult;
import com.rd.robot.model.VectorRecord;
import io.milvus.client.MilvusServiceClient;
import io.milvus.param.*;
import io.milvus.param.collection.*;
import io.milvus.param.dml.*;
import io.milvus.param.index.CreateIndexParam;
import io.milvus.grpc.*;
import io.milvus.common.clientenum.ConsistencyLevelEnum;
import io.milvus.response.QueryResultsWrapper;
import io.milvus.response.SearchResultsWrapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;

/**
 * Milvus vector store — remote Milvus implementation.
 */
public class MilvusVectorStore implements VectorStore {

    private static final Logger log = LoggerFactory.getLogger(MilvusVectorStore.class);
    private static final String COLLECTION_NAME = "knowledge_base";

    private final MilvusServiceClient client;

    public MilvusVectorStore(String uri, String token) {
        ConnectParam.Builder builder = ConnectParam.newBuilder()
                .withUri(uri);
        if (token != null && !token.isEmpty()) {
            builder.withToken(token);
        }
        client = new MilvusServiceClient(builder.build());
    }

    @Override
    public void ensureCollection(int dimension) {
        HasCollectionParam hasParam = HasCollectionParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .build();
        R<Boolean> hasResp = client.hasCollection(hasParam);
        if (hasResp.getStatus() == 0 && Boolean.TRUE.equals(hasResp.getData())) {
            log.info("vdb_milvus_collection_exists name={}", COLLECTION_NAME);
            client.loadCollection(LoadCollectionParam.newBuilder()
                    .withCollectionName(COLLECTION_NAME).build());
            return;
        }

        // Create collection schema
        FieldType idField = FieldType.newBuilder()
                .withName("id").withDataType(DataType.VarChar)
                .withMaxLength(512).withPrimaryKey(true).build();
        FieldType vectorField = FieldType.newBuilder()
                .withName("vector").withDataType(DataType.FloatVector)
                .withDimension(dimension).build();
        FieldType contentField = FieldType.newBuilder()
                .withName("content").withDataType(DataType.VarChar)
                .withMaxLength(65535).build();
        FieldType sourceField = FieldType.newBuilder()
                .withName("source").withDataType(DataType.VarChar)
                .withMaxLength(1024).build();

        CreateCollectionParam createParam = CreateCollectionParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withShardsNum(1)
                .addFieldType(idField)
                .addFieldType(vectorField)
                .addFieldType(contentField)
                .addFieldType(sourceField)
                .build();

        R<RpcStatus> createResp = client.createCollection(createParam);
        if (createResp.getStatus() != 0) {
            throw new RuntimeException("创建 collection 失败: " + createResp.getMessage());
        }

        // Create HNSW index
        CreateIndexParam indexParam = CreateIndexParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withFieldName("vector")
                .withIndexType(IndexType.HNSW)
                .withMetricType(MetricType.COSINE)
                .withExtraParam("{\"M\": 16, \"efConstruction\": 200}")
                .build();

        R<RpcStatus> indexResp = client.createIndex(indexParam);
        if (indexResp.getStatus() != 0) {
            throw new RuntimeException("创建索引失败: " + indexResp.getMessage());
        }

        log.info("vdb_milvus_collection_created name={} dim={}", COLLECTION_NAME, dimension);

        client.loadCollection(LoadCollectionParam.newBuilder()
                .withCollectionName(COLLECTION_NAME).build());
    }

    @Override
    public void insert(List<VectorRecord> records) {
        if (records.isEmpty()) return;

        List<String> ids = new ArrayList<>();
        List<List<Float>> vectors = new ArrayList<>();
        List<String> contents = new ArrayList<>();
        List<String> sources = new ArrayList<>();

        for (VectorRecord r : records) {
            ids.add(r.getId());
            List<Float> vec = new ArrayList<>(r.getVector().length);
            for (double v : r.getVector()) vec.add((float) v);
            vectors.add(vec);
            contents.add(r.getContent());
            String source = r.getMeta() != null ? r.getMeta().getOrDefault("source", "") : "";
            sources.add(source);
        }

        List<InsertParam.Field> fields = new ArrayList<>();
        fields.add(new InsertParam.Field("id", ids));
        fields.add(new InsertParam.Field("vector", vectors));
        fields.add(new InsertParam.Field("content", contents));
        fields.add(new InsertParam.Field("source", sources));

        UpsertParam upsertParam = UpsertParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withFields(fields)
                .build();

        R<MutationResult> resp = client.upsert(upsertParam);
        if (resp.getStatus() != 0) {
            throw new RuntimeException("插入向量失败: " + resp.getMessage());
        }
    }

    @Override
    public List<SearchResult> search(double[] queryVector, int topK, double scoreThreshold) {
        client.loadCollection(LoadCollectionParam.newBuilder()
                .withCollectionName(COLLECTION_NAME).build());

        List<Float> vec32 = new ArrayList<>(queryVector.length);
        for (double v : queryVector) vec32.add((float) v);

        List<List<Float>> searchVectors = List.of(vec32);
        List<String> outputFields = List.of("id", "content", "source");

        SearchParam searchParam = SearchParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withMetricType(MetricType.COSINE)
                .withTopK(topK)
                .withVectors(searchVectors)
                .withVectorFieldName("vector")
                .withOutFields(outputFields)
                .withConsistencyLevel(ConsistencyLevelEnum.EVENTUALLY)
                .withParams("{\"ef\": 16}")
                .build();

        R<SearchResults> resp = client.search(searchParam);
        if (resp.getStatus() != 0) {
            throw new RuntimeException("向量检索失败: " + resp.getMessage());
        }

        SearchResultsWrapper wrapper = new SearchResultsWrapper(resp.getData().getResults());
        List<SearchResult> results = new ArrayList<>();

        List<SearchResultsWrapper.IDScore> idScores = wrapper.getIDScore(0);
        List<QueryResultsWrapper.RowRecord> rowRecords = wrapper.getRowRecords(0);

        for (int i = 0; i < idScores.size(); i++) {
            if (idScores.get(i).getScore() < scoreThreshold) continue;

            SearchResult sr = new SearchResult();
            sr.setId(String.valueOf(idScores.get(i).getStrID()));
            sr.setScore((double) idScores.get(i).getScore());

            if (i < rowRecords.size()) {
                QueryResultsWrapper.RowRecord record = rowRecords.get(i);
                sr.setContent(String.valueOf(record.get("content")));
                Map<String, String> meta = new HashMap<>();
                meta.put("source", String.valueOf(record.get("source")));
                sr.setMetadata(meta);
            }
            results.add(sr);
        }

        return results;
    }

    @Override
    public void deleteByIds(List<String> ids) {
        if (ids.isEmpty()) return;

        StringBuilder expr = new StringBuilder("id in [");
        for (int i = 0; i < ids.size(); i++) {
            if (i > 0) expr.append(", ");
            expr.append("\"").append(ids.get(i)).append("\"");
        }
        expr.append("]");

        DeleteParam deleteParam = DeleteParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withExpr(expr.toString())
                .build();

        client.delete(deleteParam);
    }

    @Override
    public void deleteBySource(String source) {
        String expr = "source == \"" + source + "\"";
        DeleteParam deleteParam = DeleteParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .withExpr(expr)
                .build();
        client.delete(deleteParam);
    }

    @Override
    public List<SearchResult> listBySource(String source) {
        return List.of(); // Milvus 暂不支持，待后续实现
    }

    @Override
    public void purge() {
        HasCollectionParam hasParam = HasCollectionParam.newBuilder()
                .withCollectionName(COLLECTION_NAME)
                .build();
        R<Boolean> hasResp = client.hasCollection(hasParam);
        if (hasResp.getStatus() == 0 && Boolean.TRUE.equals(hasResp.getData())) {
            client.dropCollection(DropCollectionParam.newBuilder()
                    .withCollectionName(COLLECTION_NAME).build());
        }
    }

    @Override
    public void close() {
        client.close();
    }
}