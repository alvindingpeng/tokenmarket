import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { metricsSnapshotQueryOptions, type MetricsSnapshot } from '@/api/ops';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { MetricsSection } from './MetricsSection';

// 面板回归(mock SSR): 有数据 / 加载中 / 失败三态必须各走各的分支, 且数字来自快照本身。
// 快照字段名是照抄后端 op.MetricsTotals 的: 测试里故意填非零且互不相同的值,
// 这样任何一个字段接错(比如把 cost 接到 balance 上)都会被断言抓到。

const snapshot: MetricsSnapshot = {
    generated_at: '2026-11-08T04:30:00Z',
    requests_by_status: { success: 4321, failed: 77, canceled: 9 },
    input_tokens: 1234567,
    output_tokens: 765432,
    cost: 12.345678,
    users: 12,
    balance: 345.6789,
    channels: 6,
    channels_enabled: 4,
    alerts_24h: 3,
};

const queryKey = metricsSnapshotQueryOptions.queryKey;

// seedError 把查询推进"失败"态。retryOnMount: false 是为了让 mock SSR 停在落定后的错误分支,
// 而不是 react-query 重挂时的"即将重试"过渡态(见 channel/profit.test.tsx 同一处理)。
function seedError(client: QueryClient, error: Error) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey }, {
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

describe('MetricsSection (mock SSR)', () => {
    it('有数据时渲染关键指标与终态分布', () => {
        const html = renderSSR(<MetricsSection />, (client) => {
            client.setQueryData(queryKey, snapshot);
        });
        expect(html).toContain('Requests by terminal status');
        // 终态分列: 三个终态都要出现, 且计数是快照里的真值。
        expect(html).toContain('Success');
        expect(html).toContain('4321');
        expect(html).toContain('Failed');
        expect(html).toContain('77');
        expect(html).toContain('Canceled');
        expect(html).toContain('9');
        // token 走 formatTokenCount 的 m 档; 金额保留 6 位后裁尾零。
        expect(html).toContain('1.2m');
        expect(html).toContain('765,432');
        expect(html).toContain('12.345678');
        // 用户数与余额合计是两条独立指标, 不能被接成同一格。
        expect(html).toContain('12');
        expect(html).toContain('345.6789');
        // 渠道展示"启用/总数", 顺序写错会显示成 6/4。
        expect(html).toContain('4/6');
        expect(html).toContain('3'); // 24 小时告警
        // 最后刷新时间来自本地 dataUpdatedAt, 一定有值。
        expect(html).toContain('Last refreshed');
        expect(html).not.toContain('Failed to load');
    });

    it('加载中显示占位符, 不显示假零', () => {
        const html = renderSSR(<MetricsSection />);
        expect(html).toContain('Loading...');
        expect(html).toContain('-'); // 指标卡尚未有数据时是占位符
        // 加载态不能因为"没有数据"就渲染成"0 请求"。
        expect(html).not.toContain('No request has reached a terminal status yet.');
        expect(html).not.toContain('Failed to load');
    });

    it('失败时给报错而不是落回空态', () => {
        const html = renderSSR(<MetricsSection />, (client) => {
            seedError(client, new Error('boom'));
        });
        expect(html).toContain('Failed to load');
        expect(html).not.toContain('Requests by terminal status');
        expect(html).not.toContain('4321');
    });

    it('没有终态记录时走真实空态文案', () => {
        const html = renderSSR(<MetricsSection />, (client) => {
            client.setQueryData(queryKey, { ...snapshot, requests_by_status: {} } satisfies MetricsSnapshot);
        });
        expect(html).toContain('No request has reached a terminal status yet.');
        expect(html).not.toContain('Failed to load');
    });

    it('快照里出现未知状态时照样列出, 不静默丢弃', () => {
        // 后端新增终态(如 interrupted)时, 面板要显示原始键而不是把它藏起来 ——
        // 藏起来会让"总数对不上"变成无法解释的谜。
        const html = renderSSR(<MetricsSection />, (client) => {
            client.setQueryData(queryKey, {
                ...snapshot,
                requests_by_status: { success: 1, interrupted: 2 },
            } satisfies MetricsSnapshot);
        });
        expect(html).toContain('interrupted');
        expect(html).toContain('2');
    });

    it('三语渲染同一棵组件树', () => {
        for (const [locale, messages, expected] of [
            ['en', en, 'Channels enabled/total'],
            ['zh-Hans', zhHans, '启用/渠道总数'],
            ['zh-Hant', zhHant, '啟用/渠道總數'],
        ] as const) {
            const html = renderSSR(<MetricsSection />, (client) => {
                client.setQueryData(queryKey, snapshot);
            }, { locale, messages });
            expect(html).toContain(expected);
        }
    });
});
