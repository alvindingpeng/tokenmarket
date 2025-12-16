import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';
import { useEffect, useRef, useState, useCallback } from 'react';

/**
 * 日志数据
 */
export interface RelayLog {
    id: number;
    time: number;                // 时间戳
    request_model_name: string;  // 请求模型名称
    channel: number;             // 实际使用的渠道ID
    actual_model_name: string;   // 实际使用模型名称
    input_tokens: number;        // 输入Token
    output_tokens: number;       // 输出Token
    ftut: number;                // 首字时间(毫秒)
    use_time: number;            // 总用时(毫秒)
    cost: number;                // 消耗费用
    request_content: string;     // 请求内容
    response_content: string;    // 响应内容
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
 * SSE 实时日志流 Hook
 * 
 * @example
 * const { logs, isConnected, error, clear } = useLogStream();
 * 
 * // logs 会实时更新
 * logs.forEach(log => console.log(log.request_model_name));
 * 
 * // 清空当前接收的日志
 * clear();
 */
export function useLogStream(maxLogs: number = 100) {
    const [logs, setLogs] = useState<RelayLog[]>([]);
    const [isConnected, setIsConnected] = useState(false);
    const [error, setError] = useState<Error | null>(null);
    const eventSourceRef = useRef<EventSource | null>(null);

    const clear = useCallback(() => {
        setLogs([]);
    }, []);

    useEffect(() => {
        let eventSource: EventSource | null = null;

        const connect = async () => {
            try {
                // 先获取临时 token
                const { token } = await apiClient.get<{ token: string }>('/api/v1/log/stream-token');

                // 使用临时 token 连接 SSE
                eventSource = new EventSource(`/api/v1/log/stream?token=${token}`);
                eventSourceRef.current = eventSource;

                eventSource.onopen = () => {
                    setIsConnected(true);
                    setError(null);
                };

                eventSource.onmessage = (event) => {
                    try {
                        const log: RelayLog = JSON.parse(event.data);
                        setLogs((prevLogs) => {
                            const newLogs = [log, ...prevLogs];
                            if (newLogs.length > maxLogs) {
                                return newLogs.slice(0, maxLogs);
                            }
                            return newLogs;
                        });
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
    }, [maxLogs]);

    return {
        logs,
        isConnected,
        error,
        clear,
    };
}
