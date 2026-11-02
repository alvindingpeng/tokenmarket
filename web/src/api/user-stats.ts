import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { useAuthStore } from './user';

// UserStatsOverview 用户调用概览。
//
// 同一个响应里混了两个数据源, 口径不能相互推断(后端 internal/model/user_stats.go 同此约定):
//   - 计费口径(total_requests / total_cost / total_tokens)来自 billing_records, 永久保留;
//   - 请求口径(success_rate / request_*)来自调用日志 relay_requests, 按留存期滚动删除。
// 失败的调用不产生计费行, 所以用计费行算成功率会恒等于 100% —— 只有调用日志能看见失败。
export type UserStatsOverview = {
    // 计费口径调用量(billing_records 行数, 含退费行), 不等于真实请求数, 真实请求数见 request_total。
    total_requests: number;
    // 真实请求成功率 % = request_success / (request_success + request_failed), 取消不计入分母。
    // 区间内没有调用日志时后端返回 0: 那是"无数据"的哨兵值, 页面必须显示"—"而不是 0.0%。
    success_rate: number;
    total_cost: number;
    total_tokens: number;
    // 请求口径计数(调用日志): 只有百分比没有样本量等于没有信息, 成功率必须能被这两个数解释。
    request_total: number;    // = success + failed + canceled
    request_success: number;
    request_failed: number;
    request_canceled: number; // 客户端取消, 不进成功率分母
    // 调用日志在查询区间内的真实覆盖起点 YYYY-MM-DD; 空串 = 区间内没有任何调用日志
    // (超出留存期, 或者确实没有调用)。早于查询起点说明成功率只覆盖末尾几天, 必须如实提示。
    request_coverage_start: string;
    request_retention_days: number;
    // 词元构成: 四项之和等于 total_tokens(后端同源查询保证)。
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    // cache_hit_rate = cacheRead/(cacheRead+input), 输出不计入分母。
    cache_hit_rate: number;
};

// UserStatsTrend 用户调用趋势数据点。
// 与概览同样分层: requests/cost 是计费口径, success_rate 与 request_* 是调用日志口径。
export type UserStatsTrend = {
    date: string;
    requests: number;
    cost: number;
    // 该桶的真实请求成功率 %; 桶内没有调用日志样本时后端返回 0 且三项计数均为 0 ——
    // 那是"无数据"的哨兵值, 画图时必须换成 null 断线, 否则会画成"成功率崩到 0"。
    success_rate: number;
    request_success: number;
    request_failed: number;
    request_canceled: number;
};

// UserStatsModel 用户按模型统计。
export type UserStatsModel = {
    model_name: string;
    requests: number;
    cost: number;
    tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    cache_hit_rate: number;
};

// UserStatsLatency 用户调用延迟指标。
// 数据源是调用日志(按留存期滚动删除), 因此 coverage_start 可能晚于查询起点:
// 页面必须据此提示真实覆盖范围, 不能把 7 天均值当成 30 天的结论。
export type UserStatsLatency = {
    sample_count: number;
    avg_first_token_ms: number;
    p95_first_token_ms: number;
    avg_duration_ms: number;
    avg_upstream_ttfb_ms: number;
    coverage_start: string;
    retention_days: number;
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
export const userStatsLatencyPath = '/api/v1/user-stats/latency';

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

// userStatsLatencyQueryOptions 用户调用延迟查询定义。
export function userStatsLatencyQueryOptions(params: UserStatsQuery = {}, userId = 0) {
    const qs = buildQueryString(params);
    return queryOptions({
        queryKey: ['user-stats', 'latency', params, userId] as const,
        queryFn: ({ signal }) => apiRequest<UserStatsLatency>(`${userStatsLatencyPath}?${qs}`, { signal }),
        retry: apiQueryRetry,
    });
}

// useUserStatsLatency 查询用户调用延迟指标。
export function useUserStatsLatency(params: UserStatsQuery = {}) {
    const userId = useAuthStore((state) => state.userId);
    return useQuery(userStatsLatencyQueryOptions(params, userId));
}
