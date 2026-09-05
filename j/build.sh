#!/bin/bash
# ============================================================
# build.sh — 构建 kb-chat-flow (Java版) Docker 镜像
#
# 用法:
#   ./build.sh                  # 默认 tag: kb-chat-flow:latest
#   ./build.sh v1.0.0           # 指定版本
#   ./build.sh v1.0.0 --push    # 构建并推送
#
# 多阶段构建:
#   Stage 1: Maven + JDK 17 编译 + ProGuard 混淆
#   Stage 2: JRE 17 Alpine 运行时
# ============================================================

set -euo pipefail

# ---- 配置 ----
IMAGE_NAME="${IMAGE_NAME:-kb-chat-flow}"
REGISTRY="${REGISTRY:-}"
DOCKERFILE="${DOCKERFILE:-Dockerfile}"

# ---- 颜色输出 ----
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*"; }

# ---- 解析参数 ----
TAG=""
PUSH=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --push) PUSH=true ;;
        -h|--help)
            echo "用法: $0 [<tag>] [--push]"
            echo ""
            echo "  <tag>      镜像标签，默认 latest"
            echo "  --push     构建后推送到仓库"
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
if ! command -v docker &>/dev/null; then
    err "docker 未安装或不在 PATH 中"
    exit 1
fi

# ---- 构建 Docker 镜像 ----
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

info "开始构建镜像: $FULL_IMAGE"

docker build -t "$FULL_IMAGE" -f "$DOCKERFILE" .

info "镜像构建完成: $FULL_IMAGE"

# ---- 可选推送 ----
if $PUSH; then
    if [[ -z "$REGISTRY" ]]; then
        err "推送需要设置 REGISTRY 环境变量"
        exit 1
    fi
    info "推送镜像: $FULL_IMAGE"
    docker push "$FULL_IMAGE"
    info "推送完成"
fi

# ---- 镜像信息 ----
echo ""
info "========== 镜像信息 =========="
docker images "$FULL_IMAGE" --format "table {{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedAt}}"
echo ""
info "启动命令:"
echo "  docker run -d --name kb-chat-flow -p 19007:19007 \\"
echo "    -v \$(pwd)/cfg.yml:/opt/csm/cfg.yml \\"
echo "    -v \$(pwd)/upload_doc:/opt/csm/upload_doc \\"
echo "    -v \$(pwd)/vdb:/opt/csm/vdb \\"
echo "    $FULL_IMAGE"
