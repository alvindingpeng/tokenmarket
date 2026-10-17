import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// WalletSummary 钱包摘要: 余额口径(可用/冻结)与累计口径(消费/充值/收益)一次取回。
export type WalletSummary = {
    balance: number;
    frozen: number;
    total_spent: number;
    total_revenue: number;
    total_recharged: number;
};

// LedgerKind 余额流水类型: 预扣/结算/释放/调账/充值/赠送/兑换。
export type LedgerKind = 'reserve' | 'settle' | 'release' | 'adjust' | 'topup' | 'bonus' | 'redemption';

// LedgerRow 余额流水行; amount 为可用余额净变动, frozen 为冻结额变动, cost 为本次实际费用。
// 钱包页与计费页读的是同一个后端端点, 这里只有这一份类型定义(此前 billing.ts 另有一份
// BalanceLedger, 还把 request_id 写成了 string, 与实际返回的数字不符)。
export type LedgerRow = {
    id: number;
    user_id: number;
    kind: LedgerKind | string;
    amount: number;
    frozen: number;
    cost: number;
    balance_after: number;
    request_id: number;
    model_name: string;
    actor_id: number;
    actor_name: string;
    note: string;
    created_at: string;
};

// LedgerQuery 余额流水查询参数; kind 为空取全部, userId 为 0 表示按当前身份(普通用户恒为自己)。
export type LedgerQuery = {
    kind?: string;
    limit?: number;
    offset?: number;
    userId?: number;
};

// balanceLedgerQueryOptions 余额流水的查询配置, 钱包页与计费页共用。
// 键统一挂在 ['wallet'] 下: 兑换/调账后失效 ['wallet'] 即可同时刷新摘要与流水。
export function balanceLedgerQueryOptions(query: LedgerQuery = {}) {
    const limit = query.limit ?? 20;
    const offset = query.offset ?? 0;
    const userId = query.userId ?? 0;
    const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (query.kind) params.set('kind', query.kind);
    if (userId) params.set('user_id', String(userId));
    return queryOptions({
        queryKey: ['wallet', 'ledger', query.kind ?? '', limit, offset, userId],
        queryFn: () => apiRequest<{ items: LedgerRow[]; total: number }>('/api/v1/stats/balance/ledger?' + params.toString()),
        placeholderData: (previous) => previous,
    });
}

// useWalletSummary 钱包摘要; 兑换/调账后按 ['wallet'] 失效即可刷新。
export function useWalletSummary() {
    return useQuery({
        queryKey: ['wallet', 'summary'],
        queryFn: () => apiRequest<WalletSummary>('/api/v1/wallet/summary'),
    });
}

// useWalletLedger 钱包页流水: 一次取 50 条, kind 为空取全部。管理员看自己也要显式传 user_id。
export function useWalletLedger(kind: string, userId: number) {
    return useQuery(balanceLedgerQueryOptions({ kind, userId, limit: 50 }));
}
