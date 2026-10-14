import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// WalletSummary 钱包摘要: 余额口径(可用/冻结)与累计口径(消费/充值/收益)一次取回。
export type WalletSummary = {
    balance: number;
    frozen: number;
    total_spent: number;
    total_revenue: number;
    total_recharged: number;
};

// LedgerRow 余额流水行; kind 区分预扣/结算/释放/调账/充值/兑换。
export type LedgerRow = {
    id: number;
    user_id: number;
    kind: string;
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

// useWalletSummary 钱包摘要; 兑换/调账后按 ['wallet'] 失效即可刷新。
export function useWalletSummary() {
    return useQuery({
        queryKey: ['wallet', 'summary'],
        queryFn: () => apiRequest<WalletSummary>('/api/v1/wallet/summary'),
    });
}

// useWalletLedger 余额流水; kind 为空取全部。管理员看自己也要显式传 user_id。
export function useWalletLedger(kind: string, userId: number) {
    return useQuery({
        queryKey: ['wallet', 'ledger', kind, userId],
        queryFn: () => {
            const params = new URLSearchParams({ limit: '50' });
            if (kind) params.set('kind', kind);
            if (userId) params.set('user_id', String(userId));
            return apiRequest<{ items: LedgerRow[]; total: number }>(`/api/v1/stats/balance/ledger?${params}`);
        },
    });
}
