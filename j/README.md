# kb-chat-flow (Java 版)

知识库问答机器人 — 基于 Netty 的轻量级 Java HTTP 服务，集成 LLM、向量检索、FAQ 匹配等功能。

## 运行环境

| 项目 | 最低要求 |
|---|---|
| **JDK** | **17** |
| **Maven** | 3.6+ |
| **操作系统** | Linux / macOS / Windows |

> ⚠️ **JDK 版本说明**：本项目使用了 `var`、`record`、文本块（`"""`）、Switch 表达式等 Java 新语法，并且依赖库（Netty、Jackson、PDFBox、Milvus SDK 等）均要求 Java 17+，**无法在 JDK 8/11 下编译或运行**。

### 可选外部依赖

| 组件 | 说明 |
|---|---|
| **fasttext** | 意图分类器，需安装 `fasttext` 二进制（Alpine: `apk add fasttext`） |
| **Redis** | 集群模式下会话共享、登录状态存储 |
| **MySQL** | 集群模式下元数据存储（单机模式使用 SQLite） |
| **Milvus / Qdrant** | 远程向量数据库（单机模式使用本地 SQLite 向量存储） |
| **MinIO / S3** | 集群模式下文件存储（单机模式使用本地文件系统） |

## 编译

```bash
# 编译打包（跳过测试）
mvn clean package -DskipTests

# 输出: target/kb-chat-flow-1.0.0.jar
```

## 运行

```bash
# 确保当前目录有 cfg.yml 配置文件
java -jar target/kb-chat-flow-1.0.0.jar
```

## 配置文件

启动前需要在工作目录准备 `cfg.yml`，参考模板：`cfg.yml.template`。

## Docker 构建

```bash
./build.sh          # 构建 latest 标签
./build.sh v1.0.0   # 构建指定版本
```

## 项目结构

```
src/main/java/com/rd/robot/
├── Bootstrap.java          # 入口，启动 Netty HTTP 服务
├── client/                 # LLM / Embedding / Rerank HTTP 客户端
├── config/                 # 配置解析
├── engine/                 # CSM 引擎、工作流引擎、意图分类器
├── fasttext/               # FastText 预测封装
├── knowledge/              # 知识库管理、文件存储、文本提取
├── logger/                 # 日志配置
├── model/                  # 数据模型
├── redis/                  # Redis 客户端
├── repository/             # 元数据存储（SQLite / MySQL）
├── security/               # Token 认证、密码加密
├── session/                # 会话管理（内存 / Redis）
├── vector/                 # 向量存储（本地 SQLite / Milvus / Qdrant）
└── web/
    ├── controller/         # HTTP API 控制器
    ├── interceptor/        # API 调用日志拦截器
    └── router/             # 路由定义
```