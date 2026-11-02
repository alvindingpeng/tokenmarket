import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChannelStatsFormatted } from '@/api/channel';
import { resetChannelBreaker } from '@/api/channel';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { isBroken, remainText } from './breaker';
import { ChannelPauseBadge } from './PauseBadge';
import { channelPauseResetVariables } from './pause';

// 渠道级额度暂停与人工启停、模型级熔断是三件事, 这里钉住"只有真暂停才显示"与"恢复动作的形状"。

const now = Date.UTC(2026, 10, 2, 4, 0, 0);

function channel(disabledUntil: string | null): ChannelStatsFormatted {
    return {
        channel_id: 12,
        user_id: 1,
        channel_name: '[OI] official',
        enabled: true,
        shared: false,
        share_code: '',
        review_status: 'approved',
        review_note: '',
        submitted_at: null,
        reviewed_at: null,
        trust_level: 'trusted',
        observation_start: null,
        observation_until: null,
        disabledUntil,
        models: [],
        formatted: {
            input_token: { value: '0', unit: '' },
            output_token: { value: '0', unit: '' },
            input_cost: { value: '0', unit: '$' },
            output_cost: { value: '0', unit: '$' },
            wait_time: { value: '0', unit: 's' },
            request_success: { value: '0', unit: '' },
            request_failed: { value: '0', unit: '' },
            request_count: { value: '0', unit: '' },
            total_token: { value: '0', unit: '' },
            total_cost: { value: '0', unit: '$' },
        },
    } as unknown as ChannelStatsFormatted;
}

describe('额度暂停剩余时长', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(now);
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('未暂停与非法时间都不算熔断', () => {
        expect(isBroken(null)).toBe(false);
        expect(isBroken(undefined)).toBe(false);
        expect(isBroken('not-a-time')).toBe(false);
        expect(isBroken(new Date(now - 1000).toISOString())).toBe(false);
    });

    it('到期时间在未来即为暂停中, 并按 时/分/秒 收敛成最短写法', () => {
        expect(isBroken(new Date(now + 60_000).toISOString())).toBe(true);
        expect(remainText(new Date(now + ((1 * 60 + 2) * 60 + 3) * 1000).toISOString())).toBe('1h2m');
        expect(remainText(new Date(now + 5 * 60_000).toISOString())).toBe('5m');
        expect(remainText(new Date(now + 30_000).toISOString())).toBe('30s');
        // 已过期不显示负数。
        expect(remainText(new Date(now - 30_000).toISOString())).toBe('0s');
    });
});

describe('ChannelPauseBadge (SSR)', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(now);
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('暂停中渲染徽章与恢复按钮, 并给出剩余时长', () => {
        const html = renderSSR(<ChannelPauseBadge channel={channel(new Date(now + 5 * 60_000).toISOString())} />);
        expect(html).toContain('Quota paused · 5m left');
        expect(html).toContain('Resume');
        expect(html).toContain('data-share-exclude');
        // 列表卡片位置紧张: 未要求 hint 时说明只落在 title 属性里, 不占一行版面。
        expect(html.split(en.channel.stats.pauseHint)).toHaveLength(2);
    });

    it('hint 为真时补一行解释: 上游余额不足触发整渠道暂停, 到期自动恢复', () => {
        const html = renderSSR(<ChannelPauseBadge channel={channel(new Date(now + 60_000).toISOString())} hint />);
        expect(html).toContain(en.channel.stats.pauseHint);
    });

    it('未暂停时整块不渲染, 不给每张卡片挂一个"未暂停"的噪音', () => {
        expect(renderSSR(<ChannelPauseBadge channel={channel(null)} />)).toBe('');
        expect(renderSSR(<ChannelPauseBadge channel={channel(new Date(now - 60_000).toISOString())} />)).toBe('');
    });

    it('三语渲染同一份暂停态', () => {
        for (const [locale, messages, badge] of [
            ['zh-Hans', zhHans, '额度暂停 · 剩余 5m'],
            ['zh-Hant', zhHant, '額度暫停 · 剩餘 5m'],
        ] as const) {
            const html = renderSSR(
                <ChannelPauseBadge channel={channel(new Date(now + 5 * 60_000).toISOString())} />,
                undefined,
                { locale, messages },
            );
            expect(html).toContain(badge);
            expect(html).toContain(locale === 'zh-Hans' ? '恢复' : '恢復');
        }
    });
});

describe('恢复整条渠道的请求形状', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: { url: string; init?: RequestInit }[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return new Response(JSON.stringify({ code: 200, message: 'success', data: { reset: 1 } }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            });
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    it('带 channel: true 打到渠道级恢复接口', async () => {
        await resetChannelBreaker(channelPauseResetVariables(channel(null)));
        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/channel/breaker/reset');
        expect(calls[0].init?.method).toBe('POST');
        expect(JSON.parse(String(calls[0].init?.body))).toEqual({ id: 12, channel: true });
    });

    it('变量来自渠道本身: 只把 id 与渠道级标志位发给后端', () => {
        expect(channelPauseResetVariables(channel(null))).toEqual({ id: 12, channel: true });
    });
});

describe('channel.stats 暂停文案三语', () => {
    it('四个键在三种语言里都齐全, 且占位符一致', () => {
        for (const [name, messages] of [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const) {
            const stats = messages.channel.stats as Record<string, string>;
            for (const key of ['pauseBadge', 'pauseResume', 'pauseReset', 'pauseHint']) {
                expect({ name, key, value: stats[key] }).toEqual({ name, key, value: expect.any(String) });
            }
            // 带剩余时长的键必须三语都留占位符, 否则界面会漏掉倒计时。
            expect(stats.pauseBadge).toContain('{time}');
            expect(stats.pauseReset).toContain('{name}');
        }
    });
});
