# 版本号与更新说明发布流程

本项目每次更新都要**带版本号**并把**更新说明同步到 GitHub**。版本号只有一个事实来源，
更新说明也只有一个事实来源，其余渠道都由脚本派生，避免多处手工维护互相漂移。

## 事实来源

| 内容 | 位置 | 谁读它 |
| --- | --- | --- |
| 版本号 | `main.go` 里的 `// Version vX.Y.Z` 标记 | `scripts/version.sh`、上游 `.github/workflows/release.yaml`、`scripts/publish-release.sh` |
| 更新说明 | `CHANGELOG.md` 中对应的 `## vX.Y.Z` 章节 | `scripts/publish-release.sh` → GitHub Release 正文 |

> 不要再新增 `VERSION` 文件、`web/package.json` version 之类的第二处定义。上游 release
> workflow 用 `grep '^// Version ' main.go` 解析版本，改格式会静默弄断 CI 发布。

## 版本递增约定（语义化版本）

- 新增用户可见能力（新功能、新页面、新协议路由）→ **次版本号** `v0.14.0 → v0.15.0`
- 仅缺陷修复、样式修复、文案与文档 → **修订号** `v0.14.0 → v0.14.1`
- 不兼容的数据库/接口变更 → **主版本号**（本项目暂无此打算）
- 一次性攒够多个功能再发版时，仍然只 +1 次版本号，内容写进同一章节

## 一次更新的完整动作

1. 改代码，`CHANGELOG.md` 顶部写（或补写）本次 `## vX.Y.Z` 章节。
2. 把 `main.go` 的 `// Version` 改成同一个 `vX.Y.Z`。
3. `bash scripts/build-local.sh` —— 前端与后端**必须同时重建**：`static/static.go` 用 embed
   把 `static/out` 打进二进制，顺序反了或只重建一边，二进制里就是旧前端。
4. 部署 + 回归（本仓库部署见 `octopus-mobile-deploy.py`，回归 `octopus-e2e-billing.py`）。
5. `git commit`。提交后 `post-commit` 钩子自动完成：
   - `git push github HEAD:refs/heads/main`（实时推送，日志 `.git/auto-push.log`）
   - `scripts/publish-release.sh`：本地无 `vX.Y.Z` 标签则 `git tag -a` 并推送标签，
     再用 CHANGELOG 对应章节 POST/PATCH GitHub Release（日志 `.git/publish-release.log`）。

## 版本号的运行时呈现

- 后端：`-ldflags -X internal/conf.Version` 注入，`octopus version`、`/healthz` 的
  `version` 字段、`/metrics` 的 `octopus_build_info` 都取它；`/api/v1/update/now-version`
  把它返回给前端。
- 前端：构建期 `VITE_APP_VERSION` 注入，显示在「设置 > 信息」。
- 两者必须同源：不一致时「设置 > 信息」会判定为浏览器缓存问题并弹红色告警 + 强制刷新按钮，
  那是误报而不是真有新版本 —— `scripts/build-local.sh` 已从 `scripts/version.sh` 取同一个值喂给两边。
- `VITE_GITHUB_REPO` 同样在 `scripts/build-local.sh` 里给出默认值，让「设置 > 信息」
  显示本项目仓库链接（留空该行不渲染）。

## 手工发布 / 排错

改了 CHANGELOG 想覆盖已发布的 Release 正文：

```bash
bash scripts/publish-release.sh        # 幂等: 标签已存在不重复打, Release 已存在则 PATCH 正文
```

- 日志 `SKIP CHANGELOG.md 里没有 vX.Y.Z 章节` → 忘了写章节，或 `main.go` 标记与章节标题不一致。
- 日志 `SKIP 没有可用的 GITHUB_TOKEN` → `github` 远端 URL 里没有内嵌 PAT，
  临时 `GITHUB_TOKEN=... bash scripts/publish-release.sh`。
- GitHub Actions 也可以发布（仓库 Actions 权限已开），但那要求 runner 能拿到 secrets；
  本地钩子路径不依赖 Actions，改代码机即发布机，所以选它。
