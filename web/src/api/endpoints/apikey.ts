import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';

/**
 * API Key 数据
 */
export interface APIKey {
    id: number;
    name: string;
    api_key: string;
}

/**
 * 创建 API Key 请求
 */
export interface CreateAPIKeyRequest {
    name: string;
}

/**
 * 获取 API Key 列表 Hook
 * 
 * @example
 * const { data: apiKeys, isLoading, error } = useAPIKeyList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * apiKeys?.forEach(key => console.log(key.name));
 */
export function useAPIKeyList() {
    return useQuery({
        queryKey: ['apikeys', 'list'],
        queryFn: async () => {
            return apiClient.get<APIKey[]>('/api/v1/apikey/list');
        },
        refetchInterval: 30000,
    });
}

/**
 * 创建 API Key Hook
 * 
 * @example
 * const createAPIKey = useCreateAPIKey();
 * 
 * createAPIKey.mutate({
 *   name: 'My API Key',
 * });
 */
export function useCreateAPIKey() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (data: CreateAPIKeyRequest) => {
            return apiClient.post<APIKey>('/api/v1/apikey/create', data);
        },
        onSuccess: (data) => {
            logger.log('API Key 创建成功:', data);
            queryClient.invalidateQueries({ queryKey: ['apikeys', 'list'] });
        },
        onError: (error) => {
            logger.error('API Key 创建失败:', error);
        },
    });
}

/**
 * 删除 API Key Hook
 * 
 * @example
 * const deleteAPIKey = useDeleteAPIKey();
 * 
 * deleteAPIKey.mutate(1); // 删除 ID 为 1 的 API Key
 */
export function useDeleteAPIKey() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (id: number) => {
            return apiClient.delete<null>(`/api/v1/apikey/delete/${id}`);
        },
        onSuccess: () => {
            logger.log('API Key 删除成功');
            queryClient.invalidateQueries({ queryKey: ['apikeys', 'list'] });
        },
        onError: (error) => {
            logger.error('API Key 删除失败:', error);
        },
    });
}

