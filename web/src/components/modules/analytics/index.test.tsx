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
    // 三个时区字段默认给"已对齐": 两个时区偏移相同 —— 这是绝大多数用户的状态,
    // 默认 fixture 保持它, 页面就不该多出一行提示(错误提示比漏提示更烦人)。
    // 需要错开场景的用例自己改 offset。
    stats_timezone: 'Asia/Shanghai',
    stats_calendar_timezone: 'Asia/Shanghai',
    stats_timezone_offset_minutes: 480,
    stats_calendar_timezone_offset_minutes: 480,
    stats_timezone_source: 'server',
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
        // 独立加载则趋势已渲染, 只剩 3 个区块在转(概览/模型/延迟)。
        // 分段成功率柱状图与趋势图共用同一份查询, 趋势就绪时它一起就绪, 不额外转圈。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            client.setQueryData(keys().trend, trendData);
        });
        expect(html.match(/Loading.../g)).toHaveLength(3);
        expect(html).toContain('Request Trend');
        expect(html).not.toContain('Retry'); // 没失败就不报错
    });

    it('全部在加载时整页是加载态, 不是假零', () => {
        const html = renderSSR(<AnalyticsPage />, () => undefined);
        // 五个区块: 概览 / 趋势 / 分段成功率 / 延迟 / 模型。
        // 分段成功率与趋势共用同一份查询, 但各自独立渲染四态 —— 查询悬着时两处都转圈。
        expect(html.match(/Loading.../g)).toHaveLength(5);
        expect(html).not.toContain('Retry');
        expect(html).not.toMatch(/tabular-nums">0</);
    });

    it('默认预设是 today: 不出现日期输入(页面行为与修前一致)', () => {
        // 只有选中 custom 才有两框。SelectContent 是 portal, SSR 里看不到选项, 所以这里只断言输入框。
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).not.toContain('data-testid="custom-range"');
    });

    it('选中 custom 后出现起止两个 date 输入, 且默认值合法不报错', () => {
        // 初值都给"今天", 因此校验通过: 不该出现 invalid 提示, 也不该把半成品发出去。
        const html = renderSSR(<AnalyticsPage initialPreset="custom" />, (client) => seedAll(client));
        expect(html).toContain('data-testid="custom-range"');
        // 两个框都是 type=date, 并且各自带可访问名(aria-label), 不能只靠视觉位置区分起止。
        expect(html.match(/type="date"/g)).toHaveLength(2);
        expect(html).toContain('aria-label="Start date"');
        expect(html).toContain('aria-label="End date"');
        expect(html).not.toContain('data-testid="custom-range-invalid"');
    });

    it('选中 custom 后按"单日"取数: 与今天同一份查询键(粒度保持小时)', () => {
        // custom 单日区间与 today 同义(effectiveGranularity 规则), 因此查询键与 keys() 完全一致 ——
        // 若规则写错(比如 custom 恒降级成 day), 这份种子就会读不到, 页面转圈而不是显示数字。
        const html = renderSSR(<AnalyticsPage initialPreset="custom" />, (client) => seedAll(client));
        expect(html).toContain('1,234'); // 概览读到了
        expect(html).not.toContain('Loading...');
    });
});

describe('analytics 三语键', () => {
    it('三语文案键位齐全, 含自定义区间与分段成功率的新键', () => {
        for (const [name, messages] of [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const) {
            const analytics = (messages as { analytics: Record<string, unknown> }).analytics;
            const range = analytics.range as Record<string, unknown>;
            const custom = analytics.custom as Record<string, unknown>;
            const chart = analytics.chart as Record<string, unknown>;
            for (const key of ['title', 'loading', 'error', 'retry', 'empty']) {
                expect({ name, key, value: analytics[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            // custom 现在真的有日期控件兜着, 所以它必须有文案(修前被刻意移除过)。
            for (const key of ['today', 'week', 'month', 'custom']) {
                expect({ name, key, value: range[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            for (const key of ['start', 'end', 'invalid']) {
                expect({ name, key, value: custom[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            for (const key of ['successByBucket', 'successByBucketHint']) {
                expect({ name, key, value: chart[key] }).toEqual({ name, key, value: expect.any(String) });
            }
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

    it('两个时区偏移相同时不显示任何时区提示', () => {
        // 偏移相同 = 挂钟一致 = 用户看到的日期与分组完全一样, 这里多一行说明只是噪声。
        // 关键: 也要覆盖"名字不同但偏移相同"的情形, 否则按名字判会给一大批人误报。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            client.setQueryData(keys().overview, {
                ...overviewData,
                // 同偏移不同名: 用户偏好是 Asia/Macau, 服务器是 Asia/Shanghai —— 都是 +08:00。
                stats_timezone: 'Asia/Macau',
                stats_calendar_timezone: 'Asia/Shanghai',
                stats_timezone_source: 'user',
                stats_timezone_offset_minutes: 480,
                stats_calendar_timezone_offset_minutes: 480,
            });
        });
        expect(html).not.toContain('data-testid="timezone-notice"');
        expect(html).not.toContain('Range boundaries follow');
        // 页面其它部分照常。
        expect(html).toContain('Total Requests');
    });

    it('两个时区偏移不同时, 提示里同时出现两个时区名与偏移', () => {
        // 用户选了纽约(-05:00), 服务器在上海(+08:00): 区间边界按纽约算, 分桶按上海算。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            client.setQueryData(keys().overview, {
                ...overviewData,
                stats_timezone: 'America/New_York',
                stats_timezone_source: 'user',
                stats_timezone_offset_minutes: -300,
                stats_calendar_timezone: 'Asia/Shanghai',
                stats_calendar_timezone_offset_minutes: 480,
            });
        });
        expect(html).toContain('data-testid="timezone-notice"');
        // 两个时区都必须点名并带偏移: 只说"时区不同"用户无从判断差在哪、影响哪部分。
        expect(html).toContain('America/New_York');
        expect(html).toContain('UTC-05:00');
        expect(html).toContain('Asia/Shanghai');
        expect(html).toContain('UTC+08:00');
        // 文案只说事实, 并说明这种分工是刻意的 —— 不暗示它是缺陷。
        expect(html).toContain('Range boundaries follow');
        expect(html).toContain('This is intentional');
    });

    it('老后端没有时区字段时不渲染提示, 也不把 undefined 拼进文案', () => {
        // 部署里很可能还是旧版本: 拿不到事实就不说, 更不能印出 "Range boundaries follow UTC+00:00 undefined"。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            const stale: Record<string, unknown> = { ...overviewData };
            delete stale.stats_timezone;
            delete stale.stats_calendar_timezone;
            delete stale.stats_timezone_offset_minutes;
            delete stale.stats_calendar_timezone_offset_minutes;
            delete stale.stats_timezone_source;
            client.setQueryData(keys().overview, stale as unknown as UserStatsOverview);
        });
        expect(html).not.toContain('data-testid="timezone-notice"');
        expect(html).not.toContain('undefined');
        expect(html).toContain('Total Requests'); // 页面本身照常
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


// 分段成功率柱状图(§P0-3 的"独立成功率柱状图"那一半):
// 与趋势图共用同一份查询与同一套样本语义, 所以两处对"没样本"的判断必须一致 ——
// 趋势图断线的地方, 柱状图那里必须是一个空槽, 不能是一根 0% 的贴底柱。
describe('AnalyticsPage 分段成功率柱状图', () => {
    it('有样本时渲染独立图表区块, 并说明留空的含义', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('data-testid="success-bar-chart"');
        expect(html).toContain('Success rate per interval');
        // "没有样本 = 留空"必须写出来, 否则空槽会被当成渲染故障或 0 值。
        expect(html).toContain('never drawn as 0%');
    });

    it('整段都没有日志样本时走空态, 不画一张全空的图', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            const k = keys();
            // 每一天都是"无样本"(三项计数为 0): 那是留存期边界, 不是"成功率归零"。
            client.setQueryData(k.trend, [
                { date: '2026-01-15', requests: 10, cost: 1, success_rate: 0, request_success: 0, request_failed: 0, request_canceled: 0 },
            ] as UserStatsTrend[]);
        });
        expect(html).toContain('data-testid="success-bar-chart"');
        // 空态文案来自 card.successNoSamples: 点出这是"没有调用日志样本", 并给出留存天数。
        expect(html).toContain('No call-log samples in this range');
    });

    it('真·全失败(分母非零)不算空态: 0% 是真实值, 必须画出来', () => {
        // 与上一条的区别是分母: 有 5 次失败、0 次成功 —— 这一段确实全挂, 不能当成"无样本"隐藏。
        const html = renderSSR(<AnalyticsPage />, (client) => {
            seedAll(client);
            const k = keys();
            client.setQueryData(k.trend, [
                { date: '2026-01-15', requests: 5, cost: 0.5, success_rate: 0, request_success: 0, request_failed: 5, request_canceled: 0 },
            ] as UserStatsTrend[]);
        });
        expect(html).toContain('data-testid="success-bar-chart"');
        expect(html).not.toContain('No call-log samples in this range');
    });

    it('趋势查询失败时这张图也进失败态, 不落回空态', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client, { trend: 'error' }));
        expect(html).toContain('data-testid="success-bar-chart"');
        expect(html).toContain('Failed to load statistics');
    });
});
