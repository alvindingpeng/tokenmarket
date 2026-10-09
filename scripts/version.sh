#!/bin/bash
# 输出当前发布版本号(形如 v0.14.0)。
# 唯一事实来源是 main.go 的 "// Version vX.Y.Z" 标记 —— 上游 release workflow 也按同一行解析,
# 不要再新增 VERSION 文件之类的第二处定义, 否则两处会漂移。
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly MARKER="$(grep -m1 '^// Version ' "${REPO_ROOT}/main.go" | sed 's|^// Version ||' | tr -d '[:space:]')"

if [ -z "${MARKER}" ]; then
    echo "scripts/version.sh: main.go 缺少 '// Version vX.Y.Z' 版本标记" >&2
    exit 1
fi

printf '%s\n' "${MARKER}"
