import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { UserView } from './user';

/**
 * 计费明细（与后端 model.BillingRecord 对齐）：
 * user_price 为上浮后的用户价，supply_price 为供货价；
 * user_cost 按用户价计支出，owner_revenue 按供货价计渠道商收入，platform_revenue 为上浮差额。
 */
export type BillingRecord = {
    id: number;
    request_id: string;
    user_id: number;
    api_key_id: number;
    group_id: number;
    group_model: string;
    channel_id: number;
    owner_id: number;
    share_code: string;
    model_name: string;
    supply_price: {
        input: number;
        output: number;
        cache_read: number;
        cache_write: number;
    };
    user_price: {
        input: number;
        output: number;
        cache_read: number;
        cache_write: number;
    };
    input_token: number;
    output_token: number;
    cache_read_token: number;
    cache_write_token: number;
    media_units?: {
        images?: number;
        seconds?: number;
        resolution?: string;
    };
    media_user?: {
        per_image?: number;
        per_second?: number;
    };
    media_supply?: {
        per_image?: number;
        per_second?: number;
    };
    user_cost: number;
    owner_revenue: number;
    platform_revenue: number;
    status: 'settled' | 'refunded';
    created_at: string;
};

/**
 * 计费日聚合行（消费与收入视图共用同一种行形状）。
 */
export type BillingDaily = {
    date: string;
    input_token: number;
    output_token: number;
    cache_read_token: number;
    cache_write_token: number;
    request_count: number;
    user_cost: number;
    owner_revenue: number;
    platform_revenue: number;
};

// BillingFilter 计费明细的筛选条件; 后端对非管理员强制只看自己。
export type BillingFilter = {
    model?: string;
    channel?: string;
    status?: string;
    from?: string;
    to?: string;
    user_id?: number;
};

// billingQuery 把筛选条件与分页参数序列化为查询串, 列表与导出共用。
function billingQuery(filter: BillingFilter, limit: number, offset: number): string {
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (filter.model) query.set('model', filter.model);
    if (filter.channel) query.set('channel', filter.channel);
    if (filter.status) query.set('status', filter.status);
    if (filter.from) query.set('from', filter.from);
    if (filter.to) query.set('to', filter.to);
    if (filter.user_id) query.set('user_id', String(filter.user_id));
    return query.toString();
}

/**
 * useBillingRecords 计费明细分页查询：普通用户/渠道商恒返回自己的，管理员可按 user_id 过滤。
 */
export function useBillingRecords(filter: BillingFilter = {}, limit = 20, offset = 0) {
    return useQuery({
        queryKey: ['billing', 'records', filter, limit, offset],
        queryFn: () => apiRequest<{ items: BillingRecord[]; total: number }>('/api/v1/stats/billing/records?' + billingQuery(filter, limit, offset)),
        placeholderData: (previous) => previous,
    });
}

// billingExportUrl 构造计费明细 CSV 下载地址(同一套筛选条件, 单次上限 5000 条)。
export function billingExportUrl(filter: BillingFilter = {}): string {
    return './api/v1/stats/billing/records/export?' + billingQuery(filter, 5000, 0);
}

/** 余额流水一行: Amount 为可用余额净变动, Frozen 为冻结额变动, Cost 为本次实际费用。 */
export type BalanceLedger = {
    id: number;
    user_id: number;
    kind: 'reserve' | 'settle' | 'release' | 'adjust';
    amount: number;
    frozen: number;
    cost: number;
    balance_after: number;
    request_id: string;
    model_name: string;
    actor_id: number;
    actor_name: string;
    note: string;
    created_at: string;
};

/**
 * useBalanceLedger 余额流水分页查询：普通用户恒返回自己的，管理员可按 user_id 指定。
 */
export function useBalanceLedger(kind = '', limit = 20, offset = 0, userID = 0) {
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (kind) query.set('kind', kind);
    if (userID) query.set('user_id', String(userID));
    return useQuery({
        queryKey: ['billing', 'ledger', kind, limit, offset, userID],
        queryFn: () => apiRequest<{ items: BalanceLedger[]; total: number }>('/api/v1/stats/balance/ledger?' + query.toString()),
        placeholderData: (previous) => previous,
    });
}

/**
 * useBillingDaily 当前用户近 30 天消费聚合。
 */
export function useBillingDaily() {
    return useQuery({
        queryKey: ['billing', 'daily'],
        queryFn: () => apiRequest<BillingDaily[]>('/api/v1/stats/billing'),
    });
}

/**
 * useRevenueDaily 收入聚合（供货价口径）：渠道商恒返回自己，管理员可按 owner_id 过滤。
 */
export function useRevenueDaily(ownerID = 0) {
    return useQuery({
        queryKey: ['billing', 'revenue', ownerID],
        queryFn: () => apiRequest<BillingDaily[]>(`/api/v1/stats/billing/revenue${ownerID ? `?owner_id=${ownerID}` : ''}`),
    });
}

/**
 * useAccountProfile 账户概览：余额、冻结额、累计消费/收入（登录态轮询自 /user/status）。
 */
export function useAccountProfile() {
    return useQuery({
        queryKey: ['user', 'status'],
        queryFn: () => apiRequest<UserView>('/api/v1/user/status'),
    });
}
