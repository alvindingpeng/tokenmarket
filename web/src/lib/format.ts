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

// Token 档位: 实际 token / m(1e6) / b(1e9) / mb(1e12)。
// 千位级不再缩 K —— "12,340 tokens" 比 "12.3K" 直读, K 档只会让相邻卡片数字口径不一致;
// 百万以上才进 m 档, 与运维/统计侧"量级一眼可辨"的诉求一致。
export function formatTokenCount(value: number | null | undefined): string {
    const count = Number.isFinite(value) ? Math.max(0, value ?? 0) : 0;
    const units = ['', 'm', 'b', 'mb'] as const;
    const thresholds = [1, 1e6, 1e9, 1e12] as const;
    let index = 0;
    while (index < units.length - 1 && count >= thresholds[index + 1]) {
        index++;
    }
    if (index === 0) {
        return Math.round(count).toLocaleString();
    }
    const scaled = count / thresholds[index];
    // Promote rounded boundary values rather than displaying 1000.0m.
    if (Number(scaled.toFixed(1)) >= 1000 && index < units.length - 1) {
        return (scaled / 1000).toFixed(1) + units[index + 1];
    }
    return scaled.toFixed(1) + units[index];
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
