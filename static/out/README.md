# static/out

这个目录里的构建产物**不入库**(见仓库根 `.gitignore` 的 `static/out/*` 与 `!static/out/README.md`)。

它由 `cd web && pnpm run build` 生成(输出目录即此处), 然后被 `static/static.go` 的
`//go:embed all:out` 打进二进制。

**本文件必须留在仓库里**: `go:embed all:out` 要求至少匹配到一个文件, 目录整个不存在时
`go build ./...` 会直接失败(`pattern all:out: no matching files found`)。
干净检出后若不先构建前端, 任何 import 了 `internal/server` 的编译/测试都会挂 ——
CI 的 Go 测试 job 正是这种形态, 保留本文件让它能先跑起来。
