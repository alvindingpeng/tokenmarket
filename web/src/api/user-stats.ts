import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { useAuthStore } from './user';

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

// userStatsOverviewPath / userStatsTrendPath / userStatsModelsPath 服务端路由挂在 /api/v1/user-stats 组下
// (internal/server/handlers/user_stats.go), 前缀缺一不可 —— 少了 /api/v1 就是 404。
export const userStatsOverviewPath = '/api/v1/user-stats/overview';
export const userStatsTrendPath = '/api/v1/user-stats/trend';
export const userStatsModelsPath = '/api/v1/user-stats/models';

// userStatsOverviewQueryOptions 用户调用概览查询定义。
// queryFn 接 react-query 的 signal: 组件卸载/切范围时正在飞的请求立刻中止, 不白等。
export function userStatsOverviewQueryOptions(params: UserStatsQuery = {}, userId = 0) {
    const qs = buildQueryString(params);
    return queryOptions({
        queryKey: ['user-stats', 'overview', params, userId] as const,
        queryFn: ({ signal }) => apiRequest<UserStatsOverview>(`${userStatsOverviewPath}?${qs}`, { signal }),
        retry: apiQueryRetry,
    });
}

// userStatsTrendQueryOptions 用户调用趋势查询定义。
export function userStatsTrendQueryOptions(params: UserStatsQuery = {}, userId = 0) {
    const qs = buildQueryString(params);
    return queryOptions({
        queryKey: ['user-stats', 'trend', params, userId] as const,
        queryFn: ({ signal }) => apiRequest<UserStatsTrend[]>(`${userStatsTrendPath}?${qs}`, { signal }),
        retry: apiQueryRetry,
    });
}

// userStatsModelsQueryOptions 用户按模型统计查询定义。
export function userStatsModelsQueryOptions(params: UserStatsQuery = {}, userId = 0) {
    const qs = buildQueryString(params);
    return queryOptions({
        queryKey: ['user-stats', 'models', params, userId] as const,
        queryFn: ({ signal }) => apiRequest<UserStatsModel[]>(`${userStatsModelsPath}?${qs}`, { signal }),
        retry: apiQueryRetry,
    });
}

// useUserStatsOverview 查询用户调用概览。
export function useUserStatsOverview(params: UserStatsQuery = {}) {
    const userId = useAuthStore((state) => state.userId);
    return useQuery(userStatsOverviewQueryOptions(params, userId));
}

// useUserStatsTrend 查询用户调用趋势。
export function useUserStatsTrend(params: UserStatsQuery = {}) {
    const userId = useAuthStore((state) => state.userId);
    return useQuery(userStatsTrendQueryOptions(params, userId));
}

// useUserStatsModels 查询用户按模型统计。
export function useUserStatsModels(params: UserStatsQuery = {}) {
    const userId = useAuthStore((state) => state.userId);
    return useQuery(userStatsModelsQueryOptions(params, userId));
}
