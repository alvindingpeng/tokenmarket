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
4. 部署 + 回归（本仓库部署见 `switchyard-mobile-deploy.py`，回归 `switchyard-e2e-billing.py`）。
5. `git commit`。提交后 `post-commit` 钩子自动完成：
   - `git push github HEAD:refs/heads/<当前分支>`（实时推送，日志 `.git/auto-push.log`）。
     目标分支取自提交所在的分支，**不硬编码**：本仓库的提交落在 `master` 上，
     而远端 `main` 自 v1.5.6 起已与 `master` 分叉未再跟进，硬编码 `main` 会让每次
     自动推送都以 non-fast-forward 失败。分离头指针时回落到 `master`。
   - `scripts/publish-release.sh`：本地无 `vX.Y.Z` 标签则 `git tag -a` 并推送标签，
     再用 CHANGELOG 对应章节 POST/PATCH GitHub Release（日志 `.git/publish-release.log`）。

> 注：GitHub 仓库的**默认分支仍是 `main`**（即 v1.5.6 时代的内容），所以仓库首页展示的不是最新代码。
> 需要的话可在 GitHub 设置里把默认分支改成 `master`；`main` 分支本身未做删除或强制同步 ——
> 它含有一个 `master` 上没有的提交（v1.5.3–v1.5.6 的整批落库），两边历史已分叉，贸然覆盖会丢东西。

## 部署时的健康检查窗口(v1.10.22 教训)

替换二进制后的健康等待**不能拍一个「差不多」的数字**, 必须按实测启动耗时给足倍数。

v1.10.22 首次部署被自己的健康检查判成失败并自动回滚, 根因不是新二进制坏, 而是等待太短:

| 观测项 | 实测值 |
|---|---|
| systemd `Started` → 进程开始输出 banner | ~0s |
| banner → `secret master key ready` | <1s |
| `secret master key ready` → `secret at-rest verified` | **~36s** |
| `secret at-rest verified` → `Program started`, `/healthz` 可用 | 同一秒 |

那 36 秒花在启动期的**全表 secret 校验**(生产 encrypt 行数十级)与本机路由状态恢复
(189 组 / 176 指标 / 7 出口)。启动耗时随加密行数与路由规模增长, 不是常数。

因此:

- **等待窗口取实测值的 4 倍以上**(本次用 150s, 实际 26s 起来)。`deploy/swap_deploy.sh` 的
  `HEALTH_RETRIES`(默认 20 次 × (5s curl + 1s 间隔)) 上界约 120s, 对当前规模够用,
  但换更大库时要么调大 `SWITCHYARD_HEALTH_RETRIES`, 要么先测一次冷启动耗时。
- **判定失败后先去日志看新进程死在哪一步**, 再决定回滚还是继续等。日志里出现
  `Version: vX.Y.Z` + `secret master key ready` 说明新进程已经正常跑起来了 ——
  此时回滚是误杀, 不是修复。
- 回滚本身是安全的(原子替换 + 旧二进制留档 + 库未被破坏), 但「回滚成功」不等于
  「新版本有问题」: 这两种结论必须由日志区分, 不能由退出码代替。

## 版本号的运行时呈现

- 后端：`-ldflags -X internal/conf.Version` 注入，`switchyard version`、`/healthz` 的
  `version` 字段、`/metrics` 的 `switchyard_build_info` 都取它；`/api/v1/update/now-version`
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
