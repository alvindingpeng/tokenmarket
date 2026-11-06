import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { apiQueryRetry } from './client';

// 上游账单对单: 渠道商导入供应商账单(原币成本), 与平台 owner_revenue 逐模型核对。
export type UpstreamBillRow = {
    id: number;
    channel_id: number;
    period: string;
    model_name: string;
    currency: string;
    cost: number;
    cost_usd: number;
    fx_rate: number;
    owner_rev: number;
    diff_ratio: number;
    status: 'pending' | 'matched' | 'mismatch' | 'explained';
    note: string;
};

export function useUpstreamBills(channelID: number, period: string) {
    return useQuery({
        queryKey: ['upstream-bills', channelID, period] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<{ items: UpstreamBillRow[] }>('/api/v1/upstream-bills?channel_id=' + channelID + '&period=' + period, { signal }),
        retry: apiQueryRetry,
        enabled: channelID > 0,
    });
}

export function useImportUpstreamBills() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (data: { channel_id: number; period: string; file: File }) => {
            const form = new FormData();
            form.append('channel_id', String(data.channel_id));
            form.append('period', data.period);
            form.append('file', data.file);
            return apiRequest<{ imported: number }>('/api/v1/upstream-bills/import', { method: 'POST', body: form });
        },
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}

export function useExplainUpstreamBill() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (data: { id: number; note: string }) =>
            apiRequest<Record<string, never>>('/api/v1/upstream-bills/explain', { method: 'POST', body: JSON.stringify(data) }),
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}

export function useRecomputeUpstreamBills() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (data: { channel_id: number; period: string }) =>
            apiRequest<{ recomputed: number }>('/api/v1/upstream-bills/recompute', { method: 'POST', body: JSON.stringify(data) }),
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}
