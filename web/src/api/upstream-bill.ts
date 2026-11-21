import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry, apiUpload } from './client';

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

// importUpstreamBills 上传供应商账单 CSV。
// 必须走 apiUpload: 后端用 c.PostForm("channel_id") 读 multipart 字段, FormData 若交给 apiRequest
// 会被 JSON.stringify 成 "{}" 且硬带 application/json, channel_id 拿到空串直接 400 Invalid parameter。
// apiUpload 内部不带 Content-Type, 由浏览器按 FormData 自动补 multipart boundary。
export function importUpstreamBills(data: { channel_id: number; period: string; file: File }) {
    const form = new FormData();
    form.append('channel_id', String(data.channel_id));
    form.append('period', data.period);
    form.append('file', data.file);
    return apiUpload<{ imported: number }>('/api/v1/upstream-bills/import', form);
}

export function useImportUpstreamBills() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: importUpstreamBills,
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}

// explainUpstreamBill 定性一条差异(人工说明为什么对不上)。
// 后端 c.ShouldBindJSON(&request), 因此请求体必须是对象本身 —— 外面再 stringify 一层会变成
// JSON 字符串字面量, 后端 400, 这条差异永远定性不了。
export function explainUpstreamBill(data: { id: number; note: string }) {
    return apiRequest<Record<string, never>>('/api/v1/upstream-bills/explain', { method: 'POST', body: data });
}

export function useExplainUpstreamBill() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: explainUpstreamBill,
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}

// recomputeUpstreamBills 按渠道+账期重算对单(同样走 ShouldBindJSON, 同理不能二次 stringify)。
export function recomputeUpstreamBills(data: { channel_id: number; period: string }) {
    return apiRequest<{ recomputed: number }>('/api/v1/upstream-bills/recompute', { method: 'POST', body: data });
}

export function useRecomputeUpstreamBills() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: recomputeUpstreamBills,
        onSuccess: () => { void qc.invalidateQueries({ queryKey: ['upstream-bills'] }); },
    });
}
