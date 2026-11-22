import type { UserStatsQuery } from '@/api/user-stats';

// 预设时间范围。custom 需要页面上真有日期输入控件才成立 ——
// 曾经因为没有日期控件把它移除过(选了只会退化成「不限时间」的假范围), 现在控件补齐了, 它回到列表里。
// 顺序即下拉顺序: 三档常用预设在前, 自定义垫底。
export const PRESETS = ['today', 'week', 'month', 'custom'] as const;
export type Preset = typeof PRESETS[number];

// formatDate 把日期格式化成 YYYY-MM-DD, 口径是浏览器本地年月日。
// 不能用 toISOString().slice(0, 10): toISOString 输出的是 UTC 时刻,
// UTC+8 的早上 8 点前「今天」会算成前一天, UTC-8 的傍晚 4 点后「今天」会算成明天 ——
// 统计范围就悄悄偏了一天。本地日期用本地字段取。
//
// 这里是**浏览器**的本地日期, 而后端按 `stats_timezone`(账号时区偏好, 默认跟随服务器)解释它。
// 两者可能不是同一个时区 —— 这正是概览响应要带上 `stats_timezone_offset_minutes` 与
// `stats_calendar_timezone_offset_minutes` 的原因: 偏移不同时页面会如实说明「区间边界按哪个时区、
// 日/周/月分组与留存按哪个时区」。不要在这里偷偷改成服务器时区: 用户输入的是他自己的日子,
// 换掉它等于替用户重新定义「今天」。
export function formatDate(date: Date): string {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
}

// getDateRange 按预设算出查询用的起止日期(闭区间, 都含当天, 本地口径)。
// custom 不在这里算: 它的起止由用户输入决定, 见 customDateRange/isValidDateRange。
// now 参数只为测试可复现; 生产调用不传, 取当前时间。
export function getDateRange(preset: Preset, now: Date = new Date()): Pick<UserStatsQuery, 'start_date' | 'end_date'> {
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const daysBack = preset === 'custom' ? 0 : preset === 'today' ? 0 : preset === 'week' ? 6 : 29;
    const start = new Date(today);
    start.setDate(start.getDate() - daysBack);
    return { start_date: formatDate(start), end_date: formatDate(today) };
}

// DATE_PATTERN 是后端 binding:"omitempty,datetime=2006-01-02" 的同一格式: 早退在本地, 别让服务端去挡。
// 只校验形状不够 —— 2026-02-31 形状合法但不存在, 所以还要 round-trip 回读一次确认。
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;

// isValidDate 判断一个字符串是不是真实存在的 YYYY-MM-DD。
// date input 正常只会给合法值(空串是用户清空), 但粘贴/程序赋值/老浏览器都可能塞进怪值,
// 而把非法日期拼进查询串会让后端静默回落成"最近 30 天" —— 用户以为自己看了某个区间, 其实不是。
export function isValidDate(value: string): boolean {
    if (!DATE_PATTERN.test(value)) return false;
    const [year, month, day] = value.split('-').map(Number);
    const parsed = new Date(year, month - 1, day);
    return parsed.getFullYear() === year && parsed.getMonth() === month - 1 && parsed.getDate() === day;
}

// isValidDateRange 判断起止都合法且起点不晚于终点(闭区间, 同一天合法)。
// 起止写反时不发查询、只把提示摆出来: 悄悄交换两端会把用户填的东西改掉, 他下次看到的值就不是自己输入的。
export function isValidDateRange(start: string, end: string): boolean {
    return isValidDate(start) && isValidDate(end) && start <= end;
}

// customDateRange 把用户的起止拼成查询区间。**调用方必须先用 isValidDateRange 挡住非法值** ——
// 这里不做兜底转换, 因为任何兜底都在替用户改口径。
export function customDateRange(start: string, end: string): Pick<UserStatsQuery, 'start_date' | 'end_date'> {
    return { start_date: start, end_date: end };
}

// rangeSpanDays 闭区间跨度的天数(同一天为 1)。
// 用 UTC 归一后再相减: 本地 Date 在夏令时切换日一天可能是 23 或 25 小时,
// 按毫秒除 86400000 会算出 0.96 天或 1.04 天, 再四舍五入就在那两天翻错。
export function rangeSpanDays(start: string, end: string): number {
    if (!isValidDate(start) || !isValidDate(end) || start > end) return 0;
    const [sy, sm, sd] = start.split('-').map(Number);
    const [ey, em, ed] = end.split('-').map(Number);
    const startMs = Date.UTC(sy, sm - 1, sd);
    const endMs = Date.UTC(ey, em - 1, ed);
    return Math.floor((endMs - startMs) / 86400000) + 1;
}

// effectiveGranularity 决定实际向服务端要的粒度。
// 规则与预设一致: 单日区间按小时有意义(看的是"刚才到现在"), 多日区间按小时会画出一根根
// 挤在一起的细柱, 于是小时自动降到按天 —— 但用户选的 时/日/周/月 不丢, 切回单日会恢复。
// custom 是唯一可能"多日但用户明确想看小时"的情形: 跨一天以上的小时桶数量 = 天数 × 24,
// 上限 30 天就是 720 个桶, 画不下也读不出, 所以照样降级。
export function effectiveGranularity(
    preset: Preset,
    granularity: 'hour' | 'day' | 'week' | 'month',
    spanDays: number,
): 'hour' | 'day' | 'week' | 'month' {
    if (preset === 'today') return 'hour';
    if (preset === 'custom' && spanDays <= 1) return granularity;
    return granularity === 'hour' ? 'day' : granularity;
}
