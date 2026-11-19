import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';
import { statsDailyQueryOptions, statsHomeQueryOptions, statsHourlyQueryOptions, statsTotalQueryOptions } from './queries';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';
import { POLL_HOUR, POLL_REALTIME, POLL_SLOW } from '@/lib/polling';

/**
 * 统计数据
 */
export interface StatsMetrics {
    /** 毛输入词元(prompt, 含缓存读/写); 总词元 = 输入 + 输出, 与上游 total_tokens 对齐 */
    input_token: number;
    output_token: number;
    input_cost: number;
    output_cost: number;
    wait_time: number;
    request_success: number;
    request_failed: number;
}

export interface StatsMetricsFormatted {
    input_token: ReturnType<typeof formatCount>;
    output_token: ReturnType<typeof formatCount>;
    input_cost: ReturnType<typeof formatMoney>;
    output_cost: ReturnType<typeof formatMoney>;
    wait_time: ReturnType<typeof formatTime>;
    request_success: ReturnType<typeof formatCount>;
    request_failed: ReturnType<typeof formatCount>;

    request_count: ReturnType<typeof formatCount>;
    total_token: ReturnType<typeof formatCount>;
    total_cost: ReturnType<typeof formatMoney>;
}

// formatStatsMetrics 把一组累计统计格式化为界面展示字段，并补齐合计项。
// 渠道、渠道模型和渠道凭据各自维护独立计数，均按同一口径展示。
export function formatStatsMetrics(metrics: StatsMetrics): StatsMetricsFormatted {
    return {
        input_token: formatCount(metrics.input_token),
        output_token: formatCount(metrics.output_token),
        total_token: formatCount(metrics.input_token + metrics.output_token),
        input_cost: formatMoney(metrics.input_cost),
        output_cost: formatMoney(metrics.output_cost),
        total_cost: formatMoney(metrics.input_cost + metrics.output_cost),
        wait_time: formatTime(metrics.wait_time),
        request_success: formatCount(metrics.request_success),
        request_failed: formatCount(metrics.request_failed),
        request_count: formatCount(metrics.request_success + metrics.request_failed),
    };
}

export interface StatsDaily extends StatsMetrics {
    date: string;
}
export interface StatsDailyResponse {
    max_request_count: number;
    items: StatsDaily[];
}
export interface StatsDailyFormatted extends StatsMetricsFormatted {
    date: string;
}
interface StatsDailyFormattedResponse {
    max_request_count: number;
    items: StatsDailyFormatted[];
}

export interface StatsTotal extends StatsMetrics {
    id: number;
}
type StatsTotalFormatted = StatsMetricsFormatted;

export interface StatsHourly extends StatsMetrics {
    hour: number;
    date: string;
}
interface StatsHourlyFormatted extends StatsMetricsFormatted {
    hour: number;
    date: string;
}

/**
 * 首页聚合统计: 一次请求取回首屏所需的累计, 每日, 每小时与榜单数据
 */
// StatsHomeRankMetrics 是榜单所需的精简指标: 合计项由后端算好, 前端直接格式化。
export interface StatsHomeRankMetrics {
    total_cost: number;
    total_token: number;
    request_count: number;
    request_success: number;
    request_failed: number;
}

// StatsHomeModel 榜单里的单个渠道模型; 同一模型可能出现在多个渠道下, 前端按名称聚合。
export interface StatsHomeModel extends StatsHomeRankMetrics {
    id: number;
    name: string;
    channelName: string;
}

// StatsHomeChannel 榜单里的单个渠道, 连同该渠道各模型的精简统计。
export interface StatsHomeChannel extends StatsHomeRankMetrics {
    id: number;
    name: string;
    channelName: string;
    models: StatsHomeModel[];
}

export interface StatsHome {
    total: StatsTotal;
    daily: StatsDailyResponse;
    hourly: StatsHourly[];
    channels: StatsHomeChannel[];
}

// 榜单条目的展示形状, 榜单组件直接消费, 不再自行格式化。
export interface StatsHomeModelFormatted extends StatsHomeRankFormatted {
    id: number;
    name: string;
    channelName: string;
}

export interface StatsHomeChannelFormatted extends StatsHomeModelFormatted {
    models: StatsHomeModelFormatted[];
}

// StatsHomeRankFormatted 是榜单条目的展示形状: 与 RankMetrics 同构, 由精简指标格式化而来。
export interface StatsHomeRankFormatted {
    total_cost: ReturnType<typeof formatMoney>;
    total_token: ReturnType<typeof formatCount>;
    request_count: ReturnType<typeof formatCount>;
    request_success: ReturnType<typeof formatCount>;
    request_failed: ReturnType<typeof formatCount>;
}

// formatStatsHomeRank 把精简指标格式化成榜单展示字段。
// 合计项已在后端按 formatStatsMetrics 的口径算好, 这里只格式化, 不再做任何求和:
// 两处各算一套必然漂移, 榜单的排序(raw)与展示(formatted)必须来自同一份数值。
export function formatStatsHomeRank(metrics: StatsHomeRankMetrics): StatsHomeRankFormatted {
    return {
        total_cost: formatMoney(metrics.total_cost),
        total_token: formatCount(metrics.total_token),
        request_count: formatCount(metrics.request_count),
        request_success: formatCount(metrics.request_success),
        request_failed: formatCount(metrics.request_failed),
    };
}

// StatsDailyFormattedResponse / StatsHourlyFormatted 的聚合形式, 供首页组件直接消费。
export interface StatsHomeFormatted {
    total: StatsMetricsFormatted;
    daily: StatsDailyFormattedResponse;
    hourly: StatsHourlyFormatted[];
    channels: StatsHomeChannelFormatted[];
}
/**
 * API Key 统计数据
 */
export interface StatsAPIKey extends StatsMetrics {
    api_key_id: number;
}

export interface StatsAPIKeyFormatted extends StatsMetricsFormatted {
    api_key_id: number;
}

// statsDailyFormattedQueryOptions 统一首页每日统计查询、格式化和刷新策略。
const statsDailyFormattedQueryOptions = queryOptions({
    ...statsDailyQueryOptions,
    select: (data): StatsDailyFormattedResponse => ({
        max_request_count: data.max_request_count,
        items: data.items.map((item): StatsDailyFormatted => ({
            ...formatStatsMetrics(item),
            date: item.date,
        })),
    }),
    refetchInterval: POLL_HOUR, // 1 小时
    refetchOnMount: 'always',
});

/**
 * 获取每日统计数据 Hook
 */
export function useStatsDaily() {
    const query = useQuery(statsDailyFormattedQueryOptions);
    return {
        ...query,
        data: query.data?.items,
        maxRequestCount: query.data?.max_request_count ?? 0,
    };
}

// statsHourlyFormattedQueryOptions 统一首页每小时统计查询、格式化和刷新策略。
const statsHourlyFormattedQueryOptions = queryOptions({
    ...statsHourlyQueryOptions,
    select: (data) => data.map((item): StatsHourlyFormatted => ({
        ...formatStatsMetrics(item),
        hour: item.hour,
        date: item.date,
    })),
    refetchInterval: POLL_REALTIME,// 10 秒
    refetchOnMount: 'always',
});

/**
 * 获取每小时统计数据 Hook
 */
export function useStatsHourly() {
    return useQuery(statsHourlyFormattedQueryOptions);
}

// statsTotalFormattedQueryOptions 统一首页总统计查询、格式化和刷新策略。
const statsTotalFormattedQueryOptions = queryOptions({
    ...statsTotalQueryOptions,
    select: (data): StatsTotalFormatted => formatStatsMetrics(data),
    refetchInterval: POLL_REALTIME,// 10 秒
    refetchOnMount: 'always',
});

/**
 * 获取总统计数据 Hook
 */
export function useStatsTotal() {
    return useQuery(statsTotalFormattedQueryOptions);
}

// formatStatsHome 把聚合响应就地格式化成各组件原本的形状, 组件只换数据来源, 展示逻辑一行不改。
// 单独导出成纯函数是为了能被单测直接覆盖: hook 的 select 就是它本身, 测到的就是线上跑的那段。
export function formatStatsHome(data: StatsHome): StatsHomeFormatted {
    return {
        total: formatStatsMetrics(data.total),
        daily: {
            max_request_count: data.daily.max_request_count,
            items: data.daily.items.map((item): StatsDailyFormatted => ({
                ...formatStatsMetrics(item),
                date: item.date,
            })),
        },
        hourly: data.hourly.map((item): StatsHourlyFormatted => ({
            ...formatStatsMetrics(item),
            hour: item.hour,
            date: item.date,
        })),
        channels: data.channels.map((channel): StatsHomeChannelFormatted => ({
            id: channel.id,
            name: channel.name,
            channelName: channel.channelName,
            ...formatStatsHomeRank(channel),
            models: channel.models.map((channelModel): StatsHomeModelFormatted => ({
                id: channelModel.id,
                name: channelModel.name,
                channelName: channelModel.channelName,
                ...formatStatsHomeRank(channelModel),
            })),
        })),
    };
}

// statsHomeFormattedQueryOptions 统一首页聚合查询、格式化与刷新策略。
const statsHomeFormattedQueryOptions = queryOptions({
    ...statsHomeQueryOptions,
    select: formatStatsHome,
    // 与原先首屏最高的刷新频率一致: 实时指标仍按 10 秒更新, 日曲线也随之刷新。
    refetchInterval: POLL_REALTIME, // 10 秒
    refetchOnMount: 'always',
});

/**
 * 获取首页聚合统计数据 Hook
 */
export function useStatsHome() {
    return useQuery(statsHomeFormattedQueryOptions);
}



/**
 * 获取 API Key 统计数据列表 Hook
 */
export function useStatsAPIKey() {
    return useQuery({
        queryKey: ['stats', 'apikey'],
        queryFn: () => apiRequest<StatsAPIKey[]>('/api/v1/stats/apikey'),
        select: (data) => data.map((item): StatsAPIKeyFormatted => ({
            ...formatStatsMetrics(item),
            api_key_id: item.api_key_id,
        })),
        refetchInterval: POLL_SLOW,
        refetchOnMount: 'always',
    });
}
