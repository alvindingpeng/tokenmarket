import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { channelProfitQueryOptions, type ChannelProfit } from '@/api/channel';
import { ApiError } from '@/api/client';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { ChannelProfitPanel } from './Profit';
import { defaultProfitRange, profitPresetRange, profitRangeValid } from './profitRange';

// 面板四态回归(mock SSR): 加载 / 失败+重试 / 无权限 / 空 / 有数据必须各走各的分支。
// 利润是钱的数字, 绝不能拿零值兜底 —— 假零会被当成真实经营结果读。

const channelId = 12;

const payload: ChannelProfit = {
    channel_id: channelId,
    channel_name: 'OpenAI',
    start_date: '2026-10-02',
    end_date: '2026-11-01',
    totals: { requests: 1234, user_cost: 12.5, owner_revenue: 10, platform_revenue: 2.5, margin_rate: 20 },
    daily: [
        { date: '2026-10-02', requests: 10, user_cost: 1.5, owner_revenue: 1.2, platform_revenue: 0.3, margin_rate: 20 },
        { date: '2026-10-03', requests: 5, user_cost: 0.5, owner_revenue: 0.9, platform_revenue: -0.4, margin_rate: -80 },
    ],
};

// 组件在挂载时取"最近 30 天", 测试用同一函数算键, 否则种子数据落不到组件实际查询上。
const currentKey = () => {
    const range = defaultProfitRange();
    return channelProfitQueryOptions(channelId, range.startDate, range.endDate).queryKey;
};

function seedError(client: QueryClient, error: Error) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey: currentKey() }, {
        data: undefined,
        dataUpdateCount: 0,
        dataUpdatedAt: 0,
        error,
        errorUpdateCount: 1,
        errorUpdatedAt: Date.now(),
        fetchFailureCount: 1,
        fetchFailureReason: error,
        fetchMeta: null,
        isInvalidated: false,
        status: 'error',
        fetchStatus: 'idle',
    });
}

describe('ChannelProfitPanel 状态(mock SSR)', () => {
    it('有数据时渲染汇总与每日明细', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
            client.setQueryData(currentKey(), payload);
        });
        expect(html).toContain('Profit analysis');
        expect(html).toContain('Revenue');
        expect(html).toContain('$12.5'); // 收入
        expect(html).toContain('$10'); // 成本
        expect(html).toContain('$2.5'); // 毛利
        expect(html).toContain('20.00%'); // 毛利率
        expect(html).toContain('1,234'); // 请求数
        expect(html).toContain('2026-10-02'); // 每日行
        expect(html).toContain('2026-10-03');
        expect(html).not.toContain('Loading profit data');
        expect(html).not.toContain('Retry');
    });

    it('每日亏损行加负毛利, 不被四舍五入抹平', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
            client.setQueryData(currentKey(), payload);
        });
        expect(html).toContain('$-0.4');
        expect(html).toContain('-80.00%');
    });

    it('加载中只显示加载态, 不显示假零', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />);
        expect(html).toContain('Loading profit data');
        expect(html).not.toContain('Retry');
        expect(html).not.toContain('$0');
    });

    it('普通失败给报错与重试, 不落回空态', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
            seedError(client, new Error('boom'));
        });
        expect(html).toContain('Failed to load profit data');
        expect(html).toContain('Retry');
        expect(html).not.toContain('No settled billing records');
    });

    it('403 显示无权限且不给重试(角色问题重试无用)', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
            seedError(client, new ApiError(403, 'forbidden'));
        });
        expect(html).toContain('Only administrators can view channel profit');
        expect(html).not.toContain('Retry');
        expect(html).not.toContain('Failed to load profit data');
    });

    it('区间内无结算记录走真实空态', () => {
        const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
            client.setQueryData(currentKey(), {
                ...payload,
                totals: { requests: 0, user_cost: 0, owner_revenue: 0, platform_revenue: 0, margin_rate: 0 },
                daily: [],
            } satisfies ChannelProfit);
        });
        expect(html).toContain('No settled billing records in this range');
        expect(html).not.toContain('Retry');
        expect(html).not.toContain('2026-10-02');
    });

    it('三语渲染同一棵组件树', () => {
        // locale 用 BCP47 标签, 与 LocaleProvider 一致: 'zh_hans' 不是合法语言标签,
        // 带数字占位符的文案(近{days}天)会因 Intl.NumberFormat 报错而取不到值。
        for (const [locale, messages, expected] of [
            ['zh-Hans', zhHans, '利润分析'],
            ['zh-Hant', zhHant, '利潤分析'],
        ] as const) {
            const html = renderSSR(<ChannelProfitPanel channelId={channelId} />, (client) => {
                client.setQueryData(currentKey(), payload);
            }, { locale, messages });
            expect(html).toContain(expected);
            expect(html).toContain('20.00%');
        }
    });
});

describe('profit 日期范围', () => {
    it('默认最近 30 天(含今天)', () => {
        const range = defaultProfitRange(new Date(2026, 10, 1)); // 2026-11-01
        expect(range).toEqual({ startDate: '2026-10-03', endDate: '2026-11-01' });
    });

    it('预设区间按含今天的天数反推起点', () => {
        expect(profitPresetRange(7, new Date(2026, 0, 10))).toEqual({ startDate: '2026-01-04', endDate: '2026-01-10' });
        expect(profitPresetRange(1, new Date(2026, 0, 10))).toEqual({ startDate: '2026-01-10', endDate: '2026-01-10' });
    });

    it('起点晚于终点/任一为空都判非法', () => {
        expect(profitRangeValid('2026-10-02', '2026-11-01')).toBe(true);
        expect(profitRangeValid('2026-11-01', '2026-10-02')).toBe(false);
        expect(profitRangeValid('', '2026-11-01')).toBe(false);
        expect(profitRangeValid('2026-10-02', '')).toBe(false);
        expect(profitRangeValid('2026-11-01', '2026-11-01')).toBe(true);
    });
});

describe('channel.profit 三语键', () => {
    it('三语键位与关键文案齐全(整体一致性由 locales.test.ts 兜底)', () => {
        for (const [name, messages] of [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const) {
            const profit = (messages as { channel: { profit: Record<string, unknown> } }).channel.profit;
            for (const key of ['title', 'preset', 'startDate', 'endDate', 'rangeInvalid', 'loading', 'error', 'retry', 'empty', 'forbidden']) {
                expect({ name, key, value: profit[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            const metric = profit.metric as Record<string, unknown>;
            for (const key of ['revenue', 'revenueHint', 'cost', 'costHint', 'profit', 'profitHint', 'margin', 'marginHint', 'requests']) {
                expect({ name, key, value: metric[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            const table = profit.table as Record<string, unknown>;
            for (const key of ['date', 'requests', 'revenue', 'cost', 'profit', 'margin']) {
                expect({ name, key, value: table[key] }).toEqual({ name, key, value: expect.any(String) });
            }
        }
    });
});
