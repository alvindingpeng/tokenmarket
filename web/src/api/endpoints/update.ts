import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';

/**
 * 系统版本信息
 */
export interface SysVersionInfo {
    now_version: string;
    latest_version: string;
    latest_published_at: string;
    latest_body: string;
    latest_message: string;
}

/**
 * 获取系统版本信息 Hook
 * 
 * @example
 * const { data: versionInfo, isLoading, error } = useVersionInfo();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * console.log('Current version:', versionInfo?.now_version);
 * console.log('Latest version:', versionInfo?.latest_version);
 */
export function useVersionInfo() {
    return useQuery({
        queryKey: ['update', 'version'],
        queryFn: async () => {
            return apiClient.get<SysVersionInfo>('/api/v1/update');
        },
        refetchInterval: 3600000, // 1 小时
        refetchOnMount: 'always',
    });
}

/**
 * 执行更新 Hook
 * 
 * @example
 * const updateCore = useUpdateCore();
 * 
 * updateCore.mutate(undefined, {
 *   onSuccess: () => {
 *     console.log('Update started successfully');
 *   },
 * });
 */
export function useUpdateCore() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async () => {
            return apiClient.post<string>('/api/v1/update');
        },
        onSuccess: (data) => {
            logger.log('更新成功:', data);
            queryClient.invalidateQueries({ queryKey: ['update', 'version'] });
        },
        onError: (error) => {
            logger.error('更新失败:', error);
        },
    });
}

