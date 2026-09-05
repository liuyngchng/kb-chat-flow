package com.rd.robot.vector;

import com.rd.robot.model.Config;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * Factory for creating VectorStore instances based on configuration.
 */
public class VectorStoreFactory {

    private static final Logger log = LoggerFactory.getLogger(VectorStoreFactory.class);

    private VectorStoreFactory() {}

    /**
     * Create a VectorStore according to the configured backend.
     *
     * @param cfg   application config
     * @param vdbId knowledge base ID
     * @return VectorStore instance
     */
    public static VectorStore create(Config cfg, long vdbId) {
        String backend = cfg.getVector() != null ? cfg.getVector().getBackend() : "local";

        switch (backend) {
            case "milvus":
                if (cfg.getMilvus() == null || cfg.getMilvus().getUri() == null || cfg.getMilvus().getUri().isEmpty()) {
                    throw new RuntimeException("Milvus URI 未配置");
                }
                log.info("vdb_milvus_remote_init uri={}", cfg.getMilvus().getUri());
                try {
                    return new MilvusVectorStore(cfg.getMilvus().getUri(), cfg.getMilvus().getToken());
                } catch (Exception e) {
                    throw new RuntimeException("创建 Milvus 向量存储失败", e);
                }

            case "qdrant":
                if (cfg.getQdrant() == null || cfg.getQdrant().getHost().isEmpty()) {
                    throw new RuntimeException("Qdrant Host 未配置");
                }
                int port = cfg.getQdrant().getPort();
                if (port == 0) port = 6334;
                log.info("vdb_qdrant_init host={} port={}", cfg.getQdrant().getHost(), port);
                return new QdrantVectorStore(
                        cfg.getQdrant().getHost(),
                        port,
                        cfg.getQdrant().getApiKey(),
                        cfg.getQdrant().isUseTls(),
                        vdbId
                );

            default:
                // "local" 或空
                log.info("vdb_local_init vdb_id={}", vdbId);
                try {
                    return new LocalVectorStore("./vdb/vectors.db", vdbId);
                } catch (Exception e) {
                    throw new RuntimeException("创建本地向量存储失败", e);
                }
        }
    }
}