import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { POLL_MINUTE } from '@/lib/polling';

// 汇率快照(账面币种 USD 为基准): 改价工作台、现金流报价与账单复核共用同一份数据。
export type FxRateItem = {
    from: string;
    to: string;
    rate: number;
    day: string;
    source: string;
};

// UI 统一显示的法币选项; 与后端 SupportedQuoteCurrencies + 基准 USD 同步维护。
// 现有六种(CNY/USD/EUR/GBP/JPY/HKD/SGD)之外, 补齐后端已支持的扩展币种(AUD/CAD/KRW/CHF/THB),
// 充值/提现统一用这份清单渲染币种选择, 白名单与后端 model.ValidPriceCurrency 对齐。
export const CASHFLOW_CURRENCIES = ['CNY', 'USD', 'EUR', 'GBP', 'JPY', 'HKD', 'SGD', 'AUD', 'CAD', 'KRW', 'CHF', 'THB'] as const;
export type CashflowCurrency = typeof CASHFLOW_CURRENCIES[number];

// formatFxSnapshotDay 将 YYYYMMDD / YYYY-MM-DD 规范化为可读 ISO 日期, 未提供则返回空。
export function formatFxSnapshotDay(day?: string): string {
    if (!day) return '';
    const compact = day.replaceAll('-', '');
    return /^\d{8}$/.test(compact) ? compact.slice(0, 4) + '-' + compact.slice(4, 6) + '-' + compact.slice(6) : day;
}

// cashflowFxRate 取 USD 与目标币种之间的快照汇率。FX API 方向固定 USD→quote。
export function cashflowFxRate(rates: FxRateItem[] | undefined, currency: string, fallback?: number): FxRateItem | undefined {
    const code = currency.trim().toUpperCase();
    if (code === 'USD') return { from: 'USD', to: 'USD', rate: 1, day: '', source: 'ledger' };
    return (rates ?? []).find((item) => item.from.toUpperCase() === 'USD' && item.to.toUpperCase() === code)
        ?? (code === 'CNY' && fallback && fallback > 0 ? { from: 'USD', to: 'CNY', rate: fallback, day: '', source: 'configured' } : undefined);
}

const fxPath = '/api/v1/fx/rates';
const adminFxHistoryPath = '/api/v1/admin/fx/rates/history';

// AdminFxRateHistoryQuery 历史查询参数: 日期缺省为后端默认最近 30 天, 币种缺省为全部报价币种。
export type AdminFxRateHistoryQuery = {
    startDate?: string;
    endDate?: string;
    currency?: string;
};

// AdminFxRateHistory 是历史快照查询响应: items 按 day 升序, 同一天内按币种排序。
// from/to 与 fx_rates.day 同口径(YYYYMMDD), 直接展示, 不再另做格式转换。
export type AdminFxRateHistory = {
    currency: string;
    from: string;
    to: string;
    limit: number;
    items: FxRateItem[];
};

// adminFxRateHistoryQueryKey 历史查询键: 日期与币种必须进键, 否则换条件不会重新取数。
export function adminFxRateHistoryQueryKey(query: AdminFxRateHistoryQuery) {
    return ['admin-fx-rates', 'history', query.startDate ?? '', query.endDate ?? '', query.currency ?? ''] as const;
}

// useAdminFxRateHistory 拉取 fx_rates 历史快照(仅管理员), 只读不触发采集。
// 后端参数口径是 from/to=YYYYMMDD(与 fx_rates.day 一致), 前端类型保持 YYYY-MM-DD 便于日期控件, 这里负责转换。
export function useAdminFxRateHistory(query: AdminFxRateHistoryQuery) {
    return useQuery({
        queryKey: adminFxRateHistoryQueryKey(query),
        queryFn: ({ signal }: { signal: AbortSignal }) => {
            const params = new URLSearchParams();
            const compact = (d: string) => d.replaceAll('-', '');
            if (query.startDate) params.set('from', compact(query.startDate));
            if (query.endDate) params.set('to', compact(query.endDate));
            if (query.currency) params.set('currency', query.currency);
            const qs = params.toString();
            return apiRequest<AdminFxRateHistory>(adminFxHistoryPath + (qs ? `?${qs}` : ''), { signal });
        },
        retry: apiQueryRetry,
    });
}

// fxHistoryChangePct 计算相邻两天同币种汇率变化百分比; 前一天缺失或为 0 返回 null 不显示。
export function fxHistoryChangePct(items: FxRateItem[]): (number | null)[] {
    const prevByQuote = new Map<string, number>();
    return items.map((item) => {
        const prev = prevByQuote.get(item.to);
        prevByQuote.set(item.to, item.rate);
        if (prev === undefined || prev <= 0) return null;
        return (item.rate / prev - 1) * 100;
    });
}
const adminFxStatusPath = '/api/v1/admin/fx/rates/status';
const adminFxRefreshPath = '/api/v1/admin/fx/rates/refresh';

export type FxRefreshStatus = {
    LastAttemptAt: string;
    LastSuccessAt: string;
    LastFailureAt: string;
    LastError: string;
    FailureCount: number;
    Source: string;
};

export type AdminFxRateStatus = {
    day: string;
    items: FxRateItem[];
    age_days: number;
    stale: boolean;
    max_age_days: number;
    source: string;
    supported_quotes: string[];
    refresh: FxRefreshStatus;
};

export type AdminFxRateRefreshResult = {
    written: number;
    age_days: number;
    stale: boolean;
    max_age_days: number;
    source: string;
};

export function useAdminFxRateStatus() {
    return useQuery({
        queryKey: ['admin-fx-rates', 'status'] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) => apiRequest<AdminFxRateStatus>(adminFxStatusPath, { signal }),
        retry: apiQueryRetry,
        refetchInterval: POLL_MINUTE,
    });
}

export function useRefreshAdminFxRates() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<AdminFxRateRefreshResult>(adminFxRefreshPath, { method: 'POST' }),
        onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['admin-fx-rates', 'status'] }),
    });
}

// FxReconciliationBucket 折算质量分桶: 正常/陈旧/缺失三类的笔数与金额(账面币种 USD)。
// 后端字段以 Lead 消息为准, 全部可选兜底; 金额缺省按 0 计, 占比由前端按总额归一。
export type FxReconciliationBucket = {
    count?: number;
    amount?: number;
};

// AdminFxReconciliation 对账报表响应: 时间窗 + 三个分桶 + 汇总。
// 兼容两类字段命名: 嵌套 ok/stale/missing 桶, 或扁平 ok_count/ok_amount 等标量。
export type AdminFxReconciliation = {
    from: string;
    to: string;
    ok?: FxReconciliationBucket;
    stale?: FxReconciliationBucket;
    missing?: FxReconciliationBucket;
    usd_native?: FxReconciliationBucket; // 记账前历史账单/未走折算, 财务重点下钻对象。
    usd_native_count?: number;
    usd_native_amount?: number;
    ok_count?: number;
    ok_amount?: number;
    stale_count?: number;
    stale_amount?: number;
    missing_count?: number;
    missing_amount?: number;
    total_count?: number;
    total_amount?: number;
};

// fxReconciliationBucketOf 归一取桶: 嵌套字段优先, 扁平字段兜底。
export function fxReconciliationBucketOf(
    report: AdminFxReconciliation | undefined,
    kind: 'ok' | 'stale' | 'missing' | 'usd_native'
): { count: number; amount: number } {
    if (!report) return { count: 0, amount: 0 };
    const nested = report[kind];
    const flat = report as unknown as Record<string, number | undefined>;
    const count = nested?.count ?? flat[kind + '_count'] ?? 0;
    const amount = nested?.amount ?? flat[kind + '_amount'] ?? 0;
    return { count: Number(count) || 0, amount: Number(amount) || 0 };
}

// fxReconciliationQueryKey 查询键: 日期窗必须进键, 换窗口才重新取数。
export function fxReconciliationQueryKey(from: string, to: string) {
    return ['admin-fx-reconciliation', from, to] as const;
}

// fxRangeToCompactDate 把 YYYY-MM-DD 转 YYYYMMDD; 已是紧凑形式原样返回, 空值回空串。
export function fxRangeToCompactDate(day: string): string {
    return day ? day.replaceAll('-', '') : '';
}

const adminFxReconciliationPath = '/api/v1/admin/fx/reconciliation';

// useAdminFxReconciliation 拉取折算质量对账报表(仅管理员)。
// from/to 前端用 YYYY-MM-DD 便于日期控件, 请求前转成后端要求的 YYYYMMDD。
// 空窗不发请求: 报表总是显式选窗口, 不给一个含义模糊的默认区间。
export function useAdminFxReconciliation(from: string, to: string) {
    return useQuery({
        queryKey: fxReconciliationQueryKey(from, to),
        enabled: from !== '' && to !== '',
        queryFn: ({ signal }: { signal: AbortSignal }) => {
            const params = new URLSearchParams({ from: fxRangeToCompactDate(from), to: fxRangeToCompactDate(to) });
            return apiRequest<AdminFxReconciliation>(adminFxReconciliationPath + '?' + params, { signal });
        },
        retry: apiQueryRetry,
    });
}

export function useFxRates() {
    return useQuery({
        queryKey: ['fx-rates'] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<{ day: string; items: FxRateItem[] }>(fxPath, { signal }),
        retry: apiQueryRetry,
        // 牌价一天一更; 前端分钟级低频跟一次即可。
        refetchInterval: POLL_MINUTE,
    });
}
