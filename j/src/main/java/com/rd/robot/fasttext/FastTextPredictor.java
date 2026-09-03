package com.rd.robot.fasttext;

import com.rd.robot.model.IntentCategory;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.List;
import java.util.concurrent.locks.ReentrantLock;

/**
 * fastText intent classifier.
 * Auto-generates training data from category keywords+descriptions,
 * trains a model, and performs prediction.
 */
public class FastTextPredictor {

    private static final Logger log = LoggerFactory.getLogger(FastTextPredictor.class);

    /** Confidence threshold: below this value, fallthrough to next tier. */
    public static final double CONFIDENCE_THRESHOLD = 0.5;

    private static final String DEFAULT_WORK_DIR = "./dt/ft";

    private final ReentrantLock lock = new ReentrantLock();
    private final String workDir;
    private String modelHash; // hash of currently trained categories, used to detect changes

    /** Prediction result. */
    public record Result(String label, double confidence) {}

    public FastTextPredictor() {
        this.workDir = DEFAULT_WORK_DIR;
        try {
            Files.createDirectories(Paths.get(workDir));
        } catch (IOException ignored) {}
    }

    /**
     * Train the fastText model from intent categories.
     * Skips training if categories haven't changed (hash match).
     */
    public void train(List<IntentCategory> categories, String prompt) throws IOException {
        String hash = hashCategories(categories, prompt);
        if (hash.equals(modelHash)) {
            return; // no changes, skip training
        }

        // Serialize training to avoid concurrent conflicts
        lock.lock();
        try {
            // Double-check
            if (hash.equals(modelHash)) {
                return;
            }

            // Generate training data
            Path trainPath = Paths.get(workDir, "train.txt");
            generateTrainData(trainPath, categories);

            // Train + quantize
            Path modelPath = Paths.get(workDir, "model.ftz");
            trainModel(trainPath, modelPath);

            this.modelHash = hash;
            log.info("fasttext_model_trained categories={} model={}", categories.size(), modelPath);
        } finally {
            lock.unlock();
        }
    }

    /**
     * Predict the intent label for a user query.
     * Returns empty if model is not trained, or result is "none" (unrelated input).
     */
    public Result predict(String query) {
        Path modelPath = Paths.get(workDir, "model.ftz");
        if (!Files.exists(modelPath)) {
            return null;
        }

        lock.lock();
        try {
            String tokens = tokenize(query);

            // Call fasttext predict-prob
            ProcessBuilder pb = new ProcessBuilder(
                    "fasttext", "predict-prob", modelPath.toString(), "-", "1");
            pb.directory(new File(workDir));
            pb.redirectErrorStream(false);
            Process proc = pb.start();

            // Write query to stdin
            try (OutputStream os = proc.getOutputStream()) {
                os.write((tokens + "\n").getBytes(StandardCharsets.UTF_8));
                os.flush();
            }

            // Read output
            String output;
            try (BufferedReader reader = new BufferedReader(
                    new InputStreamReader(proc.getInputStream(), StandardCharsets.UTF_8))) {
                output = reader.readLine();
            }

            proc.waitFor();

            Result result = parsePredictOutput(output);
            if (result == null || result.label.isEmpty() || "none".equals(result.label)) {
                if (result != null && "none".equals(result.label)) {
                    log.info("fasttext_classified_as_none confidence={} query={}",
                            result.confidence, truncate(query, 50));
                }
                return null;
            }

            return result;
        } catch (Exception e) {
            log.warn("fasttext_predict_failed query={} error={}", truncate(query, 50), e.getMessage());
            return null;
        } finally {
            lock.unlock();
        }
    }

    /**
     * Check if the model has been trained.
     */
    public boolean isTrained() {
        return Files.exists(Paths.get(workDir, "model.ftz"));
    }

    // ============================================================
    // Internal methods
    // ============================================================

    /** Tokenize Chinese text character by character (space-separated). */
    static String tokenize(String s) {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < s.length(); i++) {
            if (i > 0) sb.append(' ');
            sb.append(s.charAt(i));
        }
        return sb.toString();
    }

    /** Samples of unrelated input to teach the model to reject out-of-scope queries. */
    private static final String[] NONE_SAMPLES = {
            "今天天气真好", "明天会下雨吗", "附近有什么好吃的",
            "帮我写首诗", "讲个笑话", "几点了",
            "你是谁", "你会做什么", "你好啊",
            "播放音乐", "设置闹钟", "帮我查快递",
            "翻译一下", "什么是人工智能", "怎么做红烧肉",
            "股票涨了", "最近有什么电影",
    };

    /** Generate training data from category definitions. */
    private static void generateTrainData(Path path, List<IntentCategory> categories) throws IOException {
        try (PrintWriter w = new PrintWriter(Files.newBufferedWriter(path, StandardCharsets.UTF_8))) {
            for (IntentCategory cat : categories) {
                String label = cat.getName();
                // Keywords as training samples
                if (cat.getKeywords() != null) {
                    for (String kw : cat.getKeywords()) {
                        w.printf("__label__%s %s%n", label, tokenize(kw));
                    }
                }
                // Description as training sample
                String desc = cat.getDescription();
                if (desc != null && !desc.trim().isEmpty()) {
                    w.printf("__label__%s %s%n", label, tokenize(desc.trim()));
                }
            }

            // "none" category: teach model to reject unrelated input
            for (String s : NONE_SAMPLES) {
                w.printf("__label__none %s%n", tokenize(s));
            }
        }
    }

    /** Train and quantize the fastText model. */
    private static void trainModel(Path trainPath, Path modelPath) throws IOException {
        String modelPathStr = modelPath.toString();
        String outputPrefix = modelPathStr.endsWith(".ftz")
                ? modelPathStr.substring(0, modelPathStr.length() - 4)
                : modelPathStr;

        // Step 1: Supervised training
        ProcessBuilder trainPb = new ProcessBuilder(
                "fasttext", "supervised",
                "-input", trainPath.toString(),
                "-output", outputPrefix,
                "-epoch", "200",
                "-lr", "0.8",
                "-wordNgrams", "3",
                "-dim", "50",
                "-minCount", "1"
        );
        trainPb.inheritIO();
        try {
            Process proc = trainPb.start();
            int exitCode = proc.waitFor();
            if (exitCode != 0) {
                throw new IOException("fasttext supervised failed with exit code " + exitCode);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IOException("fasttext supervised interrupted", e);
        }

        // Step 2: Quantize (drastically reduces model size)
        ProcessBuilder quantPb = new ProcessBuilder(
                "fasttext", "quantize",
                "-input", trainPath.toString(),
                "-output", outputPrefix,
                "-qnorm",
                "-retrain",
                "-epoch", "25",
                "-cutoff", "50000"
        );
        quantPb.inheritIO();
        try {
            Process proc = quantPb.start();
            int exitCode = proc.waitFor();
            if (exitCode != 0) {
                throw new IOException("fasttext quantize failed with exit code " + exitCode);
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IOException("fasttext quantize interrupted", e);
        }

        // Verify quantized model exists
        if (!Files.exists(modelPath)) {
            throw new IOException("model file not created: " + modelPath);
        }

        // Clean up un-quantized .bin and .vec files
        try {
            Files.deleteIfExists(Paths.get(outputPrefix + ".bin"));
            Files.deleteIfExists(Paths.get(outputPrefix + ".vec"));
        } catch (IOException ignored) {}
    }

    /** Parse fasttext predict-prob output: "__label__xxx 0.999876" */
    static Result parsePredictOutput(String output) {
        if (output == null || output.trim().isEmpty()) {
            return null;
        }

        String[] parts = output.trim().split("\\s+");
        if (parts.length < 2) {
            return null;
        }

        // Strip __label__ prefix
        String label = parts[0];
        if (!label.startsWith("__label__")) {
            return null; // invalid output
        }
        label = label.substring(9); // remove "__label__"

        double confidence;
        try {
            confidence = Double.parseDouble(parts[1]);
        } catch (NumberFormatException e) {
            return null;
        }

        return new Result(label, confidence);
    }

    /** Generate a hash from category configurations for change detection. */
    private static String hashCategories(List<IntentCategory> categories, String prompt) {
        StringBuilder sb = new StringBuilder();
        sb.append(prompt).append("|");
        for (IntentCategory cat : categories) {
            sb.append(cat.getName()).append(":");
            sb.append(cat.getDescription()).append(":");
            if (cat.getKeywords() != null) {
                sb.append(String.join(",", cat.getKeywords()));
            }
            sb.append(";");
        }
        return sb.toString();
    }

    private static String truncate(String s, int maxLen) {
        if (s == null) return "";
        return s.length() <= maxLen ? s : s.substring(0, maxLen);
    }
}
