# 更新说明

本项目遵循[语义化版本](https://semver.org/lang/zh-CN/)，版本号唯一来源是 `main.go` 里的
`// Version vX.Y.Z` 标记（`scripts/version.sh` 读取它，CI 的 release workflow 也解析同一行）。
每次发布把新章节追加到本文件顶部，GitHub Release 的正文由 `scripts/publish-release.sh`
自动取本文件中对应版本的那一节。

## v0.14.0 — 2026-10-09

基于上游 v0.13.9 的本项目第一批完整发布，汇总此前未成版本的全部开发与修复。

### 新增功能

- **渠道模型自动添加（v1）**：渠道编辑页新增「自动添加」开关，保存渠道后探测上游 `/models`
  并入渠道。语义为只增不删、授权按位 OR 并入、单次上限 200 个模型、写入
  `channel.model-added` 审计；探测逻辑抽到 `internal/op/channel_probe.go`，与手动刷新共用同一实现。
- **生图支持**：`/v1/images/generations` 与 `/v1/images/edits` 两条 OpenAI Images 协议路由完整转发，
  按张计费；预扣保守张数由 `balance_reserve_images` 参数化（默认 4）。
- **计费与对账**：消费明细（筛选 + 分页 + CSV 导出）、用户余额流水、价格快照与对账、
  低余额告警、审计日志 CSV 导出。
- **运营可靠性两批**：渠道健康探测（延迟超阈值判 degraded、连续 5 次失败才判 down、恢复补发
  recovered）、告警规则可配置、告警闭环、自动备份（加密 / WebDAV 异地推送 / 校验和）、
  日志生命周期、限流实时用量、`/healthz` 与 `/metrics`（含 `metrics_auth` 鉴权选项）、运维概览。
- **渠道模型改价工作台**：发布管理内直接维护渠道商供货价，用户价按全局上浮比例联动。
- **分组编辑置顶/置底**（继承上游 `0538c3e`）、**子路径部署**用于反向代理场景（继承上游 `bdc9948`）。
- **系统信息配置**：站点名称 / 描述 / 联系方式 / 公告 / 维护模式（消费方接线仍在排期内）。

### 问题修复

- 渠道编辑弹窗在矮屏（手机、平板横屏、压缩后的桌面窗口）看不到「保存」按钮：
  表单与详情区高度原先写成固定 24/29rem，会被 `max-h-[calc(100dvh-1rem)]` 压缩的弹窗
  配合 `overflow-hidden` 把底部按钮行裁出可视区。改为统一的 `SHELL_HEIGHT` 高度阶梯
  `min(24rem,100dvh-5rem)` / `min(29rem,100dvh-5rem)`，常态尺寸不变，矮屏按可视区夹住。
- 发布管理「模型上架与定价」弹窗被 JSX 注释挤压布局、把注释文本渲染成可见内容；
  同时修复弹窗内列表横向滚动与底部按钮常驻。
- 版本号注入缺失导致「设置 > 信息」误报前端/后端版本不一致（浏览器缓存告警）：
  本地构建脚本改为同时向后端 `conf.Version` 与前端 `VITE_APP_VERSION` 注入同一版本号。

### 工程与发布

- 新增 `scripts/version.sh`（读取版本标记）、`scripts/build-local.sh`（前端 + 注入版本的后端一体化构建）、
  `scripts/publish-release.sh`（推送标签并携带本文件对应章节创建 GitHub Release）。
- 提交后自动实时推送到本仓库 `main` 分支，并在版本变更时发布 Release（`.git/hooks/post-commit`）。
- 前端单测（vitest）从 0 建起：定价 / 统计 / URL 拼装等纯逻辑 24 个用例；
  后端 e2e 回归 156/156 通过（含 9 条自动添加断言）。

## 上游基线 v0.13.9 及更早

v0.13.9 以前的历史与上游 [bestruirui/octopus](https://github.com/bestruirui/octopus) 一致，
更新说明见上游的提交记录（`git log --oneline v0.13.9`）。
