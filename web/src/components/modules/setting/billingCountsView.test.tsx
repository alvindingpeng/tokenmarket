import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { SettingBilling } from './Billing';

// 资金表留存卡片里四张表现有行数的失败态(mock SSR)。
//
// 修前的症状: 行数按 counts[row.key] ?? 0 渲染 —— 取数失败时四个键全缺, 于是四行全显 0。
// 后果不是看着别扭, 而是决策错误: 管理员据此认为表已经空了, 于是跳过「立即清理」,
// 而超期的资金明细还在库里。
//
// 同时保住反向: 表真的是 0 行时必须显 0 —— 那是「不必清理」这个具体结论。

const lifecycleKey = ['ops', 'billing-lifecycle'] as const;

function seed(client: QueryClient, value: unknown) {
    client.setQueryData(lifecycleKey, value);
}

function seedError(client: QueryClient) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey: lifecycleKey }, {
        data: undefined, dataUpdateCount: 0, dataUpdatedAt: 0,
        error: new Error('boom'), errorUpdateCount: 1, errorUpdatedAt: Date.now(),
        fetchFailureCount: 1, fetchFailureReason: new Error('boom'), fetchMeta: null,
        isInvalidated: false, status: 'error', fetchStatus: 'idle',
    });
}

describe('SettingBilling 行数失败态', () => {
    it('取数失败时报错, 且四行行数显示占位符而不是 0', () => {
        const html = renderSSR(<SettingBilling />, seedError);
        expect(html).toContain('Table counts unavailable');
        // 修前这里是四个 0 —— 管理员据此跳过清理, 而超期明细还在库里。
        expect(html).not.toContain('>0<');
        expect(html).toContain('—');
    });
});

describe('SettingBilling 行数正常', () => {
    it('真实行数照实渲染, 带千分位', () => {
        const html = renderSSR(<SettingBilling />, (client) => {
            seed(client, { items: [], retention_days: 90, archive_enabled: true, cutoff: '2026-01-01', counts: {
                billing_records: 50000, balance_ledgers: 12, balance_reservations: 7, billing_settlement_tasks: 3, billing_rollups: 0,
            } });
        });
        expect(html).toContain('50,000');
        expect(html).not.toContain('Table counts unavailable');
    });

    it('表真的是 0 行时显 0, 不是占位符', () => {
        // 反向保住: 把 0 也弄成占位符就反了 —— 0 行是「不必清理」这个具体结论。
        const html = renderSSR(<SettingBilling />, (client) => {
            seed(client, { items: [], retention_days: 90, archive_enabled: true, cutoff: '2026-01-01', counts: {
                billing_records: 0, balance_ledgers: 0, balance_reservations: 0, billing_settlement_tasks: 0, billing_rollups: 0,
            } });
        });
        expect(html).toContain('>0<');
    });
});
