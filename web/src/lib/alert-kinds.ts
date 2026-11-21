// ALERT_KINDS 站内告警 kind 的闭集, 与后端 internal/op/alert_silence.go:26-42 的
// AlertValidKind 白名单逐一对齐(该闭集同时是"管理员能为哪类告警建静默窗口"的判据)。
//
// 为什么单独收口: ChannelAlert.kind 原先是一串手抄的联合类型, 与后端长期不同步 ——
// fx_rate_refresh_failed 已经在发却不在类型里, withdrawal_approve_timeout /
// payment_after_expiry 同理。因为 kindBadge 的入参是 string, 编译器一声不吭,
// 于是"前端需要穷举 kind 的地方(switch / Record)静默漏分支"这类问题永远发现不了。
//
// Go 侧新增 kind 时必须同步: 闭集加一项 → 这里加一项 → lib/alert-kinds.test.ts 才会绿。
export const ALERT_KINDS = [
    'down', // 渠道连续失败
    'recovered', // 渠道恢复
    'degraded', // 渠道延迟降级
    'key_disabled', // 渠道凭据熔断
    'model_disabled', // 渠道模型熔断
    'low_balance', // 余额不足
    'quota_exceeded', // 配额超限
    'mismatch', // 计费对账不平
    'billing_failed', // 计费结算失败
    'backup_failed', // 自动备份失败
    'archive_failed', // 日志归档失败
    'watchdog_alert', // 外部看门狗报障
    'route_persist_failed', // 路由热状态刷盘失败
    'route_persist_recovered', // 上述刷盘恢复
    'task_failed', // 后台任务连续失败
    'task_recovered', // 后台任务恢复
    'key_degraded', // 凭据级延迟降级(比 key_disabled 轻一档)
    'model_degraded', // 模型级延迟降级
    'payment_amount_mismatch', // 支付金额与订单不符
    'reserve', // 预扣余额相关告警
    'input_tokens', // 输入 token 配额触顶
    'output_tokens', // 输出 token 配额触顶
    'requests', // 请求数配额触顶
    'update_available', // 发现新版本
    'test', // 告警通道连通性测试
    'withdrawal_approve_timeout', // 已审批提现超时被自动撤销
    'payment_after_expiry', // 过期充值单仍收到成功回调并补入账
    'fx_rate_refresh_failed', // 牌价源连续采集失败
    // 第二批补登记(原先只在 ALERT_KINDS_UNREGISTERED 里, 后端补齐后挪上来)。
    'usage_missing', // 成功请求却没有用量回传(疑似免费调用)
    'account_invalidated', // 上游账号令牌失效, 需重新导入
    'billing_archive_failed', // 资金表归档失败
    'audit_archive_failed', // 审计表归档失败
    'fx_rate_stale', // 牌价采集失败且快照已陈旧, 折算按旧价进行
] as const;

export type AlertKind = (typeof ALERT_KINDS)[number];

// ALERT_KINDS_UNREGISTERED 已经在发但**没进闭集**的 kind。
//
// 这批是"发得出但登记不全"的另一半: 它们会出现在告警列表里(所以类型必须收),
// 但管理员为它们建静默窗口会被后端拒(400), 外发事件名也掉成 system_alert。
// 与 fx_rate_refresh_failed 修掉前的形状一模一样, 列在这里是为了让差距可见而不是消失 ——
// 后端修好一个就把它从这份清单挪进上面的 ALERT_KINDS。
// 来源: grep -rnE 'Kind: *"[a-z_0-9]+"' --include=*.go internal cmd 的发出点清单,
// 逐个与 alert_silence.go 的闭集求差集。
// 当前为空: 上一批 5 个(usage_missing / account_invalidated / billing_archive_failed /
// audit_archive_failed / fx_rate_stale)已由后端补进闭集, 前端随之挪到 ALERT_KINDS。
// 保留这个空清单而不是直接删掉它, 是因为它是一道**哨兵**: alert-kinds.test.ts 断言
// "两份清单不相交"且"未登记清单 == 后端现状", 下次再出现"发得出但登记不全"的 kind 时,
// 这里就是唯一该改的地方, 而不是让差距悄悄消失。
export const ALERT_KINDS_UNREGISTERED = [] as const;

export type UnregisteredAlertKind = (typeof ALERT_KINDS_UNREGISTERED)[number];

// AnyAlertKind 告警列表里可能出现的全部 kind = 已登记闭集 + 已发出但未登记的。
// ChannelAlert.kind 用这个类型: 类型要对齐"实际能收到什么", 而不是只对齐"理想清单"。
export type AnyAlertKind = AlertKind | UnregisteredAlertKind;

// isRegisteredAlertKind 判断 kind 是否在后端闭集内(= 能为它建静默窗口)。
// 命名带 Registered 而不是简单的 isValid: 这里要区分"能收到"与"能登记", 两者不同。
export function isRegisteredAlertKind(kind: string): boolean {
    return (ALERT_KINDS as readonly string[]).includes(kind);
}

// isAlertKind 判断 kind 是否是告警列表里可能出现的值(闭集 ∪ 已发出未登记)。
export function isAlertKind(kind: string): boolean {
    return isRegisteredAlertKind(kind) || (ALERT_KINDS_UNREGISTERED as readonly string[]).includes(kind);
}
