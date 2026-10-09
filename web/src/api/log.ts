import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiRequest } from './client';

// RequestState 表示 Relay 请求的实时状态。
export type RequestState = 'running' | 'committed' | 'success' | 'failed' | 'canceled';

// RelayUsage 保存请求结束后确认的统一 Token 用量。
export interface RelayUsage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    prompt_tokens_details: {
        cached_tokens: number;
        write_cached_tokens?: number;
    } | null;
}

// RelayLogOverview 是请求状态流发送的完整进程内请求状态。
export interface RelayLogOverview {
 route_events?: {at:string;phase:string;reason:string}[];
    id: number;
    client_ip?: string;
    status: RequestState;
    started_at: string;
    duration: number;
    first_token_duration: number;
    stream_duration: number;
    response_duration: number;
    model: string;
    reasoning_effort: string;
    protocol: number;
    group_id: number;
    api_key_name: string;
    usage: RelayUsage;
    cost: number;
    output_chars: number;
    round: number;
    round_started_at: string;
    target_channel_key: string; // 本轮选中的渠道名称和 Key 名称, 以空格分隔。
    target_channel_code: string; // 本轮落地渠道的发布编码, 用户侧的渠道标识; 未发布为空。
    target_model: string;
    target_protocol: number;
    sending: boolean;
    error?: string;
}

export interface RelayAttempt {
 id: number; round: number; channel_code: string; target_model: string;
 started_at: string; duration_ms: number; status: string; error: string; decision: string;
}
export function useLogAttempts(id: number, enabled: boolean, active: boolean) {
 return useQuery({queryKey:['logs','attempts',id],queryFn:()=>apiRequest<{items:RelayAttempt[]}>(`/api/v1/log/attempts/${id}`),enabled,refetchInterval:active?1000:false});
}

// useClearLogs 清空已完成的内存日志。
export function useClearLogs() {
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/clear', { method: 'DELETE' }),
    });
}

// useStopRequest 按是否提供轮次参数, 中止单个轮次或整个请求。
export function useStopRequest() {
    return useMutation({
        mutationFn: ({ requestId, round }: { requestId: number; round?: number }) =>
            apiRequest<null>(`/api/v1/log/stop/${requestId}${round === undefined ? '' : `/${round}`}`, { method: 'POST' }),
    });
}

// useLogs 订阅进程内日志概览，并按 RequestID 更新同一条记录。
export function useLogs() {
    const [logs, setLogs] = useState<RelayLogOverview[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    useEffect(() => {
        const source = new EventSource('./api/v1/log/overview/stream', { withCredentials: true });

        source.onopen = () => {
            setError(null);
            setIsLoading(false);
        };
        source.addEventListener('log', (event) => {
            let next: RelayLogOverview;
            try {
                next = JSON.parse((event as MessageEvent<string>).data) as RelayLogOverview;
            } catch {
                setError(new Error('Invalid log update'));
                return;
            }
            setIsLoading(false);
            setError(null);
            // 列表始终按 ID 倒序: 命中已有记录时原地替换, 新记录插入到首个更小 ID 之前,
            // 由此避免每条更新重排整个列表, 并保留未变更记录的引用以跳过卡片重渲染。
            setLogs((current) => {
                const index = current.findIndex((item) => item.id === next.id);
                if (index >= 0) {
                    const updated = current.slice();
                    updated[index] = next;
                    return updated;
                }
                const position = current.findIndex((item) => item.id < next.id);
                if (position < 0) return [...current, next];
                return [...current.slice(0, position), next, ...current.slice(position)];
            });
        });
        source.onerror = () => {
            setIsLoading(false);
            setError(new Error('Log stream disconnected'));
        };

        return () => {
            source.close();
        };
    }, []);

    return { logs, isLoading, error };
}

// LogListParams 是历史日志分页查询的筛选条件; 全部可选。
export interface LogListParams {
    limit: number;
    offset: number;
    keyword?: string;
    status?: string; // 逗号分隔的终态列表。
    from?: string; // RFC3339 起始时间。
    to?: string; // RFC3339 结束时间。
    clientIp?: string; // 客户端 IP 模糊匹配。
    model?: string; // 模型名模糊匹配。
    channel?: string; // 渠道模糊匹配。
}

// logFilterQuery 把筛选条件序列化为查询串, 列表与导出共用。
function logFilterQuery(params: Omit<LogListParams, 'limit' | 'offset'>): string {
    const query = new URLSearchParams();
    if (params.keyword) query.set('keyword', params.keyword);
    if (params.status) query.set('status', params.status);
    if (params.from) query.set('from', params.from);
    if (params.to) query.set('to', params.to);
    if (params.clientIp) query.set('client_ip', params.clientIp);
    if (params.model) query.set('model', params.model);
    if (params.channel) query.set('channel', params.channel);
    return query.toString();
}

// logExportUrl 构造日志 CSV 导出下载地址(同一套筛选条件, 单次上限 5000 条)。
export function logExportUrl(params: Omit<LogListParams, 'limit' | 'offset'>): string {
    return './api/v1/log/export?' + logFilterQuery(params);
}

// useLogList 分页拉取历史调用日志(倒序), 筛选条件变化时由调用方更新 key。
export function useLogList(params: LogListParams) {
    const query = new URLSearchParams({ limit: String(params.limit), offset: String(params.offset) });
    if (params.keyword) query.set('keyword', params.keyword);
    if (params.status) query.set('status', params.status);
    if (params.from) query.set('from', params.from);
    if (params.to) query.set('to', params.to);
    if (params.clientIp) query.set('client_ip', params.clientIp);
    if (params.model) query.set('model', params.model);
    if (params.channel) query.set('channel', params.channel);
    return useQuery({
        queryKey: ['logs', 'list', query.toString()],
        queryFn: () => apiRequest<{ items: RelayLogOverview[]; total: number }>(`/api/v1/log/list?${query.toString()}`),
        refetchInterval: 15000,
        placeholderData: (previous) => previous,
    });
}

// useLogRequestBody 在调用方启用时按需获取指定日志的请求体。
export function useLogRequestBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'request-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/request-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}

// useLogResponseBody 在调用方启用时获取指定日志的最终响应体。
export function useLogResponseBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'response-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/response-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}
