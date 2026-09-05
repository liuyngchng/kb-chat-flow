#!/bin/bash
# ============================================================
# build.sh — 构建 kb-chat-flow Docker 镜像
#
# 用法:
#   ./build.sh                  # 默认 tag: kb-chat-flow:latest
#   ./build.sh v1.0.0           # 指定版本
#   ./build.sh v1.0.0 --push    # 构建并推送
#
# 流程:
#   1. 本地 (Ubuntu) 静态编译 Go 二进制 (garble 混淆)
#   2. 打包进 Alpine 运行时镜像
# ============================================================

set -euo pipefail

# ---- 配置 ----
IMAGE_NAME="${IMAGE_NAME:-kb-chat-flow}"
REGISTRY="${REGISTRY:-}"                        # 镜像仓库地址，如 registry.cn-hangzhou.aliyuncs.com/your-ns
BINARY="${BINARY:-kb-chat-flow}"

# ---- 颜色输出 ----
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*"; }

# ---- 解析参数 ----
TAG=""
PUSH=false
WINDOWS=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --push) PUSH=true ;;
        --windows) WINDOWS=true ;;
        -h|--help)
            echo "用法: $0 [<tag>] [--push] [--windows]"
            echo ""
            echo "  <tag>      镜像标签，默认 latest"
            echo "  --push     构建后推送到仓库"
            echo "  --windows  交叉编译 Windows amd64 exe（跳过 Docker 打包）"
            echo ""
            echo "环境变量:"
            echo "  IMAGE_NAME   镜像名，默认 kb-chat-flow"
            echo "  REGISTRY     仓库地址，如 registry.cn-hangzhou.aliyuncs.com/my-ns"
            exit 0
            ;;
        *) TAG="$1" ;;
    esac
    shift
done

TAG="${TAG:-latest}"

if [[ -n "$REGISTRY" ]]; then
    FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${TAG}"
else
    FULL_IMAGE="${IMAGE_NAME}:${TAG}"
fi

# ---- 检查前提 ----
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if ! command -v go &>/dev/null; then
    err "go 未安装或不在 PATH 中"
    exit 1
fi

if ! $WINDOWS && ! command -v docker &>/dev/null; then
    err "docker 未安装或不在 PATH 中"
    exit 1
fi

# ---- Step 1: 本地编译 ----
cd "$SCRIPT_DIR"

# 删除旧二进制，防止手动 go build（非 CGO_ENABLED=0）残留的产物混入
rm -f "$BINARY"

if $WINDOWS; then
    BINARY_NAME="${BINARY}.exe"
    info "交叉编译 Windows amd64 exe (CGO_ENABLED=0, garble -literals)..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOTOOLCHAIN=local GOGARBLE=kb-chat-flow \
        go run mvdan.cc/garble@v0.14.2 -literals build -o "$BINARY_NAME" .
else
    BINARY_NAME="$BINARY"
    info "本地编译 Go 二进制 (CGO_ENABLED=0, garble -literals)..."
    CGO_ENABLED=0 GOTOOLCHAIN=local GOGARBLE=kb-chat-flow \
        go run mvdan.cc/garble@v0.14.2 -literals build -o "$BINARY_NAME" .
fi

# 自检：确认产物是静态链接
if ! file "$BINARY_NAME" | grep -q "statically linked"; then
    err "编译产物不是静态链接！请检查 CGO_ENABLED 设置"
    exit 1
fi
info "编译完成: $SCRIPT_DIR/$BINARY_NAME（静态链接）"

# --windows 模式：只编译，跳过 Docker 和 release 打包
if $WINDOWS; then
    info "Windows exe 编译完成，跳过 Docker 打包"
    exit 0
fi

# ---- Step 2: 准备 Docker 构建上下文 ----
BUILD_DIR="$(mktemp -d -t kb-chat-flow_build_XXXXXX)"
trap "rm -rf $BUILD_DIR" EXIT
info "准备构建上下文: $BUILD_DIR"

cp "$SCRIPT_DIR/$BINARY" "$BUILD_DIR/"
cp "$SCRIPT_DIR/Dockerfile" "$BUILD_DIR/"
cp "$SCRIPT_DIR/cfg.yml.template" "$BUILD_DIR/cfg.yml.template"
cp -r "$SCRIPT_DIR/dt" "$BUILD_DIR/dt"
cp -r "$SCRIPT_DIR/vdb" "$BUILD_DIR/vdb"

# ---- Step 3: 打 Docker 镜像 ----
info "构建镜像: $FULL_IMAGE"
docker build -t "$FULL_IMAGE" "$BUILD_DIR"
info "镜像构建完成: $FULL_IMAGE"

# ---- Step 4: 可选推送 ----
if $PUSH; then
    if [[ -z "$REGISTRY" ]]; then
        err "推送需要设置 REGISTRY 环境变量"
        exit 1
    fi
    info "推送镜像: $FULL_IMAGE"
    docker push "$FULL_IMAGE"
    info "推送完成"
fi

# ---- Step 5: 打包 release ----
# 解压后目录名固定为 kb-chat-flow（不随 tag 变化）
RELEASE_DIR="$SCRIPT_DIR/kb-chat-flow-release/kb-chat-flow"
RELEASE_TAR="$SCRIPT_DIR/kb-chat-flow-release-${TAG}.tar.gz"
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR"

info "打包 release..."

# docker save 镜像
docker save -o "$RELEASE_DIR/${IMAGE_NAME}.tar" "$FULL_IMAGE"

# 配置文件
if [[ -f "$SCRIPT_DIR/cfg.yml" ]]; then
    cp "$SCRIPT_DIR/cfg.yml" "$RELEASE_DIR/cfg.yml"
else
    cp "$SCRIPT_DIR/cfg.yml.template" "$RELEASE_DIR/cfg.yml"
    warn "  cfg.yml 从 template 复制，请编辑后再交付"
fi

# 数据库
if [[ -f "$SCRIPT_DIR/cfg.db" ]]; then
    cp "$SCRIPT_DIR/cfg.db" "$RELEASE_DIR/cfg.db"
fi

# fastText 训练好的模型（运行时直接加载，不重训）
if [[ -d "$SCRIPT_DIR/dt" ]]; then
    cp -r "$SCRIPT_DIR/dt" "$RELEASE_DIR/dt"
else
    warn "  dt 目录不存在，fastText 模型未打包（首次启动会重新训练）"
fi

# 知识库向量数据（每库一个 kb_<id>.db，随包交付，运行时挂载读取）
if [[ -d "$SCRIPT_DIR/vdb" ]]; then
    cp -r "$SCRIPT_DIR/vdb" "$RELEASE_DIR/vdb"
else
    warn "  vdb 目录不存在，知识库向量数据未打包"
fi

# 启动脚本
cp "$SCRIPT_DIR/deploy.sh" "$RELEASE_DIR/deploy.sh"

# 打包 tar.gz（顶层目录名固定为 kb-chat-flow）
cd "$SCRIPT_DIR/kb-chat-flow-release"
tar czf "$RELEASE_TAR" "kb-chat-flow"
cd "$SCRIPT_DIR"
rm -rf "$RELEASE_DIR"

# ---- 镜像信息 ----
echo ""
info "========== 镜像信息 =========="
docker images "$FULL_IMAGE" --format "table {{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedAt}}"
echo ""
info "========== Release =========="
ls -lh "$RELEASE_TAR"
echo ""
info "交付步骤:"
echo "  1. 将 $(basename "$RELEASE_TAR") 拷贝给运维"
echo "  2. 运维执行:"
echo "     tar xzf $(basename "$RELEASE_TAR")"
echo "     cd kb-chat-flow"
echo "     docker load < ${IMAGE_NAME}.tar"
echo "     ./deploy.sh"
