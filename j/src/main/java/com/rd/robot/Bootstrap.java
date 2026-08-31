package com.rd.robot;

import com.rd.robot.client.ClientFactory;
import com.rd.robot.config.AppConfig;
import com.rd.robot.config.RuntimeConfig;
import com.rd.robot.knowledge.FileStore;
import com.rd.robot.knowledge.KnowledgeBaseManager;
import com.rd.robot.knowledge.LocalFileStore;
import com.rd.robot.knowledge.S3FileStore;
import com.rd.robot.model.Config;
import com.rd.robot.redis.RedisClient;
import com.rd.robot.repository.MetaStore;
import com.rd.robot.repository.MysqlMetaStore;
import com.rd.robot.repository.SqliteMetaStore;
import com.rd.robot.security.TokenProvider;
import com.rd.robot.session.RedisSessionStore;
import com.rd.robot.session.SessionManager;
import com.rd.robot.web.controller.*;
import com.rd.robot.web.router.Router;
import com.rd.robot.web.server.HttpServer;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.File;

/**
 * Application entry point.
 */
public class Bootstrap {

    private static final Logger log = LoggerFactory.getLogger(Bootstrap.class);

    public static void main(String[] args) {
        // 1. Load config
        Config cfg = AppConfig.load("cfg.yml");

        // 角色便捷判断
        boolean isAdmin = cfg.getServer().isAdminRole();
        boolean isChat  = cfg.getServer().isChatRole();

        log.info("启动对话机器人... mode={} role={}", cfg.getServer().getMode(), cfg.getServer().getRole());

        // 2. Token 签名密钥校验
        if (cfg.getServer().isClusterMode() && (cfg.getServer().getTokenSecret() == null || cfg.getServer().getTokenSecret().isEmpty())) {
            System.err.println("错误: cluster 模式下必须配置 server.token_secret（多节点共享 HMAC 签名密钥）");
            System.err.println("请在 cfg.yml 的 server 段中添加 token_secret 字段，例如:");
            System.err.println("  server:");
            System.err.println("    token_secret: \"请使用 openssl rand -hex 32 生成随机密钥\"");
            System.exit(1);
        }
        if (cfg.getServer().getTokenSecret() == null || cfg.getServer().getTokenSecret().isEmpty()) {
            log.warn("未配置 server.token_secret，使用默认密钥。生产环境请务必设置自定义密钥！");
        }

        // 3. Init token secret (cluster mode needs consistent secret across nodes)
        if (cfg.getServer().getTokenSecret() != null && !cfg.getServer().getTokenSecret().isEmpty()) {
            TokenProvider.initSecret(cfg.getServer().getTokenSecret());
        }

        // 3. Set log level
        System.setProperty("log.level", cfg.getServer().isDebug() ? "DEBUG" : "INFO");

        // 4. Initialize metadata store
        MetaStore metaStore = createMetaStore(cfg);
        log.info("数据库初始化完成");

        // 5. Load runtime config from DB
        RuntimeConfig.load(metaStore, cfg);
        log.info("运行时配置加载完成");

        // ============================================================
        // 6. Initialize cluster-mode components (Redis / S3)
        // ============================================================
        RedisClient redisClient = null;
        FileStore fileStore;
        PresenceStore presenceStore;
        SessionManager sessionMgr;

        if (cfg.getServer().isClusterMode()) {
            log.info("启动模式: 集群 (cluster)，初始化 Redis 和对象存储...");
            redisClient = new RedisClient(cfg);

            // 会话：Redis 存储
            sessionMgr = new SessionManager(new RedisSessionStore(redisClient));

            // 在线座席：Redis 存储
            presenceStore = new RedisPresenceStore(redisClient, metaStore);

            // 文件存储：S3/MinIO
            fileStore = new S3FileStore(cfg);
        } else {
            log.info("启动模式: 单例 (singleton)，使用进程内存 + 本地文件系统");

            // 会话：进程内存 + DB 落盘
            sessionMgr = new SessionManager(metaStore);

            // 在线座席：进程内存
            presenceStore = new MemoryPresenceStore(metaStore);

            // 文件存储：本地文件系统
            fileStore = new LocalFileStore();
        }

        // 7. Initialize client factory (lazy, reads config from DB)
        ClientFactory clientFactory = new ClientFactory(metaStore);

        // 8. Initialize knowledge base manager
        KnowledgeBaseManager kbManager = new KnowledgeBaseManager(cfg, metaStore, clientFactory, fileStore, redisClient);

        // 9. Start background file processing worker (only admin role)
        if (isAdmin) {
            kbManager.startFileWorker();
            log.info("文件处理 worker 已启动 (admin role)");
        }

        // 10. Create controllers
        PageController pageController = new PageController(cfg);
        AuthController authController = new AuthController(cfg, metaStore, presenceStore);
        FaqController faqController = new FaqController(metaStore, clientFactory);
        ChatController chatController = new ChatController(cfg, kbManager, sessionMgr, metaStore, clientFactory, faqController);
        VdbController vdbController = new VdbController(cfg, kbManager, metaStore, chatController.getCsmEngine());
        ConfigController configController = new ConfigController(cfg, metaStore, clientFactory);
        UserController userController = new UserController(metaStore);
        AgentController agentController = new AgentController(metaStore);
        WorkflowController workflowController = new WorkflowController(metaStore);

        // 11. Create router and register routes
        Router router = new Router();

        // -- Health --
        router.addRoute("GET", "/health", (ctx, req) -> {
            HttpServer.sendJson(ctx, 200, "{\"status\":\"ok\"}");
        });

        // -- Pages --
        router.addRoute("GET", "/login", pageController::loginPage);
        router.addRoute("GET", "/register", pageController::registerPage);

        // Chat pages (only chat role)
        if (isChat) {
            router.addRoute("GET", "/", pageController::index);
            router.addRoute("GET", "/user/api", pageController::userApiIndex);
        }

        // VDB page (shared)
        router.addRoute("GET", "/vdb/idx", pageController::vdbIndex);

        // Admin pages (only admin role)
        if (isAdmin) {
            router.addRoute("GET", "/admin/config", pageController::configIndex);
            router.addRoute("GET", "/admin/vdb/bind", pageController::vdbBindIndex);
        }

        // -- Auth API (JSON) --
        router.addRoute("POST", "/api/login", authController::login);
        router.addRoute("POST", "/api/logout", authController::logout);
        router.addRoute("POST", "/api/register", userController::register);
        router.addRoute("GET", "/api/me", authController::me);
        router.addRoute("GET", "/api/agents", authController::getOnlineAgents);

        // -- Chat API (only chat role) --
        if (isChat) {
            router.addRoute("POST", "/api/chat", chatController::chat);
            router.addRoute("POST", "/api/chat/sync", chatController::chatSync);
            router.addRoute("GET", "/api/chat/history", chatController::history);
            router.addRoute("POST", "/api/chat/clear", chatController::clear);
        }

        // -- Config API (read: shared, write: admin only) --
        router.addRoute("GET", "/api/config", configController::getConfig);
        router.addRoute("GET", "/api/info", configController::info);
        if (isAdmin) {
            router.addRoute("PUT", "/api/config", configController::updateConfig);
            router.addRoute("POST", "/api/config/test-models", configController::testModels);
        }

        // -- VDB API --
        // Read: shared
        router.addRoute("GET", "/api/vdb", vdbController::myList);
        router.addRoute("GET", "/api/vdb/pub", vdbController::pubList);
        router.addRoute("POST", "/api/vdb/search", vdbController::search);
        // Write: admin only
        if (isAdmin) {
            router.addRoute("POST", "/api/vdb", vdbController::create);
            router.addRoute("DELETE", "/api/vdb/:id", vdbController::delete);
            router.addRoute("PUT", "/api/vdb/:id/default", vdbController::setDefault);
            router.addRoute("GET", "/api/vdb/:id/files", vdbController::fileList);
            router.addRoute("POST", "/api/vdb/:id/upload", vdbController::upload);
            router.addRoute("GET", "/api/vdb/file/:id/progress", vdbController::processInfo);
            router.addRoute("GET", "/api/vdb/file/:id/chunks", vdbController::chunks);
            router.addRoute("GET", "/api/vdb/file/:id/download", vdbController::download);
            router.addRoute("DELETE", "/api/vdb/file/:id", vdbController::fileDelete);
            router.addRoute("GET", "/api/vdb/bindings", vdbController::bindingGet);
            router.addRoute("PUT", "/api/vdb/bindings", vdbController::bindingPut);
        }

        // -- FAQ API --
        // Read: shared
        router.addRoute("GET", "/api/faq", faqController::list);
        router.addRoute("GET", "/api/faq/template", faqController::template);
        router.addRoute("POST", "/api/faq/match", faqController::match);
        // Write: admin only
        if (isAdmin) {
            router.addRoute("POST", "/api/faq", faqController::create);
            router.addRoute("POST", "/api/faq/upload", faqController::upload);
            router.addRoute("PUT", "/api/faq/:id", faqController::update);
            router.addRoute("DELETE", "/api/faq/:id", faqController::delete);
            router.addRoute("DELETE", "/api/faq", faqController::clearAll);
        }

        // -- User API (admin) --
        if (isAdmin) {
            router.addRoute("GET", "/api/users", userController::listUsers);
            router.addRoute("POST", "/api/users", userController::createUser);
            router.addRoute("DELETE", "/api/users/:name", userController::deleteUser);
            router.addRoute("PUT", "/api/users/:name/reset-pwd", userController::resetUserPwd);
        }

        // -- User API (self-service, all roles) --
        router.addRoute("PUT", "/api/user/password", userController::changePassword);
        router.addRoute("GET", "/api/user/tokens", userController::listMyTokens);
        router.addRoute("POST", "/api/user/token", userController::generateToken);
        router.addRoute("GET", "/api/user/call-logs", userController::myCallLogs);

        // -- AI Agent API --
        // Read: shared
        router.addRoute("GET", "/api/system-vars", agentController::listSystemVars);
        router.addRoute("GET", "/api/ai-agents/public", agentController::listPublic);
        router.addRoute("GET", "/api/ai-agents", agentController::list);
        router.addRoute("GET", "/api/ai-agents/:id", agentController::get);
        // Write: chat role
        if (isChat) {
            router.addRoute("POST", "/api/ai-agents", agentController::create);
            router.addRoute("PUT", "/api/ai-agents/:id", agentController::update);
            router.addRoute("DELETE", "/api/ai-agents/:id", agentController::delete);
        }

        // -- Workflow API --
        // Read: shared
        router.addRoute("GET", "/api/workflows", workflowController::listPublic);
        router.addRoute("GET", "/api/workflows/:id", workflowController::get);
        // Write: admin only
        if (isAdmin) {
            router.addRoute("POST", "/api/workflows", workflowController::create);
            router.addRoute("PUT", "/api/workflows/:id", workflowController::update);
            router.addRoute("DELETE", "/api/workflows/:id", workflowController::delete);
        }

        // -- Classifier test (shared) --
        router.addRoute("POST", "/api/classifier/test", chatController::testClassifier);

        // ============================================================
        // Open API — 第三方开放接口（URL t=token 认证，网关层统一处理）
        // 与前端 /api/* 共用同一套核心 handler，仅入口与认证方式不同。
        // 仅开放查询/问答类能力，管理类写操作不对外开放。
        // ============================================================
        if (isChat) {
            router.addRoute("POST", "/open_api/chat", chatController::chat);
            router.addRoute("POST", "/open_api/chat/sync", chatController::chatSync);
            router.addRoute("GET", "/open_api/chat/history", chatController::history);
            router.addRoute("POST", "/open_api/chat/clear", chatController::clear);
        }
        router.addRoute("POST", "/open_api/vdb/search", vdbController::search);
        router.addRoute("GET", "/open_api/vdb", vdbController::myList);
        router.addRoute("GET", "/open_api/vdb/pub", vdbController::pubList);
        router.addRoute("POST", "/open_api/faq/match", faqController::match);
        router.addRoute("GET", "/open_api/faq", faqController::list);
        router.addRoute("GET", "/open_api/workflows", workflowController::listPublic);
        router.addRoute("GET", "/open_api/workflows/:id", workflowController::get);
        router.addRoute("GET", "/open_api/ai-agents", agentController::list);
        router.addRoute("GET", "/open_api/ai-agents/:id", agentController::get);
        router.addRoute("GET", "/open_api/agents", authController::getOnlineAgents);

        // 12. Start HTTP server
        HttpServer server = new HttpServer(cfg.getServer().getPort(), router, cfg);
        server.start();

        // 13. Register shutdown hook
        final RedisClient finalRedis = redisClient;
        final SessionManager finalSessMgr = sessionMgr;
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            log.info("正在关闭服务...");
            kbManager.stopFileWorker();
            finalSessMgr.stop();
            if (finalRedis != null) {
                finalRedis.close();
            }
            server.stop();
            metaStore.close();
            log.info("服务已关闭");
        }));

        log.info("服务启动: http://localhost:{}", cfg.getServer().getPort());
    }

    private static MetaStore createMetaStore(Config cfg) {
        String backend = cfg.getStore() != null ? cfg.getStore().getBackend() : "sqlite";

        if ("mysql".equals(backend)) {
            if (cfg.getMysql() == null || cfg.getMysql().getDsn() == null || cfg.getMysql().getDsn().isEmpty()) {
                System.err.println("错误: store.backend=mysql 但 mysql.dsn 为空");
                System.exit(1);
            }
            log.info("使用 MySQL 存储");
            return new MysqlMetaStore(cfg.getMysql().getDsn());
        }

        // SQLite default
        if (!new File("cfg.db").exists()) {
            System.err.println("错误: cfg.db 不存在，请将 cfg.db.template 复制为 cfg.db 后重新启动");
            System.exit(1);
        }
        log.info("使用 SQLite 存储");
        return new SqliteMetaStore("cfg.db");
    }
}