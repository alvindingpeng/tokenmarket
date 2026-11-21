// 按模型拆分的「覆盖边界」展示口径。
//
// 为什么这件事必须单独成文件并且被单测钉死: 按模型拆分现在读「明细 ∪ model 维度日汇总」,
// 它能活过明细留存期, 但活得比"总额"晚 —— 迁移 45 之前固化的日子只有 user 维度行, 没有 model 维度行。
// 那些天**总额有数、拆分补不回来**。后端的 model_breakdown_coverage_start 就是这条分界:
//   空串   = 区间内按模型完全没有数据(整段都拆不出来);
//   有日期 = 该日期之前"金额有、拆不到模型"。
// 两种空值必须分开表达: 前者是"没有", 后者是"有但看不见"。把它们说成同一句, 或者
// 干脆补 0 / 按比例摊到现存模型, 都是编数字 —— 用户会拿统计页的金额去对账, 编出来的数比缺数据更有害。
//
// 纯函数不放进 index.tsx: 组件文件只导出组件才能保住 Fast Refresh,
// 而这条判据正是最该被单测钉死的部分(与同目录 success.ts 的分工一致)。

// ModelCoverageCarrier 只声明本模块要读的那一个字段, 不 import UserStatsOverview:
// 字段是后端新加的, 老后端(或旧缓存)可能压根没下发, 而类型上它是必填 —— 用结构类型 + 运行时校验
// 才能同时覆盖"后端已升级"与"后端未升级"两种状态, 而且不需要 as any 去骗过编译器。
export type ModelCoverageCarrier = { model_breakdown_coverage_start?: unknown };

// ModelCoverage 三种状态, 对应界面上三种(含"什么都不做")的呈现:
//   none —— 区间内完全没有按模型数据;
//   from —— 从 start 那天起才有拆分, 之前只有总额;
//   full —— 不提示(要么整段都有拆分, 要么拿不到边界因而不该编一条提示)。
export type ModelCoverage =
    | { kind: 'none' }
    | { kind: 'from'; start: string }
    | { kind: 'full' };

// modelCoverageOf 判定按模型拆分的覆盖边界。
//
// 判据(与后端 internal/model/user_stats.go 的字段注释逐条对齐):
//   字段缺失(undefined / null / 非字符串)     -> full   —— 不猜、不硬报缺失。
//     老后端没有这个字段时, 界面凭空多出一条"覆盖不全"的提示就是在伪造事实;
//     宁可什么都不说, 也不要让用户以为数据一定缺了一截。
//   空串                                      -> none   —— 区间内按模型完全没有数据。
//   有日期且严格晚于查询起点                   -> from   —— 区间前段只有总额。
//   有日期但不晚于查询起点                     -> full   —— 整段都有拆分, 没必要多嘴。
//
// rangeStart 缺失时同样退回 full: 没有查询起点就无从判断"这个日期是不是早于区间",
// 猜一个起点会把提示变成噪声。
export function modelCoverageOf(
    overview: ModelCoverageCarrier | undefined | null,
    rangeStart: string | undefined,
): ModelCoverage {
    const raw = overview?.model_breakdown_coverage_start;
    // 运行时校验而非依赖类型: 后端换实现/中间层可能给 null 或数字, 那些都当"没下发"处理。
    if (typeof raw !== 'string') return { kind: 'full' };
    const start = raw.trim();
    if (start === '') return { kind: 'none' };
    if (!rangeStart) return { kind: 'full' };
    // YYYY-MM-DD 定长零填充, 字符串序即日期序, 不必再解析成 Date(与 success.ts 同一做法)。
    // 只有严格晚于起点才算缺口: 等于起点意味着第一天就有拆分, 整段完整。
    return start > rangeStart ? { kind: 'from', start } : { kind: 'full' };
}
