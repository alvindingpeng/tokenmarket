// 现金流币种的展示口径工具: 零小数币种(如 JPY/KRW)没有"分"概念,
// 直接按整数元计价与展示; 其余币种沿用两位小数(分)口径。
// 来源: ISO 4217 zero-decimal currencies 清单中与 CASHFLOW_CURRENCIES 相交的部分。

// ZERO_DECIMAL_CURRENCIES 零小数币种白名单(大写)。JPY/KRW 是当前现金流会出现的两种;
// 后续后端扩展币种(如 VND/CLP)时同步补充。
import { formatPlainMoney } from '@/lib/money';

export const ZERO_DECIMAL_CURRENCIES = new Set(['JPY', 'KRW']);

// isZeroDecimalCurrency 判定是否零小数币种: 大小写不敏感, 空值按非零小数处理。
export function isZeroDecimalCurrency(code: string | undefined): boolean {
    const c = (code ?? '').trim().toUpperCase();
    return ZERO_DECIMAL_CURRENCIES.has(c);
}

// formatCashflowAmount 按币种精度格式化金额:
// 零小数币种 toFixed(0) + 千分位; 其余照旧 formatPlainMoney 口径(最多 6 位小数去尾零)。
// 千分位用 Intl.NumberFormat(en-US 逗号分组), 展示层可读性优先。
export function formatCashflowAmount(value: number, currency: string | undefined): string {
    if (isZeroDecimalCurrency(currency)) {
        return new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 }).format(Math.round(value));
    }
    return formatPlainMoney(value);
}
