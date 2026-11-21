// LEDGER_KINDS 余额流水的类型全集, 与后端 internal/model/ledger.go:6-21 的 LedgerKind* 常量逐一对齐。
//
// 为什么单独收口: 钱包页与计费页各有一份流水列表, 两处都要按 type 筛选并渲染文案。
// 以前两处各写一份短名单(钱包 8 项、计费 4 项), 结果退款与提现四态既选不到,
// 又因为 locale 缺键在前端显示成 wallet.kind.refund 这种原始路径。
// 现在单一来源, 并由 lib/ledger-kinds.test.ts 断言 locale 里三语的 kind 键集合与它完全相等。
//
// Go 侧新增 kind 时必须同步: 这里加一项, 三份 locale 各加一条文案, 测试才会绿。
export const LEDGER_KINDS = [
    'reserve', // LedgerKindReserve: 请求开始, 冻结预估费用
    'settle', // LedgerKindSettle: 请求结束, 解冻预估并扣除实际费用
    'release', // LedgerKindRelease: 驳回无用量, 解冻预估并退还
    'adjust', // LedgerKindAdjust: 管理员直接设置余额
    'topup', // LedgerKindTopup: 充值入账
    'redemption', // LedgerKindRedeem: 兑换码入账
    'bonus', // LedgerKindBonus: 充值满赠, 不计入累计充值口径
    'refund', // LedgerKindRefund: 申诉退款返还余额并记录冲正
    'withdraw_freeze', // LedgerKindWithdrawFreeze: 提现申请, 冻结可提余额
    'withdraw_unfreeze', // LedgerKindWithdrawUnfreeze: 提现取消/驳回, 解冻
    'withdraw_paid', // LedgerKindWithdrawPaid: 已打款
    'withdraw_rollback', // LedgerKindWithdrawRollback: 打款回滚
] as const;

export type LedgerKind = (typeof LEDGER_KINDS)[number];

// ledgerKindOptions 筛选项: 空串表示"全部", 与后端 query 参数 kind='' 的语义一致。
export const ledgerKindOptions: readonly string[] = ['', ...LEDGER_KINDS];
// ledgerKindTone 给每种流水类型一个徽章配色。资金方向比类型本身更重要:
// 入账(充值/兑换/赠送/退款返还)用绿色系, 出账(结算扣费)用红色系, 冻结/占用用蓝色系,
// 提现三态与打款回滚用琥珀/紫色系区分"钱已经离开余额池"的不同阶段。
// 未列出的类型不额外上色(Badge 默认样式), 不报错 —— 新增 kind 时先能显示, 再补颜色。
const TONES: Record<string, string> = {
    settle: 'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400',
    reserve: 'border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-400',
    release: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    adjust: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    topup: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    redemption: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    bonus: 'border-teal-500/30 bg-teal-500/10 text-teal-600 dark:text-teal-400',
    refund: 'border-violet-500/30 bg-violet-500/10 text-violet-600 dark:text-violet-400',
    withdraw_freeze: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    withdraw_unfreeze: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
    withdraw_paid: 'border-fuchsia-500/30 bg-fuchsia-500/10 text-fuchsia-600 dark:text-fuchsia-400',
    withdraw_rollback: 'border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400',
};

export function ledgerKindTone(kind: string): string {
    return TONES[kind] ?? '';
}
