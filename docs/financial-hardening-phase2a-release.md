# 2A 上线准备与实际发布记录

用户在 2A 开发验证完成后明确授权继续开发与部署，并提供 root SSH 授权凭据。下文“发布前检查”为历史；“实际发布”章节为已上线证据。密码未写入仓库文件或部署脚本。

## 发布前检查（历史）

- 本会话通过 root@127.0.0.1 的 BatchMode SSH 认证失败（publickey,password）；尚未停服或替换生产二进制。需要提供可用的授权 SSH 密钥/运维入口，不从旧脚本提取密码。
- 本机与 https://tt.xjj.se/healthz 返回 status=ok、db=ok、active_requests=0，版本 v0.39.8；线上二进制 SHA-256 仍为 b96545e914d7cafc04c793e1f09a8a8e8b5a7c789565dab9c28fca7e4ac9c2a6（第一阶段）。公开 status=200，补偿端点匿名请求=404，尚未安装 2A。
- 使用 SQLite backup API 以只读连接生成权限受限的生产库一致性快照，integrity_check=ok。快照位于本地忽略构建目录 build/phase2a-release，不应提交或公开。
- 快照 SHA-256：b6038c4db484138e184b7c0628c41f7b4a41dd7fc1f47f5511c51f1512d533f8；目录 0700，数据库和审计报告 0600。快照审计：负冻结、重复计费、重复终态流水、三方拆分差异、冻结额差异五项均为 0。未执行任何历史修正。
- 本轮 scripts/check.sh 全部已执行检查通过；JWT 登录布局仍跳过。新增发布安全保护 4 项专项测试通过。

## 新增发布保护

- 发布前必须在外部入口暂停新请求并排空在途请求；脚本要求 --traffic-paused 明确确认，且检查 active_requests=0。此参数不是自动流量开关。
- 停服备份后，在隔离恢复副本执行候选二进制 verify-migration；该命令只做数据库迁移与结构检查，不启动 HTTP、缓存或后台任务。
- 旧二进制不理解补偿任务。安装后若健康或财务检查失败，脚本停止服务、保留候选与现场，不自动恢复旧二进制，不覆盖生产数据库。需运维选择兼容修复或核验新交易后的恢复方案。安装前失败可恢复原服务。
- 手动恢复数据库前必须核对发布后新交易，不能盲目覆盖当前库。

## 发布步骤（已执行）

1. 恢复授权运维入口；确认实际 service、数据库路径、域名和当前二进制摘要。
2. 构建带前端且前后端版本一致的候选，记录 SHA-256 和未提交工作区标识。
3. 完成生产快照隔离迁移演练并复核资金表未被修改。
4. 暂停流量、排空、停服整库备份并校验、候选安装、健康/权限/结构/财务核验，最后恢复流量。
5. 保存实际备份路径、二进制摘要、安装时间、前后审计与线上检查结果。本记录不得用准备工作替代上线证据。

## 实际发布（2026-10-20）

- 授权 root SSH 认证成功，核验 switchyard.service 的 ExecStart 与 WorkingDirectory 后执行；未操作 Harness 服务。
- 候选构建：2026-10-20 17:14:49 +0800；v0.39.8，Git 标识 e8aaf59（未提交工作区，不能以该提交号代替产物标识）。
- 已安装 SHA-256：a58cb865675fe091f769f587ee057df3608c8502e06af71211acca0bb310b4f2。
- 生产服务启动：2026-10-20 17:20:27 +0800，PID=621357，active。
- 受限整库备份目录：/opt/switchyard/data/releases/20261020-172016-financial；保留原二进制、配置、unit、数据库、校验文件、前后审计及 release.json。
- 数据库备份 SHA-256：8da5afd24460c7a534791513a4fb582095acc172585a41665cb37e869a7b0dc3；SQLite backup API、完整性/外键检查、恢复副本及 sha256sum -c 均通过。
- 本地真实快照迁移演练通过；逐行校验 users、billing_records、balance_ledgers、balance_reservations、settlement_statements、billing_reconciles 的原有列数据均未改变。停服后的新备份再次由候选 verify-migration 在 /var/tmp 私有副本演练成功。
- 仅 tt.xjj.se 临时维护，排空后执行发布。首次维护探测因 nginx reload 传播未确认而在换包前安全退出并恢复原配置；增加等待探测后再次发布成功。最终 nginx 配置与备份逐字相同，nginx -t 通过，入口已恢复。
- 发布前后负冻结、重复计费、重复终态、计费拆分差异、冻结额差异五项均为 0。
- 本机/公网 healthz 正常；首页和 /status 为 200；补偿 list GET 与 run POST 匿名均为 401。GET /run 为 404，符合仅允许 POST 的定义。
- 线上 lease_expires_at 列和补偿表存在；检查时已有 3 条 completed 任务，无 pending/retry/manual。仅证明新路径实际产生完成任务，不代表在生产注入失败验证。
- 服务上线后 error 级 journal 无条目；journal 提示一个旧 user 日志文件截断，与本次服务错误不同。
- 未执行历史余额修正、补扣、结算单重算或真实资金测试。JWT 登录浏览器布局验收仍未执行；数据库真实服务集成仅 SQLite。

## 后续关注

观察 billing_recovery pending/retry/manual 及租约失败告警；人工项先核对原快照与流水，不强制扣款。已产生新任务后不可盲目回退第一阶段旧二进制或恢复旧库。建议更换本次在聊天中提供的 root 密码并改用专用 SSH 密钥。
