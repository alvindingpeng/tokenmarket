import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { BudgetsSection, SilenceWindowsSection } from './index';

// 运维页两个区块的失败态(mock SSR)。
//
// 修前的症状: 两处都只判 isLoading 不看 isError, 于是请求失败时 data 是 undefined、列表渲染成空,
// 屏幕上是「还没有建过预算」/「暂无静默窗口」。
//   · 预算: 管理员据此以为预算被人删光了, 会去重建一遍(重复建出两条同名预算);
//   · 静默窗口: 值班的人据此以为当前没有任何静默 —— 而这一页的「是否静默中」直接决定告警会不会外发,
//     看错会导致维护期间该静的告警照常发给客户。
//
// 静默窗口还有第二种失败: 刷新失败但缓存里还有上一次的清单。此时保留数据是对的(比空白有用),
// 但**必须**顶一条「可能已过期」—— 否则就是静默展示过期数据。

// seedQuery 把一条查询推进到指定状态。retryOnMount: false 是为了让 mock SSR 停在落定后的分支,
// 而不是 react-query 重挂时的「即将重试」过渡态。
function seedQuery(client: QueryClient, queryKey: readonly unknown[], state: { data?: unknown; error?: Error }) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    const hasData = 'data' in state;
    client.getQueryCache().build(client, { queryKey }, {
        data: state.data,
        dataUpdateCount: 1,
        dataUpdatedAt: hasData ? Date.now() : 0,
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

const budgetsKey = ['ops', 'budgets'] as const;
const silenceKey = ['ops', 'alert-silence'] as const;

describe('BudgetsSection 失败态', () => {
    it('取数失败时报错, 并且不显示空态文案', () => {
        const html = renderSSR(<BudgetsSection />, (client) => {
            seedQuery(client, budgetsKey, { error: new Error('boom') });
        });
        // 修前这里是 No budgets yet —— 与预算被人删光在界面上完全一样。
        expect(html).toContain('Failed to load budgets');
        expect(html).not.toContain('No budgets yet');
    });

    it('错误文案自己说明它不等于还没建过', () => {
        // 只写「加载失败」不够: 管理员要先知道原来的空态文案可能骗过他, 才会去重试而不是重建。
        const html = renderSSR(<BudgetsSection />, (client) => {
            seedQuery(client, budgetsKey, { error: new Error('boom') });
        });
        expect(html).toContain('no budget has been created yet');
    });

    it('真的没有预算时才显示空态(0 条成功结果是空, 不是失败)', () => {
        const html = renderSSR(<BudgetsSection />, (client) => {
            seedQuery(client, budgetsKey, { data: [] });
        });
        expect(html).toContain('No budgets yet');
        expect(html).not.toContain('Failed to load budgets');
    });

    it('刷新失败但仍有旧数据时: 保留列表 + 明说可能已过期', () => {
        const html = renderSSR(<BudgetsSection />, (client) => {
            seedQuery(client, budgetsKey, {
                data: [{ id: 3, user_id: 12, api_key_id: 0, period: 'month', limit_amount: 100, warn_ratio: 80, action: 'block', enabled: true }],
                error: new Error('boom'),
            });
        });
        expect(html).toContain('this refresh failed');
        // 旧数据仍在: 收走资料并不能让人更安全。
        expect(html).toContain('100.00');
    });
});

describe('SilenceWindowsSection 失败态', () => {
    it('取数失败时报错, 不显示空态文案', () => {
        const html = renderSSR(<SilenceWindowsSection />, (client) => {
            seedQuery(client, silenceKey, { error: new Error('boom') });
        });
        expect(html).toContain('Failed to load silence windows');
        expect(html).not.toContain('No silence window');
    });

    it('刷新失败但仍有旧清单时: 保留清单 + 明说可能已过期', () => {
        const html = renderSSR(<SilenceWindowsSection />, (client) => {
            seedQuery(client, silenceKey, {
                data: [{ id: 1, start_at: '2026-11-20T00:00:00Z', end_at: '2026-11-20T02:00:00Z', kind: '', note: '维护' }],
                error: new Error('boom'),
            });
        });
        expect(html).toContain('Showing the last successful load');
        // 修前这里只有表格, 没有任何一句提示它可能过期 —— 而看错静默态会让该静的告警照常外发。
        expect(html).not.toContain('No silence window');
        // 旧清单本身还在(2026-11-20 那条)。
        expect(html).toContain('11/20/2026');
    });
});
