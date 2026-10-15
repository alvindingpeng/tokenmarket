import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// RechargeView 充值配置视图: 未配置商户参数时充值入口优雅降级。
export type RechargeView = {
    configured: boolean;
    rate: number;
    min_cny: number;
    amount_tiers: number[];
    bonus_enabled: boolean;
    pay_types: string[]; // 支付方式白名单; 空表示收银台自选全开放
};

// RechargeOrder 充值订单; amount_cents 为实付法币分, credits/bonus 为入账口径。
export type RechargeOrder = {
    id: number;
    order_no: string;
    user_id: number;
    provider: string;
    method: string;
    amount_cents: number;
    currency: string;
    credits: number;
    bonus: number;
    subject: string;
    status: 'pending' | 'paid' | 'failed' | 'expired' | 'refunded';
    trade_no: string | null;
    fail_reason: string;
    created_at: string;
    expired_at: string;
    paid_at: string | null;
};

// PayForm 收银台提交表单: 浏览器自动 POST 跳转。
export type PayForm = { action: string; fields: Record<string, string> };

export type RechargeQuote = { credits: number; bonus: number; rate: number };

// useRechargeView 充值配置视图(是否已配置/汇率/快捷金额/满赠开关)。
export function useRechargeView() {
    return useQuery({
        queryKey: ['recharge', 'view'],
        queryFn: () => apiRequest<RechargeView>('/api/v1/recharge/view'),
    });
}

// useRechargeQuote 按实付金额(分)实时报价; amountCents<=0 时不发请求。
export function useRechargeQuote(amountCents: number) {
    return useQuery({
        queryKey: ['recharge', 'quote', amountCents],
        enabled: amountCents > 0,
        queryFn: () =>
            apiRequest<RechargeQuote>('/api/v1/recharge/quote', {
                method: 'POST',
                body: { amount_cents: amountCents },
            }),
    });
}

// createRecharge 下单并返回收银台表单; 成功后由调用方自动提交表单跳转。
export function createRecharge(input: { amount_cents: number; method: string }) {
    return apiRequest<{ order: RechargeOrder; form: PayForm }>('/api/v1/recharge/create', {
        method: 'POST',
        body: input,
    });
}

// useRechargeOrders 充值订单; 传 userId 供管理员查指定用户, 不传查本人(或管理员全量)。
export function useRechargeOrders(userId: number) {
    return useQuery({
        queryKey: ['recharge', 'orders', userId],
        queryFn: () => {
            const params = new URLSearchParams({ limit: '50' });
            if (userId) params.set('user_id', String(userId));
            return apiRequest<{ items: RechargeOrder[] }>(`/api/v1/recharge/list?${params}`);
        },
    });
}
