import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { modelListQueryOptions } from './queries';
import { POLL_SLOW } from '@/lib/polling';

/**
 * LLM 价格信息
 */
export interface LLMPrice {
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
}

/**
 * LLM 模型信息
 */
export interface LLMInfo extends LLMPrice {
    name: string;
}

/**
 * 获取 LLM 模型列表 Hook
 * 
 * @example
 * const { data: models, isLoading, error } = useModelList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * models?.forEach(model => console.log(model.name, model.input));
 */
export function useModelList() {
    return useQuery({
        ...modelListQueryOptions,
        refetchInterval: POLL_SLOW,
        refetchOnMount: 'always',
    });
}

/**
 * 更新 LLM 模型 Hook
 * 
 * @example
 * const updateModel = useUpdateModel();
 * 
 * updateModel.mutate({
 *   name: 'gpt-4',
 *   input: 0.03,
 *   output: 0.06,
 *   cache_read: 0.015,
 *   cache_write: 0.03,
 * });
 */
export function useUpdateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: LLMInfo) =>
            apiRequest<LLMInfo>('/api/v1/model/update', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey }),
    });
}

/**
 * 创建 LLM 模型 Hook
 * 
 * @example
 * const createModel = useCreateModel();
 * 
 * createModel.mutate({
 *   name: 'gpt-4',
 *   input: 0.03,
 *   output: 0.06,
 *   cache_read: 0.015,
 *   cache_write: 0.03,
 * });
 */
export function useCreateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: LLMInfo) =>
            apiRequest<LLMInfo>('/api/v1/model/create', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey }),
    });
}

/**
 * 删除 LLM 模型 Hook
 * 
 * @example
 * const deleteModel = useDeleteModel();
 * 
 * deleteModel.mutate('gpt-4'); // 删除名称为 'gpt-4' 的模型
 */
export function useDeleteModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (name: string) =>
            apiRequest<null>('/api/v1/model/delete', { method: 'POST', body: { name } }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey }),
    });
}

/**
 * 更新 LLM 模型价格 Hook
 * 
 * @example
 * const updatePrice = useUpdateModelPrice();
 * 
 * updatePrice.mutate(); // 触发价格更新
 */
export function useUpdateModelPrice() {
    const queryClient = useQueryClient();

    return useMutation({
        // 价格同步要访问 models.dev, 网络慢时可能超过默认超时。
        mutationFn: () => apiRequest<null>('/api/v1/model/update-price', { method: 'POST', body: {}, timeoutMs: 90000 }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['models', 'last-update-time'] }),
    });
}

/**
 * 重建渠道模型价格 Hook
 */
export function useRebuildModelPrice() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: () => apiRequest<{ count: number }>('/api/v1/model/rebuild-price', { method: 'POST', body: {} }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey }),
    });
}

/**
 * 获取 LLM 模型价格最后更新时间 Hook
 * 
 * @example
 * const { data: lastUpdateTime } = useLastUpdateTime();
 * 
 * if (lastUpdateTime) {
 *   console.log('最后更新:', new Date(lastUpdateTime).toLocaleString());
 * }
 */
export function useLastUpdateTime() {
    return useQuery({
        queryKey: ['models', 'last-update-time'],
        queryFn: () => apiRequest<string>('/api/v1/model/last-update-time'),
        refetchInterval: POLL_SLOW,
    });
}

/**
 * 模型选路评分: 以「渠道模型」为单位全局统计的首响应耗时与成功率滑动平均,
 * 同名模型跨渠道的多份观测由后端按样本数加权归并。
 */
export interface ModelScoreChannel {
    channel: string;
    wait_ms: number;
    success: number;
    samples: number;
}

export interface ModelScore {
    name: string;
    wait_ms: number;
    success: number;
    samples: number;
    channels: ModelScoreChannel[];
}

export function useModelScores() {
    return useQuery({
        queryKey: ['model', 'scores'],
        queryFn: () => apiRequest<ModelScore[]>('/api/v1/model/scores'),
        refetchInterval: POLL_SLOW,
        refetchOnMount: 'always',
    });
}

/**
 * 模型能力登记（输入输出模态与规模上限）
 */
export interface ModelCapability {
    name: string;           // 模型名称
    input: string;          // 输入模态（逗号分隔）：text,image,audio,video,pdf
    output: string;         // 输出模态（逗号分隔）：text,image,video,audio,pdf
    context: number;        // 上下文窗口（token），0 表示未登记
    max_output: number;     // 单次最大输出（token），0 表示未登记
    family: string;         // 官方系列名
    source: string;         // 来源：models.dev 或 manual
    updated_at: string;     // 最近更新时间
}

/**
 * 获取模型能力登记列表
 * 
 * @example
 * const { data: capabilities } = useModelCapabilities();
 */
export function useModelCapabilities() {
    return useQuery({
        queryKey: ['models', 'capabilities'],
        queryFn: () => apiRequest<ModelCapability[]>('/api/v1/model/capability'),
        refetchInterval: POLL_SLOW,
    });
}

/**
 * 保存模型能力登记（人工补录或覆盖）
 */
export function useSaveModelCapability() {
    const queryClient = useQueryClient();
    
    return useMutation({
        mutationFn: (data: ModelCapability) =>
            apiRequest<ModelCapability>('/api/v1/model/capability/save', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'capabilities'] });
        },
    });
}

/**
 * 删除模型能力登记
 */
export function useDeleteModelCapability() {
    const queryClient = useQueryClient();
    
    return useMutation({
        mutationFn: (name: string) =>
            apiRequest<null>('/api/v1/model/capability/delete', { method: 'POST', body: { name } }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['models', 'capabilities'] });
        },
    });
}
