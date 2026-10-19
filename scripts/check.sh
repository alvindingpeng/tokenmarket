#!/usr/bin/env bash
# 交付前闸门: 一次跑完全部检查, 任一失败即非零退出。
#
# 三条流水线各管一段:
#   后端     编译 + vet + 单测     —— 形状/契约类回归(如 v0.39.6 的审核字段漏拷)
#   前端单测 类型 + lint + vitest  —— 纯函数与取 key 的回落逻辑
#   布局 e2e Playwright           —— 真实浏览器里算一遍布局(弹窗/面板塌陷这一族)
#
# 布局 e2e 需要一个登录后的 JWT: 设 SWITCHYARD_E2E_JWT 就跑, 没设就跳过并显式说明。
# 它验的是工作区源码(vite dev 现场编译), 不是已部署产物 —— 打产物等于验上一次构建, 改坏了照样绿。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-/srv/deepseek-harness/workspaces/.toolchain/go/bin}"
export PATH="$GO_BIN:$PATH"

step() { printf "\n\033[1m== %s ==\033[0m\n" "$1"; }
ok()   { printf "   \033[32mok\033[0m %s\n" "$1"; }

step "后端: 编译"
(cd "$ROOT" && go build ./...)
ok "go build"

step "后端: vet"
(cd "$ROOT" && go vet ./internal/... ./cmd/...)
ok "go vet"

step "后端: 单测"
(cd "$ROOT" && go test -count=1 ./internal/...)
ok "go test"

step "前端: 类型"
(cd "$ROOT/web" && pnpm exec tsc -b --noEmit)
ok "tsc"

step "前端: lint"
(cd "$ROOT/web" && pnpm run lint)
ok "eslint"

step "前端: 单测"
(cd "$ROOT/web" && pnpm test)
ok "vitest"

step "布局 e2e"
if [[ -z "${SWITCHYARD_E2E_JWT:-}" ]]; then
    printf "   \033[33m跳过\033[0m 未设 SWITCHYARD_E2E_JWT(取法见 web/e2e/README.md)\n"
else
    (cd "$ROOT/web" && pnpm exec playwright test)
    ok "playwright"
fi

printf "\n\033[1;32m全部通过\033[0m\n"
