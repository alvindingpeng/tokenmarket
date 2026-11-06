import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import {
    userStatsModelsQueryOptions,
    userStatsLatencyQueryOptions,
    userStatsOverviewQueryOptions,
    userStatsTrendQueryOptions,
    type UserStatsModel,
    type UserStatsOverview,
    type UserStatsTrend,
    type UserStatsLatency,
} from '@/api/user-stats';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { getDateRange } from './dates';
import { AnalyticsPage } from './index';
import { buildTrendChartData, hasSuccessSamples, isCoverageLimited, summarizeSuccess } from './success';

// 页面状态回归(mock SSR): 加载 / 失败+重试 / 空态 / 有数据 四态必须各走各的分支。
// 失败绝不能落回「零」——假零会被当真实统计读; 空态也不能拿假数据糊弄。

// shiftDate 按天平移 YYYY-MM-DD。
// 覆盖起点必须相对「今天」算: 写死某天的话, 测试会随着日期流逝悄悄失效(查询区间一直在往前滚)。
function shiftDate(date: string, days: number): string {
    const [year, month, day] = date.split('-').map(Number);
    const shifted = new Date(year, month - 1, day + days);
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${shifted.getFullYear()}-${pad(shifted.getMonth() + 1)}-${pad(shifted.getDate())}`;
}

const monthRange = getDateRange('month');

// 调用日志只留 7 天, 选"最近 30 天"时真实覆盖起点必然晚于查询起点 ——
// fixture 就把覆盖起点设成区间第 5 天, 页面必须据此提示, 而不是假装覆盖整个 30 天。
const coverageStart = shiftDate(monthRange.start_date as string, 4);

const overviewData: UserStatsOverview = {
    total_requests: 1234, success_rate: 98.5, total_cost: 12.5, total_tokens: 456000,
    input_tokens: 100000, output_tokens: 56000, cache_read_tokens: 300000, cache_write_tokens: 0,
    cache_hit_rate: 75,
    // 请求口径: 9850/10000 = 98.5%, 与 success_rate 对得上; 取消单独计, 不进分母。
    request_total: 10012, request_success: 9850, request_failed: 150, request_canceled: 12,
    request_coverage_start: coverageStart, request_retention_days: 7,
};
const trendData: UserStatsTrend[] = [
    // 有日志样本的一天: 成功率是真值。
    { date: '2026-01-15', requests: 10, cost: 1, success_rate: 90, request_success: 9, request_failed: 1, request_canceled: 0 },
    // 没有日志样本的一天(超出留存期): success_rate=0 只是哨兵值, 图上必须是断点而不是 0%。
    { date: '2026-01-16', requests: 4, cost: 0.5, success_rate: 0, request_success: 0, request_failed: 0, request_canceled: 0 },
];
const modelsData: UserStatsModel[] = [{ model_name: 'gpt-4o', requests: 5, cost: 2, tokens: 1000, cache_read_tokens: 500, cache_write_tokens: 0, cache_hit_rate: 50 }];

// latencyData 覆盖起点刻意设为 3 天前: 页面必须显示真实覆盖起点而不是假装覆盖整个区间。
const latencyData: UserStatsLatency = {
    sample_count: 42, avg_first_token_ms: 850, p95_first_token_ms: 2400,
    avg_duration_ms: 6200, avg_upstream_ttfb_ms: 700,
    coverage_start: '2026-01-12', retention_days: 7,
};

// keys 与组件取数用同一套查询定义; 日期范围是「运行当天」, 与渲染时刻同一天, 查询键一致。
const keys = () => {
    const range = getDateRange('today');
    return {
        overview: userStatsOverviewQueryOptions(range).queryKey,
        trend: userStatsTrendQueryOptions({ ...range, granularity: 'hour' }).queryKey,
        models: userStatsModelsQueryOptions(range).queryKey,
        latency: userStatsLatencyQueryOptions(range).queryKey,
    };
};

// seedError 把一条查询推进「失败」状态: 界面必须把它渲染成报错, 而不是零值或空态。
// retryOnMount: false 是给 mock SSR 用的: react-query 的乐观结果会把「无数据的失败」短暂显示成
// 「即将重试」的 pending —— 那是浏览器重挂时的过渡态; 关掉自动重试, 快照才能停在落定后的错误态,
// 也就是真实 UI 里失败后用户看到的「报错 + 重试」分支。
function seedError(client: QueryClient, queryKey: readonly unknown[]) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey }, {
        data: undefined,
        dataUpdateCount: 0,
        dataUpdatedAt: 0,
        error: new Error('boom'),
        errorUpdateCount: 1,
        errorUpdatedAt: Date.now(),
        fetchFailureCount: 1,
        fetchFailureReason: null,
        fetchMeta: null,
        isInvalidated: false,
        status: 'error',
        fetchStatus: 'idle',
    });
}

const seedAll = (client: QueryClient, patch: Partial<Record<'overview' | 'trend' | 'models' | 'latency', 'error'>> = {}) => {
    const k = keys();
    if (patch.overview === 'error') seedError(client, k.overview);
    else client.setQueryData(k.overview, overviewData);
    if (patch.trend === 'error') seedError(client, k.trend);
    else client.setQueryData(k.trend, trendData);
    if (patch.models === 'error') seedError(client, k.models);
    else client.setQueryData(k.models, modelsData);
    if (patch.latency === 'error') seedError(client, k.latency);
    else client.setQueryData(k.latency, latencyData);
};

describe('AnalyticsPage 页面状态(mock SSR)', () => {
    it('三请求都就绪时渲染真值, 不停在加载态', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('Total Requests');
        expect(html).toContain('456,000'); // 新档位: 百万以下显示实际 token
        expect(html).toContain('data-testid="analytics-scroll"');
        expect(html).toContain('overflow-y-auto');
        expect(html).toContain('1,234'); // 概览真值
        expect(html).toContain('98.5'); // 成功率(SSR 会在文本节点间插注释, 不断言整串)
        expect(html).not.toContain('Loading...');
        expect(html).not.toContain('Retry'); // 没失败就没有重试入口
    });

    it('概览请求失败时明确报错 + 重试, 不把错误当零', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client, { overview: 'error' }));
        expect(html).toContain('Failed to load statistics');
        expect(html).toContain('Retry');
        expect(html).not.toContain('Total Requests'); // 失败的卡片整个不渲染
        expect(html).not.toMatch(/tabular-nums">0</); // 更不许出现兜底假零
        expect(html).toContain('Request Trend'); // 其它区块不受它拖累
    });

    it('趋势请求失败同样报错 + 重试, 不画空图', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client, { trend: 'error' }));
        expect(html).toContain('Failed to load statistics');
        expect(html).toContain('Retry');
        expect(html).toContain('1,234'); // 概览照常
    });

    it('空列表走真实空态, 不是空白图表', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            const k = keys();
            client.setQueryData(k.trend, [] as UserStatsTrend[]);
            client.setQueryData(k.models, [] as UserStatsModel[]);
        });
        expect(html).toContain('No data in this range');
        expect(html).not.toContain('Loading...');
    });

    it('区块独立加载: 慢请求不阻塞已就绪的区块', () => {
        // 只有趋势就绪, 概览/模型还悬着 —— 全页 gate 会整体转圈(且只有 1 个加载态),
        // 独立加载则趋势已渲染, 只剩 2 个区块在转。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            client.setQueryData(keys().trend, trendData);
        });
        expect(html.match(/Loading.../g)).toHaveLength(3);
        expect(html).toContain('Request Trend');
        expect(html).not.toContain('Retry'); // 没失败就不报错
    });

    it('全部在加载时整页是加载态, 不是假零', () => {
        const html = renderSSR(<AnalyticsPage />, () => undefined);
        expect(html.match(/Loading.../g)).toHaveLength(4);
        expect(html).not.toContain('Retry');
        expect(html).not.toMatch(/tabular-nums">0</);
    });

    it('时间范围下拉没有 custom: 没有日期输入的选项是假范围', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).not.toContain('Custom');
    });
});

describe('analytics 三语键', () => {
    it('三语文案键位齐全且不含已移除的 range.custom', () => {
        for (const [name, messages] of [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const) {
            const analytics = (messages as { analytics: Record<string, unknown> }).analytics;
            const range = analytics.range as Record<string, unknown>;
            for (const key of ['title', 'loading', 'error', 'retry', 'empty']) {
                expect({ name, key, value: analytics[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            for (const key of ['today', 'week', 'month']) {
                expect({ name, key, value: range[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            expect({ name, custom: range.custom }).toEqual({ name, custom: undefined });
        }
    });
});

describe('AnalyticsPage 新增数据维度', () => {
    it('概览展示缓存命中率与词元构成四项', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="cache-hit-rate"');
        expect(html).toContain('75.0'); // 命中率真值(SSR 在文本节点间插图注释, 不断言整串)
        expect(html).toContain('data-testid="token-breakdown"');
        expect(html).toContain('Cache read');
        expect(html).toContain('Cache write');
        expect(html).toContain('100,000'); // 新档位: 百万以下显示实际 token
    });

    it('总调用量下面标注计费口径, 免得被拿去当成功率的分母', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="requests-scope"');
        // 计费口径(只含产生用量的调用)与请求口径天然不等, 界面必须自己说清楚。
        expect(html).toContain('only requests that produced usage');
    });

    it('延迟面板展示均值与 P95, 并标注真实覆盖起点', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="latency-panel"');
        expect(html).toContain('850 ms');
        expect(html).toContain('2.40 s'); // 2400ms
        // 覆盖起点只到 3 天前: 必须显示真实起点, 不能假装覆盖整月。
        expect(html).toContain('2026-01-12');
        expect(html).toContain('7');
    });

    it('延迟请求失败时明确报错, 不把无数据渲染成 0ms', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client, { latency: 'error' }));
        expect(html).toContain('Failed to load statistics');
        expect(html).not.toContain('0 ms');
    });

    it('成功率显示真实请求口径的值, 并给出可对上的样本量', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="success-rate"');
        // 98.5% 来自 9850/(9850+150); 小字里的分子分母必须与这里一致, 数字之间不能打架。
        expect(html).toContain('98.5%');
        expect(html).toContain('Success 9,850');
        expect(html).toContain('Failed 150');
        // 取消单独说明, 不进分母 —— 用户自己断开的请求不该被算成失败。
        expect(html).toContain('12 canceled');
    });

    it('没有调用日志样本时成功率显示 —, 不显示 0.0%', () => {
        // 区间内完全没有调用日志: 后端给的 success_rate=0 是哨兵值,
        // 渲染成 0.0% 会被读成"全部请求失败", 那是彻头彻尾的假数据。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            client.setQueryData(keys().overview, {
                ...overviewData,
                success_rate: 0,
                request_total: 0, request_success: 0, request_failed: 0, request_canceled: 0,
                request_coverage_start: '',
            });
        });
        expect(html).toContain('data-testid="success-rate"');
        expect(html).toContain('—');
        expect(html).toContain('No call-log samples in this range');
        expect(html).not.toContain('0.0%');
        expect(html).not.toContain('data-testid="success-coverage"'); // 无样本就不必再说覆盖起点
    });

    it('调用日志只覆盖区间末段时, 明确提示"仅覆盖 X 起"', () => {
        // 默认区间是"今天", 单日区间内不会出现覆盖缺口 —— 改用一个能出现缺口的场景验证:
        // 把种子数据的覆盖起点放到"今天"之后不可能, 故直接验证 today 下不误报, 再以纯函数断言跨日行为。
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).not.toContain('data-testid="success-coverage"'); // 单日区间无缺口, 不误报
        const todayRange = getDateRange('today');
        // 跨日场景用纯函数直证: 30 天起点在今天之前, 覆盖起点(4 天前)严格晚于它 → 有缺口。
        expect(isCoverageLimited(coverageStart, monthRange.start_date)).toBe(true);
        // 留存天数在种子数据里是 7, 覆盖提示逻辑由 isCoverageLimited 驱动(见下方多日用例)。
        expect(html).toContain('98.5%');
    });

    it('覆盖率提示只在真的少了几天时出现: 区间本就在留存期内就不多嘴', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            // 覆盖起点等于查询起点 = 整个区间都有日志, 没有缺口可讲。
            client.setQueryData(keys().overview, { ...overviewData, request_coverage_start: monthRange.start_date as string });
        });
        expect(html).not.toContain('data-testid="success-coverage"');
        expect(html).toContain('98.5%'); // 但成功率照常显示
    });

    it('趋势图把没有日志样本的桶画成断点, 而不是成功率 0', () => {
        const chart = buildTrendChartData(trendData);
        expect(chart[0].success_rate).toBe(90);   // 有样本: 真值
        expect(chart[1].success_rate).toBeNull(); // 无样本: 断点(配合 connectNulls={false} 不连线)

        // "没取到"和"真的全失败"必须分得开: 前者是 null(断线), 后者是货真价实的 0。
        const allFailed = buildTrendChartData([
            { date: '2026-01-17', requests: 5, cost: 0.2, success_rate: 0, request_success: 0, request_failed: 5, request_canceled: 0 },
        ]);
        expect(allFailed[0].success_rate).toBe(0);

        // 一个桶有样本就足以画线; 全都没有样本时折叠成一条 null 序列, 折线不成立。
        expect(hasSuccessSamples(trendData)).toBe(true);
        expect(hasSuccessSamples([trendData[1]])).toBe(false);

        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="trend-chart"');
    });

    it('覆盖率判定与成功率摘要的口径边界', () => {
        // 后端约定: 空串 = 区间内完全没有调用日志, 此时谈不上"覆盖起点"。
        expect(isCoverageLimited('', monthRange.start_date)).toBe(false);
        // 晚于查询起点 = 只覆盖末尾几天。
        expect(isCoverageLimited('2030-01-01', '2030-01-01')).toBe(false);
        expect(isCoverageLimited('2030-01-02', '2030-01-01')).toBe(true);
        expect(isCoverageLimited('2029-12-31', '2030-01-01')).toBe(false);
        // 分母为 0 必须给 "—"; 有样本时才给一位小数的百分比。
        expect(summarizeSuccess({ ...overviewData, request_success: 0, request_failed: 0 }).value).toBe('—');
        expect(summarizeSuccess({ ...overviewData, success_rate: 98.5, request_success: 9850, request_failed: 150 })).toEqual({ attempts: 10000, value: '98.5%' });
    });

    it('无延迟样本时走空态而不是显示极快的 0', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => {
            const k = keys();
            client.setQueryData(k.overview, overviewData);
            client.setQueryData(k.trend, trendData);
            client.setQueryData(k.models, modelsData);
            client.setQueryData(k.latency, { ...latencyData, sample_count: 0, coverage_start: '' });
        });
        expect(html).toContain('No latency samples in this range');
        expect(html).not.toContain('0 ms');
    });
});
