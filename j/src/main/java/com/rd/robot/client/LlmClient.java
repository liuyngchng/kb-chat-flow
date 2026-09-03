package com.rd.robot.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.rd.robot.model.ChatCompletionChunk;
import com.rd.robot.model.ChatCompletionMsg;
import com.rd.robot.model.ChatCompletionRequest;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;
import java.util.function.Consumer;

/**
 * LLM client (OpenAI compatible API).
 * Supports streaming and non-streaming chat with configurable parameters.
 */
public class LlmClient {

    private static final Logger log = LoggerFactory.getLogger(LlmClient.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final String baseUrl;
    private final String apiKey;
    private final String modelName;
    private final HttpClient httpClient;

    private Double temperature;
    private Double topP;
    private Integer maxTokens;

    public LlmClient(String baseUrl, String apiKey, String modelName) {
        this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0, baseUrl.length() - 1) : baseUrl;
        this.apiKey = apiKey;
        this.modelName = modelName;
        this.httpClient = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(10))
                .build();
    }

    /**
     * Set LLM model parameters.
     */
    public void setParams(double temperature, double topP, int maxTokens) {
        if (temperature > 0) this.temperature = temperature;
        if (topP > 0) this.topP = topP;
        if (maxTokens > 0) this.maxTokens = maxTokens;
    }

    public String getModelName() { return modelName; }
    public Double getTemperature() { return temperature; }
    public Double getTopP() { return topP; }
    public Integer getMaxTokens() { return maxTokens; }

    // ============================================================
    // Streaming chat (SSE)
    // ============================================================

    /**
     * Stream chat response via SSE callbacks.
     * Runs in a background thread.
     */
    public void chatStream(String systemPrompt, String userMessage,
                           Consumer<String> onChunk,
                           Consumer<String> onError,
                           Runnable onDone) {
        new Thread(() -> {
            try {
                List<ChatCompletionMsg> messages = List.of(
                        new ChatCompletionMsg("system", systemPrompt),
                        new ChatCompletionMsg("user", userMessage)
                );

                ChatCompletionRequest reqBody = new ChatCompletionRequest();
                reqBody.setModel(modelName);
                reqBody.setMessages(messages);
                reqBody.setStream(true);
                reqBody.setTemperature(temperature);
                reqBody.setTopP(topP);
                reqBody.setMaxTokens(maxTokens);

                String json = MAPPER.writeValueAsString(reqBody);

                HttpRequest request = HttpRequest.newBuilder()
                        .uri(URI.create(baseUrl + "/chat/completions"))
                        .timeout(Duration.ofSeconds(120))
                        .header("Content-Type", "application/json")
                        .header("Authorization", "Bearer " + apiKey)
                        .POST(HttpRequest.BodyPublishers.ofString(json))
                        .build();

                HttpResponse<java.io.InputStream> response = httpClient.send(request,
                        HttpResponse.BodyHandlers.ofInputStream());

                if (response.statusCode() != 200) {
                    String body = new String(response.body().readAllBytes());
                    onError.accept("LLM API 返回错误 " + response.statusCode() + ": " + body);
                    onDone.run();
                    return;
                }

                BufferedReader reader = new BufferedReader(
                        new InputStreamReader(response.body()));

                String line;
                while ((line = reader.readLine()) != null) {
                    line = line.trim();
                    if (line.isEmpty() || !line.startsWith("data: ")) {
                        continue;
                    }

                    String data = line.substring(6);
                    if ("[DONE]".equals(data)) {
                        break;
                    }

                    try {
                        ChatCompletionChunk chunk = MAPPER.readValue(data, ChatCompletionChunk.class);
                        if (chunk.getChoices() != null && !chunk.getChoices().isEmpty()) {
                            ChatCompletionChunk.Delta delta = chunk.getChoices().get(0).getDelta();
                            if (delta != null && delta.getContent() != null) {
                                onChunk.accept(delta.getContent());
                            }
                        }
                    } catch (Exception ignored) {
                        // skip malformed chunks
                    }
                }
            } catch (Exception e) {
                log.error("llm_request_failed", e);
                onError.accept("请求 LLM 失败: " + e.getMessage());
            }
            onDone.run();
        }).start();
    }

    // ============================================================
    // Non-streaming chat
    // ============================================================

    /**
     * Synchronous chat, returns the full response.
     */
    public String chat(String systemPrompt, String userMessage) {
        try {
            List<ChatCompletionMsg> messages = List.of(
                    new ChatCompletionMsg("system", systemPrompt),
                    new ChatCompletionMsg("user", userMessage)
            );

            ChatCompletionRequest reqBody = new ChatCompletionRequest();
            reqBody.setModel(modelName);
            reqBody.setMessages(messages);
            reqBody.setStream(false);
            reqBody.setTemperature(temperature);
            reqBody.setTopP(topP);
            reqBody.setMaxTokens(maxTokens);

            String json = MAPPER.writeValueAsString(reqBody);

            HttpRequest request = HttpRequest.newBuilder()
                    .uri(URI.create(baseUrl + "/chat/completions"))
                    .timeout(Duration.ofSeconds(120))
                    .header("Content-Type", "application/json")
                    .header("Authorization", "Bearer " + apiKey)
                    .POST(HttpRequest.BodyPublishers.ofString(json))
                    .build();

            HttpResponse<String> response = httpClient.send(request,
                    HttpResponse.BodyHandlers.ofString());

            if (response.statusCode() != 200) {
                throw new RuntimeException("LLM API 返回错误 " + response.statusCode() + ": " + response.body());
            }

            var result = MAPPER.readTree(response.body());
            var choices = result.get("choices");
            if (choices != null && choices.size() > 0) {
                var message = choices.get(0).get("message");
                if (message != null) {
                    var content = message.get("content");
                    if (content != null) {
                        return content.asText();
                    }
                }
            }

            throw new RuntimeException("LLM 返回空响应");
        } catch (RuntimeException e) {
            throw e;
        } catch (Exception e) {
            throw new RuntimeException("LLM 请求失败", e);
        }
    }
}