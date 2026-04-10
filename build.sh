#!/bin/bash
# ============================================================
# xurl Executa Plugin Binary 构建脚本（Go）
# ============================================================
# 用法:
#   ./build.sh                  # 构建当前平台
#   ./build.sh --all            # 构建所有标准平台
#   ./build.sh --test           # 构建 + 协议测试
#   ./build.sh --package        # 构建 + 打包
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

export GOCACHE="${GOCACHE:-$SCRIPT_DIR/.cache/go-build}"

for arg in "$@"; do
    case "$arg" in
        --all)     BUILD_ALL=true ;;
        --test)    RUN_TEST=true ;;
        --package) PACKAGE=true; BUILD_ALL=true ;;
        --help|-h)
            echo "Usage: $0 [--all] [--test] [--package]"
            exit 0
            ;;
    esac
done

echo -e "${CYAN}============================================================${NC}"
echo -e "${CYAN}  xurl Executa Plugin Binary Builder${NC}"
echo -e "${CYAN}============================================================${NC}"
echo -e "  Plugin:   ${PLUGIN_NAME}"
echo -e "  Entry:    ${PLUGIN_ENTRY}"
echo -e "  Platform: $(uname -s) $(uname -m)"
echo -e "  Go:       $(go version 2>/dev/null || echo 'not installed')"
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
        echo -e "  Building ${plat}..."
        GOOS=$goos GOARCH=$goarch go build -ldflags="-s -w" -o "dist/${PLUGIN_NAME}-${plat}${suffix}" "$PLUGIN_ENTRY"
    done
    echo ""
    echo -e "${GREEN}全平台构建完成！${NC}"
    ls -lh dist/
else
    go build -ldflags="-s -w" -o "dist/${PLUGIN_NAME}" "$PLUGIN_ENTRY"
    SIZE=$(du -h "dist/${PLUGIN_NAME}" | cut -f1)
    echo -e "${GREEN}构建成功！${NC} dist/${PLUGIN_NAME} (${SIZE})"
fi

if [[ "$PACKAGE" == "true" ]]; then
    echo ""
    echo -e "${GREEN}打包...${NC}"
    mkdir -p dist/packages
    for f in dist/${PLUGIN_NAME}-*; do
        base=$(basename "$f")
        plat="${base#${PLUGIN_NAME}-}"
        plat="${plat%.exe}"
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
    [[ ! -f "$BINARY" ]] && BINARY=$(ls dist/${PLUGIN_NAME}-* 2>/dev/null | head -1)

    if [[ -f "$BINARY" && -x "$BINARY" ]]; then
        echo ""
        echo -e "${CYAN}── 协议测试 ──────────────────────────────────${NC}"

        echo -e "  [describe]..."
        RESULT=$(printf '%s\n' '{"jsonrpc":"2.0","method":"describe","id":1}' | "$BINARY" 2>/dev/null)
        if printf '%s' "$RESULT" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload["result"]["name"] == "xurl-executa"
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
assert payload["command_success"] is True
assert "xurl " in payload["stdout"]
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
echo -e "  在 Anna Admin 中上传 dist/packages 下对应平台压缩包"
echo ""
