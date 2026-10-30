import type { UserStatsQuery } from '@/api/user-stats';

// 预设时间范围。custom 因为页面上没有日期输入控件, 选了只会退化成「不限时间」的假范围 ——
// 要么给它配日期选择器, 要么先移除; 这里选择移除, 有真实输入再加回来。
export const PRESETS = ['today', 'week', 'month'] as const;
export type Preset = typeof PRESETS[number];

// formatDate 把日期格式化成 YYYY-MM-DD, 口径是浏览器本地年月日。
// 不能用 toISOString().slice(0, 10): toISOString 输出的是 UTC 时刻,
// UTC+8 的早上 8 点前「今天」会算成前一天, UTC-8 的傍晚 4 点后「今天」会算成明天 ——
// 统计范围就悄悄偏了一天。本地日期用本地字段取。
export function formatDate(date: Date): string {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
}

// getDateRange 按预设算出查询用的起止日期(闭区间, 都含当天, 本地口径)。
// now 参数只为测试可复现; 生产调用不传, 取当前时间。
export function getDateRange(preset: Preset, now: Date = new Date()): Pick<UserStatsQuery, 'start_date' | 'end_date'> {
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const daysBack = preset === 'today' ? 0 : preset === 'week' ? 6 : 29;
    const start = new Date(today);
    start.setDate(start.getDate() - daysBack);
    return { start_date: formatDate(start), end_date: formatDate(today) };
}
