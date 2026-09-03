package com.rd.robot.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.EmbeddingRequest;
import com.rd.robot.model.EmbeddingResponse;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

/**
 * Embedding client (OpenAI compatible API).
 */
public class EmbeddingClient {

    private static final Logger log = LoggerFactory.getLogger(EmbeddingClient.class);
    private static final int MAX_BATCH_SIZE = 32;
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final String baseUrl;
    private final String apiKey;
    private final String modelName;
    private final HttpClient httpClient;

    public EmbeddingClient(String baseUrl, String apiKey, String modelName) {
        this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
        this.apiKey = apiKey;
        this.modelName = modelName;
        this.httpClient = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(10))
                .build();
    }

    /**
     * Batch embedding for multiple texts.
     */
    public List<double[]> embed(List<String> texts) {
        if (texts == null || texts.isEmpty()) return List.of();

        List<double[]> allEmbeddings = new ArrayList<>(texts.size());

        for (int i = 0; i < texts.size(); i += MAX_BATCH_SIZE) {
            int end = Math.min(i + MAX_BATCH_SIZE, texts.size());
            List<String> batch = texts.subList(i, end);
            try {
                List<double[]> embeddings = embedBatch(batch);
                allEmbeddings.addAll(embeddings);
            } catch (Exception e) {
                throw new RuntimeException("batch " + i + "-" + end + " embedding 失败", e);
            }
        }

        return allEmbeddings;
    }

    /**
     * Single text embedding.
     */
    public double[] embedSingle(String text) {
        List<double[]> results = embed(List.of(text));
        if (results.isEmpty()) {
            throw new RuntimeException("embedding 返回空结果");
        }
        return results.get(0);
    }

    /**
     * Probe embedding dimension.
     */
    public int dimension() {
        double[] vec = embedSingle("dimension probe");
        int dim = vec.length;
        log.info("embedding_dimension_probe_success model={} dim={}", modelName, dim);
        return dim;
    }

    private List<double[]> embedBatch(List<String> texts) {
        try {
            EmbeddingRequest reqBody = new EmbeddingRequest(modelName, texts);
            String json = MAPPER.writeValueAsString(reqBody);

            HttpRequest request = HttpRequest.newBuilder()
                    .uri(URI.create(baseUrl + "/embeddings"))
                    .timeout(Duration.ofSeconds(60))
                    .header("Content-Type", "application/json")
                    .header("Authorization", "Bearer " + apiKey)
                    .POST(HttpRequest.BodyPublishers.ofString(json))
                    .build();

            HttpResponse<String> response = httpClient.send(request,
                    HttpResponse.BodyHandlers.ofString());

            if (response.statusCode() != 200) {
                throw new RuntimeException("embedding API 返回错误 " + response.statusCode() + ": " + response.body());
            }

            EmbeddingResponse result = MAPPER.readValue(response.body(), EmbeddingResponse.class);

            List<double[]> embeddings = new ArrayList<>();
            if (result.getData() != null) {
                for (var item : result.getData()) {
                    if (item.getEmbedding() != null) {
                        double[] vec = item.getEmbedding().stream()
                                .mapToDouble(Double::doubleValue)
                                .toArray();
                        embeddings.add(vec);
                    }
                }
            }

            return embeddings;
        } catch (Exception e) {
            throw new RuntimeException("请求 embedding API 失败", e);
        }
    }
}