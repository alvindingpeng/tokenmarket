import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useLogStore } from '@/stores/log';

/**
 * 日志数据
 */
export interface RelayLog {
    id: number;
    time: number;                // 时间戳
    request_model_name: string;  // 请求模型名称
    channel: number;             // 实际使用的渠道ID
    channel_name: string;        // 渠道名称
    actual_model_name: string;   // 实际使用模型名称
    input_tokens: number;        // 输入Token
    output_tokens: number;       // 输出Token
    ftut: number;                // 首字时间(毫秒)
    use_time: number;            // 总用时(毫秒)
    cost: number;                // 消耗费用
    request_content: string;     // 请求内容
    response_content: string;    // 响应内容
    error: string;                // 错误信息
}

/**
 * 日志列表查询参数
 */
export interface LogListParams {
    page?: number;
    page_size?: number;
    start_time?: number;
    end_time?: number;
}

/**
 * 获取日志列表 Hook
 * 
 * @example
 * const { data: logs, isLoading, error } = useLogList({ page: 1, page_size: 20 });
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * logs?.forEach(log => console.log(log.request_model_name));
 */
export function useLogList(params: LogListParams = {}) {
    const { page = 1, page_size = 20, start_time, end_time } = params;

    const queryParams = new URLSearchParams();
    queryParams.set('page', String(page));
    queryParams.set('page_size', String(page_size));
    if (start_time !== undefined) {
        queryParams.set('start_time', String(start_time));
    }
    if (end_time !== undefined) {
        queryParams.set('end_time', String(end_time));
    }

    return useQuery({
        queryKey: ['logs', 'list', page, page_size, start_time, end_time],
        queryFn: async () => {
            return apiClient.get<RelayLog[]>(`/api/v1/log/list?${queryParams.toString()}`);
        },
    });
}

/**
 * 清空日志 Hook
 * 
 * @example
 * const clearLogs = useClearLogs();
 * 
 * clearLogs.mutate();
 */
export function useClearLogs() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async () => {
            return apiClient.delete<null>('/api/v1/log/clear');
        },
        onSuccess: () => {
            logger.log('日志清空成功');
            queryClient.invalidateQueries({ queryKey: ['logs', 'list'] });
        },
        onError: (error) => {
            logger.error('日志清空失败:', error);
        },
    });
}

/**
 * 日志管理 Hook
 * 整合初始加载、SSE 实时推送、滚动加载更多
 * 
 * @example
 * const { logs, isConnected, hasMore, isLoadingMore, loadMore, clear } = useLogs();
 * 
 * // logs 自动包含历史日志和实时日志，按时间倒序
 * logs.forEach(log => console.log(log.request_model_name));
 * 
 * // 滚动到底部时加载更多
 * if (hasMore && !isLoadingMore) loadMore();
 */
export function useLogs(options: { pageSize?: number } = {}) {
    const { pageSize = 20 } = options;

    const [isConnected, setIsConnected] = useState(false);
    const [error, setError] = useState<Error | null>(null);
    const [isLoadingMore, setIsLoadingMore] = useState(false);
    const eventSourceRef = useRef<EventSource | null>(null);
    const initializedRef = useRef(false);
    const pageRef = useRef(1);

    // 使用全局 store
    const { logs, hasMore, initializeLogs, appendLogs, addLog, clearLogs } = useLogStore();

    // 初始加载历史日志
    const { data: initialLogs, isLoading: isInitialLoading } = useQuery({
        queryKey: ['logs', 'initial', pageSize],
        queryFn: async () => {
            const params = new URLSearchParams();
            params.set('page', '1');
            params.set('page_size', String(pageSize));
            return apiClient.get<RelayLog[]>(`/api/v1/log/list?${params.toString()}`);
        },
        staleTime: Infinity,
    });

    // 将初始日志添加到 store
    useEffect(() => {
        if (initialLogs && !initializedRef.current) {
            initializeLogs(initialLogs, pageSize);
            initializedRef.current = true;
        }
    }, [initialLogs, initializeLogs, pageSize]);

    // 加载更多历史日志
    const loadMore = useCallback(async () => {
        if (isLoadingMore || !hasMore) return;

        setIsLoadingMore(true);
        try {
            const nextPage = pageRef.current + 1;
            const params = new URLSearchParams();
            params.set('page', String(nextPage));
            params.set('page_size', String(pageSize));

            const moreLogs = await apiClient.get<RelayLog[]>(`/api/v1/log/list?${params.toString()}`);
            appendLogs(moreLogs, pageSize);
            pageRef.current = nextPage;
        } catch (e) {
            logger.error('加载更多日志失败:', e);
        } finally {
            setIsLoadingMore(false);
        }
    }, [isLoadingMore, hasMore, pageSize, appendLogs]);

    // SSE 连接
    useEffect(() => {
        let eventSource: EventSource | null = null;

        const connect = async () => {
            try {
                const { token } = await apiClient.get<{ token: string }>('/api/v1/log/stream-token');
                eventSource = new EventSource(`${process.env.NEXT_PUBLIC_API_BASE_URL || ''}/api/v1/log/stream?token=${token}`);
                eventSourceRef.current = eventSource;

                eventSource.onopen = () => {
                    setIsConnected(true);
                    setError(null);
                };

                eventSource.onmessage = (event) => {
                    try {
                        const log: RelayLog = JSON.parse(event.data);
                        addLog(log);
                    } catch (e) {
                        logger.error('解析日志数据失败:', e);
                    }
                };

                eventSource.onerror = () => {
                    setIsConnected(false);
                    setError(new Error('SSE 连接断开'));
                    eventSource?.close();
                };
            } catch (e) {
                setError(e instanceof Error ? e : new Error('获取 stream token 失败'));
                logger.error('获取 stream token 失败:', e);
            }
        };

        connect();

        return () => {
            eventSource?.close();
            eventSourceRef.current = null;
            setIsConnected(false);
        };
    }, [addLog]);

    // 清空时重置分页
    const clear = useCallback(() => {
        clearLogs();
        pageRef.current = 1;
        initializedRef.current = false;
    }, [clearLogs]);

    return {
        logs,
        isConnected,
        error,
        hasMore,
        isLoading: isInitialLoading,
        isLoadingMore,
        loadMore,
        clear,
    };
}
