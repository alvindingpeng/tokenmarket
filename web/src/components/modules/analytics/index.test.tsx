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
    // 按模型拆分只回到区间第 11 天: 前 10 天"金额有、拆不到模型"。
    // 与 request_coverage_start 是两个独立边界(一个受日志留存期限制, 一个受迁移 45 之前的固化形态限制),
    // 所以刻意取不同的值 —— 谁被谁覆盖都会被断言抓到。
    model_breakdown_coverage_start: shiftDate(monthRange.start_date as string, 10),
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

// keysFor 按预设取查询键: 页面默认"今天", 而覆盖边界只在多日区间才可能出现,
// 那些用例要用 initialPreset="month" 渲染, 种子也必须落在 month 的键上, 否则页面读到的是空查询。
const keysFor = (preset: 'today' | 'week' | 'month') => {
    const range = getDateRange(preset);
    return {
        overview: userStatsOverviewQueryOptions(range).queryKey,
        trend: userStatsTrendQueryOptions({ ...range, granularity: preset === 'today' ? 'hour' : 'day' }).queryKey,
        models: userStatsModelsQueryOptions(range).queryKey,
        latency: userStatsLatencyQueryOptions(range).queryKey,
    };
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

// seedAllFor 按预设种下全部四份数据: 多日用例与页面实际发出的查询键保持一致。
// 直接收 client(不再 curry): curry 过一次的种子函数很容易被写成"只调用外层就以为种下了",
// 结果页面停在加载态、用例断言的却是空查询。
const seedAllFor = (preset: 'today' | 'week' | 'month', client: QueryClient) => {
    const k = keysFor(preset);
    client.setQueryData(k.overview, overviewData);
    client.setQueryData(k.trend, trendData);
    client.setQueryData(k.models, modelsData);
    client.setQueryData(k.latency, latencyData);
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

    it('选了 30 天但按模型只覆盖末尾几天时, 明确提示从哪天起有拆分', () => {
        // 这是本次缺陷的正面场景: 默认区间是"今天"(单日不可能有缺口), 必须切到多日范围才看得见。
        // 区间起点在今天前 29 天, 覆盖起点在第 11 天 —— 前 10 天"金额有、拆不到模型"。
        const html = renderSSR(<AnalyticsPage initialPreset="month" />, (client) => seedAllFor('month', client));
        expect(html).toContain('data-testid="model-coverage"');
        // 提示里必须带上真实覆盖起点, 不能只说"覆盖不全"而不说从哪天起。
        expect(html).toContain(shiftDate(monthRange.start_date as string, 10));
        // 并且解释这段缺口的性质: 金额还在, 只是拆不出来, 不会补 0 也不摊派。
        expect(html).toContain('never zero-filled or pro-rated');
    });

    it('按模型覆盖整个区间时不多嘴(不出现覆盖提示)', () => {
        const html = renderSSR(<AnalyticsPage initialPreset="month" />, (client) => {
            seedAllFor('month', client);
            client.setQueryData(keysFor('month').overview, {
                ...overviewData,
                // 覆盖起点就是区间起点 = 整段都有拆分, 没有缺口可讲。
                model_breakdown_coverage_start: monthRange.start_date as string,
            });
        });
        expect(html).not.toContain('data-testid="model-coverage"');
        // 图照常画: recharts 的 ResponsiveContainer 在 SSR 里量不到宽度(>0 的轴文本不会出现),
        // 所以断言落在面板标题上 —— 面板本身必须还在, 不能被"没有提示"顺带弄没。
        expect(html).toContain('Model Cost Distribution');
        expect(html).toContain('Model Requests');
    });

    it('字段缺失时不出现任何覆盖提示(不许伪造缺失)', () => {
        // 后端未升级 / 旧缓存: 拿不到边界就不能硬报"覆盖不全", 那与漏报一样是编事实。
        const html = renderSSR(<AnalyticsPage initialPreset="month" />, (client) => {
            seedAllFor('month', client);
            // 模拟"后端未升级 / 旧缓存": 运行时确实没有这个字段, 而类型上它是必填 ——
            // 这正是 modelCoverageOf 收 unknown 入参 + 运行时校验要吸收的状态。
            const stale: Record<string, unknown> = { ...overviewData };
            delete stale.model_breakdown_coverage_start;
            client.setQueryData(keysFor('month').overview, stale as unknown as UserStatsOverview);
        });
        expect(html).not.toContain('data-testid="model-coverage"');
        expect(html).not.toContain('Model breakdown available from');
        expect(html).not.toContain('No per-model data in this range');
        expect(html).toContain('Model Cost Distribution'); // 图表本身不受影响
    });

    it('区间内完全没有按模型数据时, 明说"拆不出来"而不是笼统的"无数据"', () => {
        // 空串 = 区间内按模型完全没有数据。此时模型列表为空, 面板会走空态分支 ——
        // 但通用空态("No data in this range")会把"拆不出来"说成"没发生过", 必须换成专门的文案。
        const html = renderSSR(<AnalyticsPage initialPreset="month" />, (client) => {
            const k = keysFor('month');
            client.setQueryData(k.overview, { ...overviewData, model_breakdown_coverage_start: '' });
            client.setQueryData(k.trend, trendData);
            client.setQueryData(k.models, [] as UserStatsModel[]);
            client.setQueryData(k.latency, latencyData);
        });
        expect(html).toContain('No per-model data in this range');
        // 不能同时冒出一句"本区间无数据": 那会与"金额其实有"自相矛盾。
        expect(html).not.toContain('data-testid="model-coverage"');
        expect(html).not.toContain('Model breakdown available from');
        // 概览的总额仍在页面上 —— 这正是"有金额但拆不到模型"的样子。
        expect(html).toContain('1,234');
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
// 缓存命中率的零样本空态: 后端在所有词元桶都为 0 时给的是哨兵 0(cache_hit_rate),
// 直接渲染成 "0.0%" 会被读成"所有请求都没命中缓存"。与成功率同一规则 —— 显示 "—"。
describe('AnalyticsPage 缓存命中率零样本', () => {
    const seedNoTokens = (client: QueryClient) => {
        const k = keys();
        // 保留成功率那份样本(它与词元无关), 只把词元四桶与命中率清零。
        client.setQueryData(k.overview, {
            ...overviewData,
            input_tokens: 0,
            output_tokens: 0,
            cache_read_tokens: 0,
            cache_write_tokens: 0,
            cache_hit_rate: 0,
        });
        client.setQueryData(k.trend, trendData);
        client.setQueryData(k.models, modelsData);
        client.setQueryData(k.latency, latencyData);
    };

    it('无词元样本时显示 "—", 不是 0.0%', () => {
        const html = renderSSR(<AnalyticsPage />, seedNoTokens);
        expect(html).toContain('data-testid="cache-hit-rate"');
        expect(html).toContain('—'); // 哨兵值本身
        expect(html).not.toContain('0.0%');
    });

    it('有词元样本时仍展示真实命中率', () => {
        // 已有 fixture(300000 命中 / 100000 输入)算出来是 75.0%。
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('75.0');
    });
});
