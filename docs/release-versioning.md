# 版本号与更新说明发布流程

本项目每次更新都要**带版本号**并把**更新说明同步到 GitHub**。版本号只有一个事实来源，
更新说明也只有一个事实来源，其余渠道都由脚本派生，避免多处手工维护互相漂移。

## 事实来源

| 内容 | 位置 | 谁读它 |
| --- | --- | --- |
| 版本号 | `main.go` 里的 `// Version vX.Y.Z` 标记 | `scripts/version.sh`、`.github/workflows/release-assets.yaml`、`scripts/publish-release.sh` |
| 更新说明 | `CHANGELOG.md` 中对应的 `## vX.Y.Z` 章节 | `release-assets.yaml` → GitHub Release 正文 |

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
3. 验证本轮改动，提交并推送到 `tokenmarket` 的 `stable` 分支。每轮优化使用一个新版本号。
4. GitHub Actions 验证本次提交；测试通过后构建含嵌入式前端的发行包并发布同名标签、Release 和校验和。
5. 在 GitHub 核对标签对应的提交和发布产物。生产部署走独立流程。

`stable` 从 v1.16.0 标签建立，作为后续版本的发布分支。其他分支需要按现有流程推送代码及版本标签。
`scripts/publish-release.sh` 校验远端分支和本地提交一致，必要时推送标签。Release 由测试通过后的工作流创建。

## 版本号在两棵 worktree 之间是**共享命名空间**(2026-11-22 事故)

本仓库用 git worktree 并行开发两棵树：基线版（`octopus-baseline`，分支 `baseline-v1.10.13`，
发布到 `tokenmarket`）与 SaaS 版（`octopus`，分支 `master`，发布到 `switchyard-saas`）。
它们**共用同一个 git common dir**，因此也**共用 `refs/tags`** —— 标签命名空间是全局的，
不是每棵树一份。

事故经过：SaaS 线先在 2026-11-20 发了 `v1.11.0`（多租户 SaaS 能力）。之后基线线也把版本号写成
`v1.11.0`，提交时 `scripts/publish-release.sh` 发现"标签已存在"就**静默复用它**，
`git rev-parse refs/tags/v1.11.0^{commit}` 于是解析到**另一棵树上的 SaaS 提交**。结果：

- 那个 SaaS 提交被当作"本次发布的提交"推给了基线仓库 `tokenmarket`；
- `tokenmarket` 上出现了一条指向 SaaS 提交、正文却是基线 CHANGELOG 的 Release。

标签是"某提交在本仓库可达"的声明；指向不可达提交时，这个声明就是假的。

### 现在的守卫

发版脚本要求本地版本标签与当前提交一致，Git 会拒绝覆盖已存在的远端标签。
工作流上传产物前也会检查版本标记及标签指向。

### 对版本号的实际影响

两条产品线**不能各自从 v0 独立计数**，否则迟早再撞一次。基线版跳过已被占用的 `v1.11.0`，
直接发 `v1.12.0`。以后发版前先查：

```bash
git tag -l 'v1.1*' | sort -V | tail -5     # 标签在两棵树之间共享
git rev-parse --abbrev-ref HEAD            # 确认自己在哪条线上
```

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
bash scripts/publish-release.sh        # 检查已推送的版本，必要时推送标签
```

- 日志 `SKIP CHANGELOG.md 里没有 vX.Y.Z 章节` → 忘了写章节，或 `main.go` 标记与章节标题不一致。
- 日志 `SKIP 没有可用的 GITHUB_TOKEN` → `github` 远端 URL 里没有内嵌 PAT，
  使用已配置的 Git 远端认证。
- GitHub Actions 也可以发布（仓库 Actions 权限已开），但那要求 runner 能拿到 secrets；
  本地钩子路径不依赖 Actions，改代码机即发布机，所以选它。
