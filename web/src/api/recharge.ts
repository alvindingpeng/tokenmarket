import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// PayChannel 充值渠道(对齐 New API pay_methods): 前端按 configured 决定可选渠道。
export type PayChannel = {
    id: string; // epay|alipay|stripe|creem
    name: string;
    configured: boolean;
    currency: string;
    pay_types?: string[];
};

// RechargeView 充值配置视图: 未配置商户参数时充值入口优雅降级。
export type RechargeView = {
    configured: boolean;
    rate: number;
    min_cny: number;
    amount_tiers: number[];
    bonus_enabled: boolean;
    pay_types: string[]; // 易支付方式白名单; 空表示收银台自选全开放
    channels: PayChannel[];
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
    pay_cents: number; // 渠道实付额(渠道币种分)
    pay_currency: string;
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

// PayForm 收银台跳转: fields 非空走表单自动 POST, 否则直接跳 redirect_url。
export type PayForm = { action: string; fields: Record<string, string>; redirect_url: string };

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

// createRecharge 下单并返回收银台跳转; 成功后由调用方提交表单或跳转链接。
export function createRecharge(input: { amount_cents: number; provider: string; method: string }) {
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
