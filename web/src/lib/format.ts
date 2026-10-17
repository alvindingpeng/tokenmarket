// 时间与金额的统一格式化入口。
// 这些函数此前在每个页面各写一份(钱包/提现/兑换/审计/用户/分组/模型), 口径容易悄悄漂移:
// 有的判 Go 零值时间、有的不判, 小数位也从 2 位到 4 位不等。统一到这里, 改口径只改一处。

// formatDateTime 把后端 RFC3339 时间转成本地日期时间。
// 空值、非法值与 Go 零值时间(0001-01-01)一律回落到 fallback, 由调用方决定占位符。
export function formatDateTime(value: string | null | undefined, fallback = ''): string {
    if (!value) return fallback;
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return fallback;
    return date.toLocaleString();
}

// formatDateOnly 与 formatDateTime 同口径, 但只展示到日期。
export function formatDateOnly(value: string | null | undefined, fallback = ''): string {
    if (!value) return fallback;
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return fallback;
    return date.toLocaleDateString();
}

// formatAmount 余额/面额展示: 千分位分组, 最多保留 4 位小数。
// 后端是 6 位小数口径, 展示截到 4 位是刻意的可读精度取舍。
export function formatAmount(value: number | null | undefined): string {
    return Number(value || 0).toLocaleString(undefined, { maximumFractionDigits: 4 });
}

// formatUsd 美元金额展示: 固定两位小数并带货币符号。
export function formatUsd(value: number | null | undefined): string {
    return '$' + Number(value || 0).toFixed(2);
}

// formatLatency 把毫秒耗时缩写: 不足 1 秒用 ms, 以上用秒(两位小数)。
// 分组页成员徽章与模型评分卡片共用同一口径, 否则同一指标两处显示不同。
export function formatLatency(ms: number | null | undefined): string {
    const value = ms ?? 0;
    return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`;
}
