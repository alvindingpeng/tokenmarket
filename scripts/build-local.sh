#!/bin/bash
# 本地(单机构建/部署用)构建: 前端产物 + 注入版本信息的后端二进制。
#
# 为什么要这个脚本: 版本号只写在 main.go 的 "// Version" 标记里, 真正的二进制版本靠
# -ldflags 注入 internal/conf.Version; 前端则靠 VITE_APP_VERSION。两者必须取同一个值,
# 否则 设置 > 信息 会把 "前端版本 != 后端版本" 判成浏览器缓存问题而弹红色告警。
# CI 用的是 scripts/build.sh(交叉编译 + 归档), 那是发布产物流水线, 与此处部署二进制互不影响。
#
# 用法: bash scripts/build-local.sh [-o 输出路径]   默认输出 build/octopus-local
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="${REPO_ROOT}/build/octopus-local"

if [ "${1:-}" = "-o" ]; then
    [ -n "${2:-}" ] || { echo "用法: scripts/build-local.sh [-o 输出路径]" >&2; exit 2; }
    OUTPUT="$2"
fi

readonly VERSION="$(bash "${REPO_ROOT}/scripts/version.sh")"
readonly COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
readonly BUILD_TIME="$(TZ='Asia/Shanghai' date +'%F %T %z')"
readonly MODULE='github.com/bestruirui/octopus'
readonly LDFLAGS="-X '${MODULE}/internal/conf.Version=${VERSION}' \
                  -X '${MODULE}/internal/conf.BuildTime=${BUILD_TIME}' \
                  -X '${MODULE}/internal/conf.Commit=${COMMIT}' \
                  -s -w"

# static.go 用 embed 打进二进制, 所以必须先构建前端, 再构建后端, 顺序不能反。
# VITE_GITHUB_REPO 让「设置 > 信息」展示本项目仓库链接(留空则不显示该行)。
echo "== 前端 (VITE_APP_VERSION=${VERSION})"
(cd "${REPO_ROOT}/web" && VITE_APP_VERSION="${VERSION}" \
    VITE_GITHUB_REPO="${VITE_GITHUB_REPO:-https://github.com/alvindingpeng/tokenmarket}" pnpm run build)

echo "== 后端 ${VERSION} (${COMMIT}) -> ${OUTPUT}"
mkdir -p "$(dirname "${OUTPUT}")"
(cd "${REPO_ROOT}" && go build -trimpath -tags=jsoniter -ldflags="${LDFLAGS}" -o "${OUTPUT}" .)

echo "== 校验"
"${OUTPUT}" version
