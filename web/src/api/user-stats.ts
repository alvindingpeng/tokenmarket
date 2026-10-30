import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// UserStatsOverview 用户调用概览。
export type UserStatsOverview = {
    total_requests: number;
    success_rate: number;
    total_cost: number;
    total_tokens: number;
};

// UserStatsTrend 用户调用趋势数据点。
export type UserStatsTrend = {
    date: string;
    requests: number;
    cost: number;
    success_rate: number;
};

// UserStatsModel 用户按模型统计。
export type UserStatsModel = {
    model_name: string;
    requests: number;
    cost: number;
    tokens: number;
};

// UserStatsQuery 统计查询参数。
export type UserStatsQuery = {
    start_date?: string; // YYYY-MM-DD
    end_date?: string;   // YYYY-MM-DD
    granularity?: 'day' | 'week' | 'month';
};

// buildQueryString 构造查询字符串。
function buildQueryString(params: UserStatsQuery): string {
    const qs = new URLSearchParams();
    if (params.start_date) qs.set('start_date', params.start_date);
    if (params.end_date) qs.set('end_date', params.end_date);
    if (params.granularity) qs.set('granularity', params.granularity);
    return qs.toString();
}

// useUserStatsOverview 查询用户调用概览。
export function useUserStatsOverview(params: UserStatsQuery = {}) {
    const qs = buildQueryString(params);
    return useQuery({
        queryKey: ['user-stats', 'overview', params],
        queryFn: () => apiRequest<UserStatsOverview>(`/user-stats/overview?${qs}`),
    });
}

// useUserStatsTrend 查询用户调用趋势。
export function useUserStatsTrend(params: UserStatsQuery = {}) {
    const qs = buildQueryString(params);
    return useQuery({
        queryKey: ['user-stats', 'trend', params],
        queryFn: () => apiRequest<UserStatsTrend[]>(`/user-stats/trend?${qs}`),
    });
}

// useUserStatsModels 查询用户按模型统计。
export function useUserStatsModels(params: UserStatsQuery = {}) {
    const qs = buildQueryString(params);
    return useQuery({
        queryKey: ['user-stats', 'models', params],
        queryFn: () => apiRequest<UserStatsModel[]>(`/user-stats/models?${qs}`),
    });
}
