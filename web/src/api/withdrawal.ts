import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// WithdrawSummary 提现口径: 收益池可提余额与规则参数(全部后台可配)。
export type WithdrawSummary = {
    available: number;
    frozen: number;
    withdrawn: number;
    revenue: number;
    rate: number;
    fee_rate: number;
    fee_min: number;
    min_amount: number;
    can_apply: boolean;
};

// Withdrawal 提现单; amount/fee/actual 为收益池口径, actual_cents 为打款指令(人民币分)。
export type Withdrawal = {
    id: number;
    user_id: number;
    username: string; // 申请人用户名(列表联查, 管理端按人看)
    amount: number;
    fee: number;
    actual: number;
    rate: number;
    actual_cents: number;
    method: string;
    account_name: string;
    account_no: string;
    status: 'pending' | 'approved' | 'paid' | 'rejected' | 'cancelled';
    review_note: string;
    reviewed_by: number;
    reviewed_at: string | null;
    paid_ref: string;
    paid_at: string | null;
    applied_at: string;
};

// useWithdrawSummary 提现口径(可提/冻结/已提 + 规则)。
export function useWithdrawSummary() {
    return useQuery({
        queryKey: ['withdraw', 'summary'],
        queryFn: () => apiRequest<WithdrawSummary>('/api/v1/withdrawal/summary'),
    });
}

// useWithdrawals 提现记录; status 为空不过滤, userId 非零供管理员按用户筛。
export function useWithdrawals(status: string, userId: number) {
    return useQuery({
        queryKey: ['withdraw', 'list', status, userId],
        queryFn: () => {
            const params = new URLSearchParams({ limit: '50' });
            if (status) params.set('status', status);
            if (userId) params.set('user_id', String(userId));
            return apiRequest<{ items: Withdrawal[] }>(`/api/v1/withdrawal/list?${params}`);
        },
    });
}

// applyWithdrawal 提现申请: 收益池条件冻结后建单。
export function applyWithdrawal(input: {
    amount: number;
    method: string;
    account_name: string;
    account_no: string;
}) {
    return apiRequest<{ order: Withdrawal }>('/api/v1/withdrawal/apply', { method: 'POST', body: input });
}

// cancelWithdrawal 取消申请(仅本人 pending)。
export function cancelWithdrawal(id: number) {
    return apiRequest<{ id: number }>('/api/v1/withdrawal/cancel', { method: 'POST', body: { id } });
}

// reviewWithdrawal 管理员审核: 通过进 approved, 拒绝解冻。
export function reviewWithdrawal(id: number, approve: boolean, note: string) {
    return apiRequest<{ id: number }>('/api/v1/withdrawal/review', {
        method: 'POST',
        body: { id, approve, note },
    });
}

// markWithdrawalPaid 管理员打款: approved → paid, 凭证必填。
export function markWithdrawalPaid(id: number, paidRef: string) {
    return apiRequest<{ id: number }>('/api/v1/withdrawal/mark-paid', {
        method: 'POST',
        body: { id, paid_ref: paidRef },
    });
}

// rollbackWithdrawal 打款回滚: paid → approved。
export function rollbackWithdrawal(id: number) {
    return apiRequest<{ id: number }>('/api/v1/withdrawal/rollback', { method: 'POST', body: { id } });
}
