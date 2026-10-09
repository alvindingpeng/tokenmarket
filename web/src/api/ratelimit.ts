import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * RateScopeType 限流范围类型; 越具体越优先。
 */
export type RateScopeType = 'system' | 'user' | 'api_key' | 'group' | 'channel' | 'channel_key' | 'channel_model';

/**
 * RateLimitPolicy 一条限流策略; rpm/tpm 为 0 表示该维度不限。
 */
export type RateLimitPolicy = {
    id: number;
    scope_type: RateScopeType;
    scope_id: number;
    model_name: string;
    rpm: number;
    tpm: number;
    concurrent: number;
    enabled: boolean;
    created_at: string;
    updated_at: string;
};

/**
 * RateLimitInspect 某范围的生效限流值与当前窗口用量。
 */
export type RateLimitInspect = {
    scope_type: string;
    scope_id: number;
    model_name: string;
    rpm: number;
    tpm: number;
    concurrent: number;
    requests: number;
    active: number;
    tokens: number;
    reset_at: string;
    blocked: boolean;
    budgets?: {model_name:string;rpm:number;tpm:number;concurrent:number;requests:number;tokens:number;active:number}[];
};

/**
 * RateLimitUsageHour 限流用量的小时聚合。
 */
export type RateLimitUsageHour = {
    id: number;
    scope_type: string;
    scope_id: number;
    hour: string;
    requests: number;
    input_tokens: number;
    output_tokens: number;
    rejected: number;
};

/**
 * RatePolicyInput 限流策略写入参数; id 为 0 表示新建。
 */
export type RatePolicyInput = {
    id?: number;
    scope_type: RateScopeType;
    scope_id: number;
    model_name: string;
    rpm: number;
    tpm: number;
    concurrent: number;
    enabled: boolean;
};

/**
 * useRatePolicies 拉取全部限流策略。
 */
export function useRatePolicies() {
    return useQuery({
        queryKey: ['ratelimit', 'policies'],
        queryFn: () => apiRequest<{ items: RateLimitPolicy[] }>('/api/v1/ratelimit/policy'),
    });
}

/**
 * useSaveRatePolicy 新建或更新限流策略。
 *
 * @example
 * save.mutate({ scope_type: 'user', scope_id: 1, model_name: '', rpm: 60, tpm: 100000, enabled: true });
 */
export function useSaveRatePolicy() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: RatePolicyInput) =>
            apiRequest<RateLimitPolicy>('/api/v1/ratelimit/policy/save', { method: 'POST', body: data }),
        onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['ratelimit'] }),
    });
}

/**
 * useDeleteRatePolicy 删除限流策略。
 */
export function useDeleteRatePolicy() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>('/api/v1/ratelimit/policy/delete', { method: 'POST', body: { id } }),
        onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['ratelimit'] }),
    });
}

/**
 * useRateInspect 查询某范围的生效限流值与当前窗口用量。
 */
export function useRateInspect(scopeType: string, scopeId: number, modelName: string) {
    const query = new URLSearchParams({ scope_type: scopeType, scope_id: String(scopeId), model: modelName });
    return useQuery({
        queryKey: ['ratelimit', 'inspect', scopeType, scopeId, modelName],
        queryFn: () => apiRequest<RateLimitInspect>('/api/v1/ratelimit/inspect?' + query.toString()),
        refetchInterval: 5000,
        enabled: scopeType !== '',
    });
}

/**
 * useRateUsage 拉取最近若干小时的限流用量聚合。
 */
export function useRateUsage(hours = 24, scopeType = '', scopeId = 0) {
    const query = new URLSearchParams({ hours: String(hours), scope_type: scopeType, scope_id: String(scopeId) });
    return useQuery({
        queryKey: ['ratelimit', 'usage', hours, scopeType, scopeId],
        queryFn: () => apiRequest<{ items: RateLimitUsageHour[] }>('/api/v1/ratelimit/usage?' + query.toString()),
        refetchInterval: 30000,
    });
}
