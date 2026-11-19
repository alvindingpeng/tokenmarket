import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// RedemptionBatch 批次行; enabled/used/disabled 为该批次各状态张数。
export type RedemptionBatch = {
    id: number;
    name: string;
    credits: number;
    count: number;
    prefix: string;
    per_user_limit: number;
    expires_at: string | null;
    created_by: number;
    created_at: string;
    enabled: number;
    used: number;
    disabled: number;
};

export type RedemptionCode = {
    id: number;
    batch_id: number;
    code: string;
    credits: number;
    status: 'enabled' | 'used' | 'disabled';
    used_by: number | null;
    used_at: string | null;
    created_at: string;
};

export type RedemptionBatchInput = {
    name: string;
    credits: number;
    count: number;
    prefix: string;
    per_user_limit: number;
    expires_at?: string | null;
};

// useRedeem 兑换兑换码; 成功后刷新钱包与兑换记录。
export function useRedeem() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (code: string) =>
            apiRequest<{ credits: number; balance: number }>('/api/v1/redemption/redeem', {
                method: 'POST',
                body: { code },
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['wallet'] });
            void queryClient.invalidateQueries({ queryKey: ['redemption'] });
        },
    });
}

// useRedemptionHistory 本人兑换记录。
export function useRedemptionHistory() {
    return useQuery({
        queryKey: ['redemption', 'history'],
        queryFn: () => apiRequest<{ items: RedemptionCode[] }>('/api/v1/redemption/history'),
    });
}

// useRedemptionBatches 管理端批次列表(含各状态张数)。
export function useRedemptionBatches() {
    return useQuery({
        queryKey: ['redemption', 'batches'],
        queryFn: () => apiRequest<{ items: RedemptionBatch[] }>('/api/v1/redemption/batch/list'),
    });
}

// useRedemptionCodes 管理端兑换码分页列表。
export function useRedemptionCodes(batchId: number, status: string, offset: number) {
    return useQuery({
        queryKey: ['redemption', 'codes', batchId, status, offset],
        queryFn: () => {
            const params = new URLSearchParams({ limit: '20', offset: String(offset) });
            if (batchId) params.set('batch_id', String(batchId));
            if (status) params.set('status', status);
            return apiRequest<{ items: RedemptionCode[]; total: number }>(
                `/api/v1/redemption/code/list?${params}`,
            );
        },
    });
}

// useCreateRedemptionBatch 生成批次; 成功返回全部新码供导出。
export function useCreateRedemptionBatch() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (input: RedemptionBatchInput) =>
            apiRequest<{ batch: RedemptionBatch; codes: RedemptionCode[] }>('/api/v1/redemption/batch/create', {
                method: 'POST',
                body: input,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['redemption'] });
        },
    });
}

// useDisableRedemptionCode 禁用未兑兑换码。
export function useDisableRedemptionCode() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<{ id: number }>('/api/v1/redemption/code/disable', { method: 'POST', body: { id } }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['redemption'] });
        },
    });
}
