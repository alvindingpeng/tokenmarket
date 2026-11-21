import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { apiKeyDashboardStatsQueryOptions, apiKeyListQueryOptions } from './queries';
import { useAuthStore } from './user';
import { StatsAPIKey, StatsAPIKeyFormatted } from './stats';
import type { APIKeyCreatePayload, APIKeyUpdatePayload } from '@/lib/apikey';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';
import { POLL_SLOW } from '@/lib/polling';

/**
 * API Key 数据
 */
export interface APIKey {
    id: number;
    name: string;
    /**
     * 明文密钥: 只在创建响应里出现一次, 此后任何接口都不返回。
     * 列表与详情只给 key_prefix / key_last4 两段掩码。
     */
    api_key?: string;
    key_prefix: string;
    key_last4: string;
    enabled: boolean;
    expire_at?: number; // Unix 时间戳（秒），不传表示永不过期
    max_cost?: number; // 不传表示无限制
    supported_models: string[]; // 允许访问的分组名称，空数组表示不限制
    // 选路偏好：inherit 跟随模型分组；其余为自动选路策略（只对可用成员排序，手动固定成员优先）。
    routing_preference?: 'inherit' | 'balanced' | 'price' | 'latency' | 'success';
    // 配额限制：0 表示不限制，按每日重置。编辑时必须原样回传，否则更新接口会把它们清零。
    quota_requests?: number;
    quota_input_tokens?: number;
    quota_output_tokens?: number;
    quota_reset_hour?: number;
}

/**
 * API Key Stats 响应（包含 stats 和 info）
 */
export interface APIKeyStatsResponse {
    stats: StatsAPIKey;
    info: APIKey;
}

interface APIKeyStatsResponseFormatted {
    stats: StatsAPIKeyFormatted;
    info: APIKey;
}

/**
 * API Key 登录 Hook（仅校验 key 是否有效）
 */
export function useAPIKeyLogin() {
    const { setAPIKeyAuth, logout } = useAuthStore();

    return useMutation({
        mutationFn: async (apiKey: string) => {
            setAPIKeyAuth(apiKey);
            await apiRequest<null>('/api/v1/apikey/login', { dispatchUnauthorized: false });
            return apiKey;
        },
        onError: () => {
            logout();
        },
    });
}

/**
 * 获取当前 API Key 的详细统计数据 Hook（仅 API Key 登录用户使用）
 */
export function useAPIKeyDashboardStats() {
    const { isAPIKeyAuth, isAuthenticated } = useAuthStore();

    return useQuery({
        ...apiKeyDashboardStatsQueryOptions,
        select: (data): APIKeyStatsResponseFormatted => ({
            stats: {
                api_key_id: data.stats.api_key_id,
                input_token: formatCount(data.stats.input_token),
                output_token: formatCount(data.stats.output_token),
                total_token: formatCount(data.stats.input_token + data.stats.output_token),
                input_cost: formatMoney(data.stats.input_cost),
                output_cost: formatMoney(data.stats.output_cost),
                total_cost: formatMoney(data.stats.input_cost + data.stats.output_cost),
                wait_time: formatTime(data.stats.wait_time),
                request_success: formatCount(data.stats.request_success),
                request_failed: formatCount(data.stats.request_failed),
                request_count: formatCount(data.stats.request_success + data.stats.request_failed),
            },
            info: data.info,
        }),
        enabled: isAPIKeyAuth && isAuthenticated,
        refetchInterval: POLL_SLOW,
    });
}

// APIKeyUsageTotals 是一段窗口内的累计用量(后端 /api/v1/apikey/usage 的 total/recent/日桶共用同一形状)。
// 口径必须如实呈现, 不要在界面上抹平:
// - 词元与请求数含已退费行(退费后用量仍是发生过的调用, 抹掉会与账单对不上);
// - user_cost 是未退费部分, 即用户实付;
// - 退费单独由 refunded_records / refunded_user_cost 给出, 界面另行提示。
export type APIKeyUsageTotals = {
    input_token: number;
    output_token: number;
    cache_read_token: number;
    cache_write_token: number;
    request_count: number;
    user_cost: number;
    refunded_records: number;
    refunded_user_cost: number;
};

// APIKeyUsageDay 是一个日桶: 日期(YYYYMMDD)加当日累计用量。
export type APIKeyUsageDay = APIKeyUsageTotals & { date: string };

// APIKeyUsageReport 是该 Key 的持久化用量报表。
// coverage_start 为空串表示窗口内完全没有数据 —— 这是"无数据", 与"用量为 0"不是一回事,
// 界面必须区分, 否则用户会把自己一分钱没花当成事实。
export type APIKeyUsageReport = {
    api_key_id: number;
    from: string; // 实际窗口起点 YYYY-MM-DD
    to: string; // 实际窗口终点(含当天)
    coverage_start: string; // 最早有数据的日期; 空串 = 区间内无数据
    retention_days: number;
    total: APIKeyUsageTotals; // 本次窗口(默认终身)累计
    recent: APIKeyUsageTotals; // 最近 30 天累计
    recent_from: string;
    days: APIKeyUsageDay[]; // 日桶升序, 可能为空数组
};

// apiKeyUsageQuery 拼出用量接口的查询串; 抽成纯函数是为了让 vitest 直接断言拼法(不必渲染 hook)。
// 非法 id(<=0 或非整数)返回空串: 后端会按 0 查不到任何 Key, 前端不该发这种请求。
export function apiKeyUsageQuery(apiKeyID: number, from?: string, to?: string): string {
    if (!Number.isInteger(apiKeyID) || apiKeyID <= 0) return '';
    const query = new URLSearchParams({ api_key_id: String(apiKeyID) });
    // from/to 是可选的窗口限定; 空串等于不限, 不能拼成 from= 让后端收到空日期。
    if (from) query.set('from', from);
    if (to) query.set('to', to);
    return query.toString();
}

// apiKeyUsageTokens 四类词元之和, 与后端的四个字段一一对应(不用 total_token: 接口没给这个字段)。
export function apiKeyUsageTokens(totals: APIKeyUsageTotals): number {
    return totals.input_token + totals.output_token + totals.cache_read_token + totals.cache_write_token;
}

// useAPIKeyUsage 拉取该 Key 的持久化用量报表。
// 刷新节奏与 useStatsAPIKey 同档(POLL_SLOW): 两者是同一张卡片上的两组数字,
// 一个 30 秒刷、另一个更慢会让界面自相矛盾。
export function useAPIKeyUsage(apiKeyID: number, opts: { from?: string; to?: string } = {}) {
    const from = opts.from ?? '';
    const to = opts.to ?? '';
    const query = apiKeyUsageQuery(apiKeyID, from, to);
    return useQuery({
        queryKey: ['apikey', 'usage', apiKeyID, from, to],
        queryFn: () => apiRequest<APIKeyUsageReport>('/api/v1/apikey/usage?' + query),
        enabled: query !== '',
        refetchInterval: POLL_SLOW,
        refetchOnMount: 'always',
    });
}

/**
 * 创建 API Key 请求: 密钥值由服务端生成, 一次性返回; 提交载荷不带任何密钥材料。
 */
type CreateAPIKeyRequest = APIKeyCreatePayload;

/**
 * 更新 API Key 请求: Key 本身不可更新 —— 旧 Key 原样保留, 权限与额度不受影响。
 */
type UpdateAPIKeyRequest = APIKeyUpdatePayload;

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
        ...apiKeyListQueryOptions,
        refetchInterval: POLL_SLOW,
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
        gcTime: 0, // one-time plaintext must not survive as an unused mutation cache entry
        mutationFn: (data: CreateAPIKeyRequest) =>
            apiRequest<APIKey>('/api/v1/apikey/create', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: apiKeyListQueryOptions.queryKey }),
    });
}

/**
 * 更新 API Key Hook
 * 
 * @example
 * const updateAPIKey = useUpdateAPIKey();
 * 
 * updateAPIKey.mutate({
 *   id: 1,
 *   name: 'Updated API Key',
 *   enabled: false,
 * });
 */
export function useUpdateAPIKey() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: UpdateAPIKeyRequest) =>
            apiRequest<APIKey>('/api/v1/apikey/update', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: apiKeyListQueryOptions.queryKey }),
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
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/apikey/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: apiKeyListQueryOptions.queryKey }),
    });
}
