import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { useAuthStore } from './user';

// QuotaUsage 用户维度的配额与当前用量(与后端 model.QuotaUsage 对齐)。
export type QuotaUsage = {
    user_id: number;
    username: string;
    tier: 'free' | 'basic' | 'pro' | 'enterprise';
    rpm: number;
    tpm: number;
    concurrent: number;
    // source 说明额度来自哪里: 显式策略 / 等级兜底 / 不限制。
    // 两者数值可能相同但含义不同, 页面必须能说出来源, 否则改等级没反应时无从判断。
    source: 'policy' | 'tier' | 'none';
    model: string;
    used_rpm: number;
    used_tpm: number;
    in_flight: number;
    window_reset_at: string;
};

// QuotaUsageResponse 后端返回的使用量 + 剩余量(剩余量由服务端算好, 前端不重复实现"不限制"判定)。
export type QuotaUsageResponse = {
    usage: QuotaUsage;
    remain_rpm: number;
    remain_tpm: number;
    remain_concurrent: number;
};

export const quotaUsageSelfPath = '/api/v1/quota-usage';
export const quotaUsageOpsPath = '/api/v1/ops/quota-usage';

// quotaUsageSelfQueryOptions 本人配额用量查询定义。
export function quotaUsageSelfQueryOptions(userId = 0) {
    return queryOptions({
        queryKey: ['quota-usage', 'self', userId] as const,
        queryFn: ({ signal }) => apiRequest<QuotaUsageResponse>(quotaUsageSelfPath, { signal }),
        retry: apiQueryRetry,
    });
}

// quotaUsageAdminQueryOptions 管理员按用户查询定义; userId 为 0 时不发请求(避免无意义的 400)。
export function quotaUsageAdminQueryOptions(userId: number) {
    return queryOptions({
        queryKey: ['quota-usage', 'admin', userId] as const,
        queryFn: ({ signal }) =>
            apiRequest<QuotaUsageResponse>(`${quotaUsageOpsPath}?user_id=${userId}`, { signal }),
        retry: apiQueryRetry,
        enabled: userId > 0,
    });
}

// useQuotaUsageSelf 查询本人配额用量。
export function useQuotaUsageSelf() {
    const userId = useAuthStore((state) => state.userId);
    return useQuery(quotaUsageSelfQueryOptions(userId));
}

// useQuotaUsageAdmin 管理员查询指定用户的配额用量。
export function useQuotaUsageAdmin(userId: number) {
    return useQuery(quotaUsageAdminQueryOptions(userId));
}

// usageRatio 计算某维度的使用占比(0-1); 额度为 0 表示不限制, 返回 0 让界面显示"不限制"。
// 超过额度时封顶为 1, 进度条不会溢出。
export function usageRatio(limit: number, used: number): number {
    if (limit <= 0) return 0;
    return Math.min(used / limit, 1);
}

// quotaLevel 按占比给出提示等级: >=0.9 danger, >=0.7 warn, 其余 ok。
// 单独抽成纯函数是为了能单测边界, 而不是把阈值散在 JSX 里。
export function quotaLevel(limit: number, used: number): 'unlimited' | 'ok' | 'warn' | 'danger' {
    if (limit <= 0) return 'unlimited';
    const ratio = usageRatio(limit, used);
    if (ratio >= 0.9) return 'danger';
    if (ratio >= 0.7) return 'warn';
    return 'ok';
}
