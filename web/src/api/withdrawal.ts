import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// WithdrawSummary 提现口径: 收益池可提余额与规则参数(全部后台可配)。
export type WithdrawSummary = {
    available: number;
    frozen: number;
    withdrawn: number;
    revenue: number;
    rate: number;
    currency?: string;
    quote_currency?: string;
    rate_day?: string;
    snapshot_day?: string;
    rate_source?: string;
    supported_currencies?: string[]; // 后端确认字段: 可选到账币种白名单(大写代码); 缺省时前端用 CASHFLOW_CURRENCIES。
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
    source_currency?: string;
    source_amount?: number;
    quote_currency?: string;
    rate_day?: string;
    currency?: string; // 兼容字段: 后端两条命名并存(currency=quote_currency), 归一在 withdrawalSnapshotOf。
    payout_currency?: string;
    payout_amount?: number;
    actual_currency?: string; // 实际打款币种(mark-paid 时管理员登记), 与申请币种不同时展示端提示。
    snapshot_day?: string;
    rate_source?: string;
};

// 提现单快照归一: 后端新增 rate_day/quote_currency/payout_* 可选字段(Lead 确认), 旧行没有这些字段。
// isLegacyWithdrawal 判定历史旧行: 无 payout_currency 也无快照元数据 → 按旧口径展示 legacy 提示。
export type WithdrawalSnapshot = { day: string; source: string; currency: string };
export function withdrawalSnapshotOf(item: Withdrawal): WithdrawalSnapshot | undefined {
    const day = item.rate_day ?? item.snapshot_day ?? '';
    const currency = item.quote_currency ?? item.currency ?? '';
    if (!day && !currency && !item.rate_source) return undefined;
    return { day, source: item.rate_source ?? '', currency };
}
export function withdrawalPayoutOf(item: Withdrawal): { currency: string; amount: number } | undefined {
    if (!item.payout_currency) return undefined;
    return { currency: item.payout_currency, amount: item.payout_amount ?? item.actual_cents / 100 };
}

// useWithdrawSummary 提现口径(可提/冻结/已提 + 规则)。
export function useWithdrawSummary() {
    return useQuery({
        queryKey: ['withdraw', 'summary'],
        queryFn: () => apiRequest<WithdrawSummary>('/api/v1/withdrawal/summary'),
    });
}

// useWithdrawals 提现记录; status 为空不过滤, userId 非零供管理员按用户筛。
export function useWithdrawals(status: string, userId: number, enabled = true) {
    return useQuery({
        enabled,
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
// payout_currency 可选: 用户选择到账币种时随申请提交, 旧服务端忽略未知字段不受影响。
export function applyWithdrawal(input: {
    amount: number;
    method: string;
    account_name: string;
    account_no: string;
    payout_currency?: string;
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
// actual_currency 可选: 实际打款币种与申请币种不同时登记, 展示端据此区分两套口径。
export function markWithdrawalPaid(id: number, paidRef: string, actualCurrency?: string) {
    const currency = (actualCurrency ?? '').trim().toUpperCase();
    return apiRequest<{ id: number }>('/api/v1/withdrawal/mark-paid', {
        method: 'POST',
        body: { id, paid_ref: paidRef, ...(currency ? { actual_currency: currency } : {}) },
    });
}

// rollbackWithdrawal 打款回滚: paid → approved。
export function rollbackWithdrawal(id: number) {
    return apiRequest<{ id: number }>('/api/v1/withdrawal/rollback', { method: 'POST', body: { id } });
}
