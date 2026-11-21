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
    // 按模型拆分在区间内的覆盖起点 YYYY-MM-DD。两种空值语义完全不同(后端 internal/model/user_stats.go 同此约定):
    //   空串   = 区间内按模型完全没有数据;
    //   有日期 = 该日期之前「金额有, 但拆不到模型」(迁移 45 之前固化的日子没有 model 维度行)。
    // 后端恒下发该字段, 所以这里是必填; 但页面仍按「拿不到就不提示」处理 ——
    // 老后端没有这个字段时不许凭空编一条覆盖提示(见 analytics/modelCoverage.ts)。
    // 早于这条边界的金额仍在 total_cost 里, 只是拆不出模型: 绝不能补 0 或按比例摊, 那是编数字。
    model_breakdown_coverage_start: string;
    // 词元构成: 四项之和等于 total_tokens(后端同源查询保证)。
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    // cache_hit_rate = cacheRead/(cacheRead+input), 输出不计入分母。
    cache_hit_rate: number;

    // ---- 时区口径(task-58 新增) ----
    // 统计页里存在两个时区, 不能混为一谈:
    //   stats_timezone          —— 查询【区间边界】所用时区: 用户偏好生效时是该 IANA 名, 否则是服务器时区名。
    //   stats_calendar_timezone —— 服务器时区: 日/周/月【分组】与【数据留存】日历所依据的时区。
    // 两者名字可能不同而当前偏移相同(+08:00 的 Asia/Shanghai 与 Asia/Shanghai 之外的等价时区):
    // 挂钟一致时用户看到的日期与分组完全一致, 此时【不需要任何提示】—— 按名字比会误报。
    // 日/周/月桶恒按 stats_calendar_timezone, 不随区间时区变: 汇总行按服务器自然日固化,
    // 换一套日历会让统计页与账单页/留存口径对不上, 这是设计而非缺陷。
    //
    // 全部标成可选是刻意的, 不是偷懒: 后端 task-58 上线之前, 这个响应里确实没有这几个字段,
    // 而页面在那时也必须正常工作(走 legacy, 不提示)。把类型写成必填等于对运行时撒谎,
    // 还会逼着每一个测试 fixture 去填假值 —— 那才是真正的失真。
    // 读取方一律按 unknown 做运行时校验(见 analytics/timezone.ts), 拿不到就走 legacy。
    stats_timezone?: string;
    // 服务器时区名: 日/周/月【分组】与【数据留存】日历依据的时区。
    // 与 stats_timezone 可以不同名而同偏移 —— 那种情况挂钟一致, 不该提示。
    stats_calendar_timezone?: string;
    // 上面两个时区【当前】的 UTC 偏移(分钟, 东正西负)。判"是否需要提示"只比这两个数值:
    // 名字不参与判定(不同名可能同偏移), 偏移相同即用户看到的日期与分组完全一致。
    stats_timezone_offset_minutes?: number;
    stats_calendar_timezone_offset_minutes?: number;
    // stats_timezone 的来源: "user" = 用户偏好生效, "server" = 回落到服务器时区。
    stats_timezone_source?: 'user' | 'server';
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
    granularity?: 'hour' | 'day' | 'week' | 'month';
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
