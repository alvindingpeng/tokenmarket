import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { UpstreamBillsSection } from './UpstreamBillsSection';

// 上游对单: 失败态与空态必须可区分(mock SSR)。
//
// 修前的症状: 整个列表只有一个分支 —— `bills.data && bills.data.items.length > 0`。
// 请求失败时 data 是 undefined, 于是**什么都不渲染**, 屏幕上只剩筛选栏。
// 这与「该期间还没导入过账单」在界面上完全一样, 后果是渠道商以为自己没导成功,
// 于是重导一遍, 把同一批账单又写进去一次。

// 默认月份随当天变化, 用固定的 period 免得测试跨月飘。
const PERIOD = '202611';
const billsKey = ['upstream-bills', 0, PERIOD] as const;

function seed(client: QueryClient, state: { data?: unknown; error?: Error }) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey: billsKey }, {
        data: state.data,
        dataUpdateCount: 1,
        dataUpdatedAt: 'data' in state ? Date.now() : 0,
        error: state.error ?? null,
        errorUpdateCount: state.error ? 1 : 0,
        errorUpdatedAt: state.error ? Date.now() : 0,
        fetchFailureCount: state.error ? 1 : 0,
        fetchFailureReason: state.error ?? null,
        fetchMeta: null,
        isInvalidated: false,
        status: state.error ? 'error' : 'success',
        fetchStatus: 'idle',
    });
}

describe('UpstreamBillsSection 失败态', () => {
    it('取数失败时报错, 且不显示「还没导入」的空态文案', () => {
        const html = renderSSR(<UpstreamBillsSection />, (client) => {
            seed(client, { error: new Error('boom') });
        });
        expect(html).toContain('Failed to load bills');
        // 关键: 空态那句「该期间还没有导入过账单」不许出现 —— 那是另一件事。
        expect(html).not.toContain('No bills imported');
    });

    it('错误文案自己说明它不等于「没有导入」', () => {
        // 只写「加载失败」不够: 渠道商要先知道自己可能误读了空态, 才不会去重导一遍。
        const html = renderSSR(<UpstreamBillsSection />, (client) => {
            seed(client, { error: new Error('boom') });
        });
        expect(html).toContain('not the same as');
    });

    it('错误态带重试按钮与 role=alert', () => {
        const html = renderSSR(<UpstreamBillsSection />, (client) => {
            seed(client, { error: new Error('boom') });
        });
        expect(html).toContain('Retry');
        expect(html).toContain('role="alert"');
    });
});

describe('UpstreamBillsSection 空态与有数据', () => {
    it('真的没有账单时才显示空态文案', () => {
        const html = renderSSR(<UpstreamBillsSection />, (client) => {
            seed(client, { data: { items: [] } });
        });
        expect(html).toContain('No bills imported');
        expect(html).not.toContain('Failed to load bills');
    });

    it('有账单时渲染表格行, 且没有空态/错误文案', () => {
        const html = renderSSR(<UpstreamBillsSection />, (client) => {
            seed(client, { data: { items: [{
                id: 1, channel_id: 1, period: PERIOD, model_name: 'gpt-4o', currency: 'USD',
                cost: 12.5, cost_usd: 12.5, fx_rate: 1, owner_rev: 10, diff_ratio: 0.2,
                status: 'mismatch', note: '',
            }] } });
        });
        expect(html).toContain('gpt-4o');
        expect(html).not.toContain('No bills imported');
        expect(html).not.toContain('Failed to load bills');
    });
});
