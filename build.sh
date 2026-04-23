#!/bin/bash
# ============================================================
# xurl Executa Plugin Binary 构建脚本（Go）
# ============================================================
# 用法:
#   ./build.sh                                  # 构建当前平台
#   ./build.sh --all                            # 构建所有标准平台
#   ./build.sh --target darwin/arm64            # 构建指定平台
#   ./build.sh --target windows/amd64 --package # 构建指定平台并打包
#   ./build.sh --test                           # 构建 + 协议测试
#   ./build.sh --package                        # 构建 + 打包
# ============================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

PLUGIN_NAME="xurl-executa"
PLUGIN_ENTRY="./cmd/executa-xurl"
BUILD_ALL=false
RUN_TEST=false
PACKAGE=false
TARGET_GOOS=""
TARGET_GOARCH=""
VERSION=""
COMMIT=""
BUILD_DATE=""

export GOCACHE="${GOCACHE:-$SCRIPT_DIR/.cache/go-build}"

usage() {
    echo "Usage: $0 [--all] [--target <goos/goarch>] [--test] [--package] [--version <value>] [--commit <value>] [--build-date <value>]"
}

platform_slug() {
    local goos=$1
    local goarch=$2

    case "${goos}/${goarch}" in
        darwin/amd64) echo "darwin-x86_64" ;;
        darwin/arm64) echo "darwin-arm64" ;;
        linux/amd64) echo "linux-x86_64" ;;
        linux/arm64) echo "linux-aarch64" ;;
        linux/arm) echo "linux-armv7l" ;;
        windows/amd64) echo "windows-x86_64" ;;
        windows/arm64) echo "windows-arm64" ;;
        *)
            echo "Unsupported target: ${goos}/${goarch}" >&2
            exit 1
            ;;
    esac
}

build_binary() {
    local goos=$1
    local goarch=$2
    local output=$3
    local ldflags
    ldflags="-s -w -X github.com/xdevplatform/xurl/version.Version=${VERSION} -X github.com/xdevplatform/xurl/version.Commit=${COMMIT} -X github.com/xdevplatform/xurl/version.BuildDate=${BUILD_DATE}"

    echo -e "  Building ${goos}/${goarch} -> ${output}..."
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -ldflags="$ldflags" -o "$output" "$PLUGIN_ENTRY"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all)
            BUILD_ALL=true
            shift
            ;;
        --target)
            shift
            [[ $# -gt 0 ]] || { usage; exit 1; }
            IFS='/' read -r TARGET_GOOS TARGET_GOARCH <<< "$1"
            [[ -n "$TARGET_GOOS" && -n "$TARGET_GOARCH" ]] || { usage; exit 1; }
            shift
            ;;
        --test)
            RUN_TEST=true
            shift
            ;;
        --package)
            PACKAGE=true
            shift
            ;;
        --version)
            shift
            [[ $# -gt 0 ]] || { usage; exit 1; }
            VERSION="$1"
            shift
            ;;
        --commit)
            shift
            [[ $# -gt 0 ]] || { usage; exit 1; }
            COMMIT="$1"
            shift
            ;;
        --build-date)
            shift
            [[ $# -gt 0 ]] || { usage; exit 1; }
            BUILD_DATE="$1"
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
done

if [[ "$BUILD_ALL" == "true" && -n "$TARGET_GOOS" ]]; then
    echo "Cannot use --all and --target together." >&2
    exit 1
fi

if [[ -z "$VERSION" ]]; then
    if VERSION_FROM_GIT=$(git describe --tags --always --dirty 2>/dev/null); then
        VERSION="$VERSION_FROM_GIT"
    else
        VERSION="dev"
    fi
fi

if [[ -z "$COMMIT" ]]; then
    if COMMIT_FROM_GIT=$(git rev-parse --short HEAD 2>/dev/null); then
        COMMIT="$COMMIT_FROM_GIT"
    else
        COMMIT="none"
    fi
fi

if [[ -z "$BUILD_DATE" ]]; then
    BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

echo -e "${CYAN}============================================================${NC}"
echo -e "${CYAN}  xurl Executa Plugin Binary Builder${NC}"
echo -e "${CYAN}============================================================${NC}"
echo -e "  Plugin:   ${PLUGIN_NAME}"
echo -e "  Entry:    ${PLUGIN_ENTRY}"
echo -e "  Platform: $(uname -s) $(uname -m)"
echo -e "  Go:       $(go version 2>/dev/null || echo 'not installed')"
echo -e "  Version:  ${VERSION}"
echo -e "  Commit:   ${COMMIT}"
echo -e "  Built at: ${BUILD_DATE}"
echo ""

rm -rf dist/
mkdir -p dist "$GOCACHE"

if [[ "$BUILD_ALL" == "true" ]]; then
    declare -A PLATFORMS=(
        ["darwin-arm64"]="darwin arm64"
        ["darwin-x86_64"]="darwin amd64"
        ["linux-x86_64"]="linux amd64"
        ["linux-aarch64"]="linux arm64"
        ["linux-armv7l"]="linux arm"
        ["windows-x86_64"]="windows amd64"
        ["windows-arm64"]="windows arm64"
    )

    for plat in "${!PLATFORMS[@]}"; do
        read -r goos goarch <<< "${PLATFORMS[$plat]}"
        suffix=""
        [[ "$goos" == "windows" ]] && suffix=".exe"
        build_binary "$goos" "$goarch" "dist/${PLUGIN_NAME}-${plat}${suffix}"
    done
    echo ""
    echo -e "${GREEN}全平台构建完成！${NC}"
    ls -lh dist/
elif [[ -n "$TARGET_GOOS" ]]; then
    TARGET_SLUG=$(platform_slug "$TARGET_GOOS" "$TARGET_GOARCH")
    TARGET_SUFFIX=""
    [[ "$TARGET_GOOS" == "windows" ]] && TARGET_SUFFIX=".exe"
    build_binary "$TARGET_GOOS" "$TARGET_GOARCH" "dist/${PLUGIN_NAME}-${TARGET_SLUG}${TARGET_SUFFIX}"
    echo ""
    ls -lh dist/
else
    CURRENT_GOOS="$(go env GOOS)"
    CURRENT_GOARCH="$(go env GOARCH)"
    CURRENT_BINARY="dist/${PLUGIN_NAME}"
    if [[ "$CURRENT_GOOS" == "windows" ]]; then
        CURRENT_BINARY="${CURRENT_BINARY}.exe"
    fi
    build_binary "$CURRENT_GOOS" "$CURRENT_GOARCH" "$CURRENT_BINARY"
    SIZE=$(du -h "$CURRENT_BINARY" | cut -f1)
    echo -e "${GREEN}构建成功！${NC} ${CURRENT_BINARY} (${SIZE})"
fi

if [[ "$PACKAGE" == "true" ]]; then
    echo ""
    echo -e "${GREEN}打包...${NC}"
    mkdir -p dist/packages
    declare -a PACKAGE_TARGETS=()
    shopt -s nullglob
    if [[ -f "dist/${PLUGIN_NAME}" ]]; then
        PACKAGE_TARGETS+=("dist/${PLUGIN_NAME}")
    fi
    if [[ -f "dist/${PLUGIN_NAME}.exe" ]]; then
        PACKAGE_TARGETS+=("dist/${PLUGIN_NAME}.exe")
    fi
    for f in dist/${PLUGIN_NAME}-*; do
        PACKAGE_TARGETS+=("$f")
    done
    shopt -u nullglob

    if [[ ${#PACKAGE_TARGETS[@]} -eq 0 ]]; then
        echo "No build outputs found under dist/ to package." >&2
        exit 1
    fi

    for f in "${PACKAGE_TARGETS[@]}"; do
        base=$(basename "$f")
        if [[ "$base" == "${PLUGIN_NAME}" || "$base" == "${PLUGIN_NAME}.exe" ]]; then
            plat=$(platform_slug "$(go env GOOS)" "$(go env GOARCH)")
        else
            plat="${base#${PLUGIN_NAME}-}"
            plat="${plat%.exe}"
        fi
        if [[ "$f" == *.exe ]]; then
            (cd dist && zip -j "packages/${PLUGIN_NAME}-${plat}.zip" "$base")
        else
            (cd dist && tar czf "packages/${PLUGIN_NAME}-${plat}.tar.gz" "$base")
        fi
    done
    echo ""
    ls -lh dist/packages/
fi

if [[ "$RUN_TEST" == "true" ]]; then
    BINARY="dist/${PLUGIN_NAME}"
    [[ ! -f "$BINARY" && -f "dist/${PLUGIN_NAME}.exe" ]] && BINARY="dist/${PLUGIN_NAME}.exe"
    [[ ! -f "$BINARY" ]] && BINARY=$(ls dist/${PLUGIN_NAME}-* 2>/dev/null | head -1)

    if [[ -f "$BINARY" && -x "$BINARY" ]]; then
        echo ""
        echo -e "${CYAN}── 协议测试 ──────────────────────────────────${NC}"

        echo -e "  [describe]..."
        RESULT=$(printf '%s\n' '{"jsonrpc":"2.0","method":"describe","id":1}' | "$BINARY" 2>/dev/null)
        if printf '%s' "$RESULT" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["result"]["name"] == "tool-lightvoss_5433-xurl-executa-6rbgfeke"
' 2>/dev/null; then
            echo -e "  ${GREEN}✅ describe 通过${NC}"
        else
            echo -e "  ${RED}❌ describe 失败${NC}"
        fi

        echo -e "  [invoke]..."
        TOKEN_FILE="dist/test-oauth2-token.json"
        cat > "$TOKEN_FILE" <<'EOF'
{
  "Client ID": "dummy-client-id",
  "Client Secret": "dummy-client-secret",
  "Access Token": "dummy-access-token",
  "Refresh Token": "dummy-refresh-token"
}
EOF
        RESULT=$(printf '%s\n' "{\"jsonrpc\":\"2.0\",\"method\":\"invoke\",\"params\":{\"tool\":\"run_xurl\",\"arguments\":{\"args\":[\"version\"],\"cwd\":\"./dist\"},\"context\":{\"credentials\":{\"X_OAUTH2_TOKEN_FILE\":\"$TOKEN_FILE\"}}},\"id\":2}" | "$BINARY" 2>/dev/null)
        if printf '%s' "$RESULT" | python3 -c '
import json, os, sys
pointer = json.load(sys.stdin)
path = pointer["__file_transport"]
with open(path, "r", encoding="utf-8") as f:
    payload = json.load(f)
assert payload["result"]["success"] is True
assert payload["result"]["tool"] == "run_xurl"
assert payload["result"]["data"]["command_success"] is True
assert "xurl " in payload["result"]["data"]["stdout"]
os.remove(path)
os.remove("dist/test-oauth2-token.json")
' 2>/dev/null; then
            echo -e "  ${GREEN}✅ invoke 通过${NC}"
        else
            echo -e "  ${RED}❌ invoke 失败${NC}"
        fi

        echo -e "  [health]..."
        RESULT=$(printf '%s\n' '{"jsonrpc":"2.0","method":"health","id":3}' | "$BINARY" 2>/dev/null)
        if printf '%s' "$RESULT" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["result"]["status"] == "healthy"
' 2>/dev/null; then
            echo -e "  ${GREEN}✅ health 通过${NC}"
        else
            echo -e "  ${RED}❌ health 失败${NC}"
        fi
    else
        echo -e "${YELLOW}未找到可执行二进制${NC}"
    fi
fi

echo ""
echo -e "${CYAN}── 下一步 ────────────────────────────────────${NC}"
echo -e "  ./build.sh --package"
echo -e "  ./build.sh --target darwin/arm64 --package"
echo -e "  在 Anna Admin 中上传 dist/packages 下对应平台压缩包"
echo ""
