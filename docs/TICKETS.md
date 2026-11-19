# 工单系统（P0-4）

## 本期范围

- 用户创建、分页筛选、查看自己的工单、文字回复、关闭和重新打开。
- 管理员查看全部工单、回复、分配有效管理员和按合法状态机处理。
- 新建与用户回复通知管理员，管理员回复及状态变化通知用户。
- 使用现有 Cookie 登录态与 RBAC，用户 ID、回复身份均由服务端获取。
- 前端通过 Zustand 页面导航进入 `tickets`，不添加 React Router。
- 标题最多 255 字符，正文/回复最多 10000 字符，拒绝纯空白文本。

## 状态和通知

工单使用 `open`、`in_progress`、`resolved`、`closed` 四态：

- `open → in_progress | closed`
- `in_progress → open | resolved | closed`
- `resolved → in_progress | closed`
- `closed → open`

同状态更新为空操作，不重复发通知；非法跳转返回 409。关闭后禁止继续回复，需先重新打开；重新打开清除关闭时间。管理员操作的候选状态必须由合法流转表限制，服务端仍需校验并发更新冲突。自动关闭以 `resolved_at`（进入 resolved 的时刻）为计时起点，满 72 小时由后台任务关闭；期间重新打开或退回 in_progress 会清空该时间戳，因此用户补一句话不会重置关闭窗口。

通知在业务提交后异步发送，有独立超时，不阻断回复或状态更新。工单通知不使用“同用户同类型一小时去重”，避免不同工单、不同回复互相吞消息。

## 接口约定

用户前缀 `/api/v1/ticket`；管理员前缀 `/api/v1/admin/ticket`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/ticket` | 创建工单 |
| GET | `/ticket/list` | 本人工单分页 |
| GET | `/ticket/:id` | 本人工单详情及对话 |
| POST | `/ticket/:id/reply` | 回复 `{content}` |
| POST | `/ticket/:id/close` | 关闭 |
| POST | `/ticket/:id/reopen` | 重新打开 |
| GET | `/admin/ticket/list` | 管理员分页筛选 |
| GET | `/admin/ticket/:id` | 管理员详情 |
| POST | `/admin/ticket/:id/reply` | 管理员回复 |
| POST | `/admin/ticket/:id/assign` | 分配 `{assigned_to}` |
| POST | `/admin/ticket/:id/status` | 更新 `{status}` |

创建与详情返回 `{ticket: ...}`，回复返回 `{message: ...}`（均在标准 `data` 外壳内）；列表返回 `{items,total,page,page_size}`。详情 `messages` 仅包含后续回复，初始正文在 `ticket.content`，前端单独显示；`message_count` 只计回复。筛选参数 `status`、`priority`、`category`、`assigned_to`、`page`、`page_size`。

## 验证与发布

```sh
# 按机器 PATH 选择 Go；也可通过 GO_BIN 指定构建工具链。
go test ./...
(cd web && pnpm test)
bash scripts/build-local.sh
```

部署脚本启用 `set -euo pipefail`，前端/后端构建管道失败即停止，不再把 `tail` 的成功状态误当作构建成功。`scripts/build-local.sh` 使用 `${GO_BIN:-go}`，避免将开发机绝对路径带进生产。

回归重点：用户身份隔离、管理员角色校验、无效分配、关闭后回复限制、重新打开时间戳、通知收件人、分页及输入长度。通知 handler 必须通过 `middleware.CurrentUser` 读取 int 类型的登录身份，不使用 `c.GetUint`。

## 安全和交付边界

工单正文按纯文本呈现，不解释 HTML。附件上传未在本期启用：需要后续独立实现存储、鉴权下载、类型限制和病毒扫描，不能用任意外链替代安全上传。全局 WebHook 仍属于 P0-5，不属于此次发布。

生产验收需确认提交号、健康检查、鉴权端点及数据库迁移成功；仅健康检查通过不等于已完成浏览器交互验收。
