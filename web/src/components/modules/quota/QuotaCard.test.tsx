import { describe, expect, it } from 'vitest';
import { QuotaCard, QuotaCardBody } from './QuotaCard';
import { renderSSR } from '@/test/ssr';
import type { QuotaUsageResponse } from '@/api/quota-usage';

// 配额卡的三条关键语义: 说明额度来源、显示已用/上限、不把未知渲染成 0。
// 用项目统一的 mock SSR(mock SSR 会在文本节点间插注释, 断言只取片段不断言整串)。

const baseUsage: QuotaUsageResponse = {
    usage: {
        user_id: 1,
        username: 'alice',
        tier: 'pro',
        rpm: 600,
        tpm: 2000000,
        concurrent: 32,
        source: 'tier',
        model: '',
        used_rpm: 120,
        used_tpm: 500000,
        in_flight: 3,
        window_reset_at: '2026-11-02T08:00:00+07:00',
    },
    remain_rpm: 480,
    remain_tpm: 1500000,
    remain_concurrent: 29,
};

describe('QuotaCardBody', () => {
    it('额度来自等级时明确说明等级, 并显示已用与上限', () => {
        const html = renderSSR(<QuotaCardBody data={baseUsage} />);
        expect(html).toContain('data-testid="quota-source"');
        expect(html).toContain('Pro'); // 等级名必须出现, 否则等于没说来源
        expect(html).toContain('120');
        expect(html).toContain('600');
        expect(html).toContain('data-testid="quota-rpm"');
    });

    it('额度来自显式策略时来源文案不同 —— 数值相同含义不同', () => {
        const policy: QuotaUsageResponse = {
            ...baseUsage,
            usage: { ...baseUsage.usage, source: 'policy' },
        };
        const html = renderSSR(<QuotaCardBody data={policy} />);
        expect(html).toContain('policy set specifically for you');
        expect(html).not.toContain('Limits come from your tier');
    });

    it('不限制的维度显示 Unlimited 而不是 0/0', () => {
        const unlimited: QuotaUsageResponse = {
            usage: { ...baseUsage.usage, tier: 'enterprise', rpm: 0, tpm: 0, concurrent: 0, source: 'none' },
            remain_rpm: 0,
            remain_tpm: 0,
            remain_concurrent: 0,
        };
        const html = renderSSR(<QuotaCardBody data={unlimited} />);
        expect(html.match(/Unlimited/g)).toHaveLength(3);
        expect(html).not.toContain('0 / 0');
    });

    it('接近上限时该维度进入警示配色', () => {
        const near: QuotaUsageResponse = {
            ...baseUsage,
            usage: { ...baseUsage.usage, used_rpm: 580 },
            remain_rpm: 20,
        };
        const html = renderSSR(<QuotaCardBody data={near} />);
        // 580/600 ≈ 96.7% -> danger 配色
        expect(html).toContain('bg-destructive');
    });
});

describe('QuotaCard 四态', () => {
    it('加载失败时报错并可重试, 不显示 0 用量', () => {
        const html = renderSSR(<QuotaCard query={{ isPending: false, isError: true, refetch: () => undefined }} />);
        expect(html).toContain('Failed to load quota usage');
        expect(html).toContain('Retry');
        expect(html).not.toContain('0 / ');
    });

    it('加载中显示加载态', () => {
        const html = renderSSR(<QuotaCard query={{ isPending: true, isError: false, refetch: () => undefined }} />);
        expect(html).toContain('Loading...');
        expect(html).not.toContain('Failed to load');
    });
});
