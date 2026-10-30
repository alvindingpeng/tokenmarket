import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import {
    userStatsModelsQueryOptions,
    userStatsOverviewQueryOptions,
    userStatsTrendQueryOptions,
    type UserStatsModel,
    type UserStatsOverview,
    type UserStatsTrend,
} from '@/api/user-stats';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { getDateRange } from './dates';
import { AnalyticsPage } from './index';

// 页面状态回归(mock SSR): 加载 / 失败+重试 / 空态 / 有数据 四态必须各走各的分支。
// 失败绝不能落回「零」——假零会被当真实统计读; 空态也不能拿假数据糊弄。

const overviewData: UserStatsOverview = { total_requests: 1234, success_rate: 98.5, total_cost: 12.5, total_tokens: 456000 };
const trendData: UserStatsTrend[] = [{ date: '2026-01-15', requests: 10, cost: 1, success_rate: 100 }];
const modelsData: UserStatsModel[] = [{ model_name: 'gpt-4o', requests: 5, cost: 2, tokens: 1000 }];

// keys 与组件取数用同一套查询定义; 日期范围是「运行当天」, 与渲染时刻同一天, 查询键一致。
const keys = () => {
    const range = getDateRange('month');
    return {
        overview: userStatsOverviewQueryOptions(range).queryKey,
        trend: userStatsTrendQueryOptions({ ...range, granularity: 'day' }).queryKey,
        models: userStatsModelsQueryOptions(range).queryKey,
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

const seedAll = (client: QueryClient, patch: Partial<Record<'overview' | 'trend' | 'models', 'error'>> = {}) => {
    const k = keys();
    if (patch.overview === 'error') seedError(client, k.overview);
    else client.setQueryData(k.overview, overviewData);
    if (patch.trend === 'error') seedError(client, k.trend);
    else client.setQueryData(k.trend, trendData);
    if (patch.models === 'error') seedError(client, k.models);
    else client.setQueryData(k.models, modelsData);
};

describe('AnalyticsPage 页面状态(mock SSR)', () => {
    it('三请求都就绪时渲染真值, 不停在加载态', () => {
        const html = renderSSR(<AnalyticsPage />, (client) => seedAll(client));
        expect(html).toContain('Total Requests');
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
        expect(html.match(/Loading.../g)).toHaveLength(2);
        expect(html).toContain('Request Trend');
        expect(html).not.toContain('Retry'); // 没失败就不报错
    });

    it('全部在加载时整页是加载态, 不是假零', () => {
        const html = renderSSR(<AnalyticsPage />, () => undefined);
        expect(html.match(/Loading.../g)).toHaveLength(3);
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
