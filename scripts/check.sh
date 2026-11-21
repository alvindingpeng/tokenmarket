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

step "历史资金审计工具: 只读与证据保留"
(cd "$ROOT" && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_financial_audit.py -v)
ok "read-only financial audit"

step "发布安全保护: 排空与补偿兼容性"
(cd "$ROOT" && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_financial_release.py -v)
ok "financial release safety"

step "第二批安全发布工具: 停机备份与数据验收"
(cd "$ROOT" && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_security_batch2_tools.py -v)
ok "security batch-2 release tools"

step "发布记录留存: 证据目录永不轮转"
(cd "$ROOT" && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_releases_retention.py -v)
ok "releases retention"

step "后端: 编译"
(cd "$ROOT" && go build ./...)
ok "go build"

# 发布产物矩阵里有 linux/386 与 linux/arm 两档 32 位目标, 其中 int 是 32 位:
# "2654435761 (untyped int constant) overflows int" 这类错在 amd64 上编译永远看不到,
# 直到 GitHub Actions 打产物时才失败(release-assets 因此连续失败、v1.10.16/17 的 Release
# 一直是空产物)。本机补一档 32 位编译, 让这类错在交付前就暴露。
step "后端: 32 位交叉编译（发布矩阵含 linux/386 与 linux/arm）"
(cd "$ROOT" && GOOS=linux GOARCH=386 CGO_ENABLED=0 go build ./... \
    && GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build ./...)
ok "go build (linux/386, linux/arm)"

step "后端: vet"
(cd "$ROOT" && go vet ./internal/... ./cmd/...)
ok "go vet"

step "后端: 单测"
(cd "$ROOT" && go test -count=1 ./internal/...)
ok "go test"

step "资金核心: 并发与退款回归（race）"
# -timeout 显式给到 30 分钟: go test 默认单包 10 分钟, 而整个 op 包在 -race 下实测约 11 分钟
# (530 个用例 + 每个用例独占一个 SQLite 库), 默认值会把一次正常的全绿跑判成超时 panic。
(cd "$ROOT" && go test -race -count=1 -timeout 30m ./internal/op)
ok "financial regression + race"

step "预扣续租生命周期: 并发回归（race）"
(cd "$ROOT" && go test -race -count=1 -run 'TestBillingLease|TestSetReservation' ./internal/relay)
ok "billing lease lifecycle + race"

step "前端: 类型"
(cd "$ROOT/web" && pnpm exec tsc -b --noEmit)
ok "tsc"

step "前端: lint"
(cd "$ROOT/web" && pnpm run lint)
ok "eslint"

step "前端: 单测"
(cd "$ROOT/web" && pnpm test)
ok "vitest"

step "公开状态页 e2e（无需登录）"
(cd "$ROOT/web" && pnpm run test:public)
ok "public status playwright"

step "首页入口 e2e（无需登录）"
(cd "$ROOT/web" && pnpm exec playwright test landing-status.spec.ts)
ok "landing status link"

step "结算补偿后台 e2e（隔离模拟，不访问生产数据）"
(cd "$ROOT/web" && pnpm exec playwright test billing-recovery.spec.ts)
ok "billing recovery dashboard"

step "布局 e2e"
if [[ -z "${SWITCHYARD_E2E_JWT:-}" ]]; then
    printf "   \033[33m跳过\033[0m 未设 SWITCHYARD_E2E_JWT(取法见 web/e2e/README.md)\n"
else
    (cd "$ROOT/web" && pnpm exec playwright test)
    ok "playwright"
fi

printf "\n\033[1;32m全部已执行检查通过（跳过项不代表通过）\033[0m\n"
