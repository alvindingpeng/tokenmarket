// 资金表留存卡片里「四张表现有行数」的展示口径。
//
// 为什么单拎出来: 这里有一个必须钉死的区别 ——
//   · 0 是一个**具体事实**: 这张表确实是空的。管理员据此不必点「立即清理」。
//   · undefined 是**这次没取到**: 请求失败、或后端这个版本还没有 counts 字段。
//     此时显示 0 会让管理员以为表已经空了, 于是跳过清理 —— 而超期的资金明细还在库里。
// 两者在界面上的差别只有「0」和「—」, 在决策上的差别是「不用清理」和「未知, 先别动」。

// formatTableCount 行数千分位; 大数字不带分隔符基本读不出来。
export function formatTableCount(value: number | undefined): string {
    if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
    return value.toLocaleString();
}
