import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import {
    channelProfitPath,
    channelProfitQueryKey,
    channelProfitQueryOptions,
    type ChannelProfit,
} from './channel';

// 契约回归: 路径必须是 /api/v1/channel/profit/:id, 日期进查询参数也进 queryKey。
// 日期不进键时切换区间会命中旧缓存, 界面显示的不是当前范围的数据。

const payload: ChannelProfit = {
    channel_id: 12,
    channel_name: 'OpenAI',
    start_date: '2026-10-02',
    end_date: '2026-11-01',
    totals: { requests: 3, user_cost: 3.5, owner_revenue: 2.5, platform_revenue: 1, margin_rate: 28.57 },
    daily: [
        { date: '2026-10-02', requests: 2, user_cost: 2, owner_revenue: 1.5, platform_revenue: 0.5, margin_rate: 25 },
        { date: '2026-10-03', requests: 1, user_cost: 1.5, owner_revenue: 1, platform_revenue: 0.5, margin_rate: 33.33 },
    ],
};

describe('channel profit 请求路径', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: { url: string; init?: RequestInit }[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return new Response(JSON.stringify({ code: 200, message: 'success', data: payload }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            });
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    it('路径带 /api/v1 前缀与渠道 id, 日期走查询参数', async () => {
        expect(channelProfitPath(12, '2026-10-02', '2026-11-01')).toBe(
            '/api/v1/channel/profit/12?start_date=2026-10-02&end_date=2026-11-01',
        );
        const options = channelProfitQueryOptions(12, '2026-10-02', '2026-11-01');
        const queryFn = options.queryFn;
        if (!queryFn) throw new Error('channelProfitQueryOptions is missing queryFn');
        await queryFn({
            client: new QueryClient(),
            queryKey: options.queryKey,
            signal: new AbortController().signal,
            meta: undefined,
        });
        expect(calls).toHaveLength(1);
        const url = new URL(calls[0].url);
        expect(url.pathname).toBe('/api/v1/channel/profit/12');
        expect(url.searchParams.get('start_date')).toBe('2026-10-02');
        expect(url.searchParams.get('end_date')).toBe('2026-11-01');
        expect(calls[0].init?.method ?? 'GET').toBe('GET');
    });

    it('QueryClient cancelQueries 把 AbortSignal 传到底层 fetch', async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        let signal: AbortSignal | null | undefined;
        vi.stubGlobal('fetch', (_url: string, init: RequestInit) => {
            signal = init.signal;
            return new Promise<Response>((_resolve, reject) => {
                signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
            });
        });
        const pending = client.fetchQuery(channelProfitQueryOptions(12, '2026-10-02', '2026-11-01')).catch(error => error);
        expect(signal?.aborted).toBe(false);
        await client.cancelQueries();
        expect(signal?.aborted).toBe(true);
        await pending;
        client.clear();
    });

    it('日期与渠道 id 都进 queryKey: 换范围/换渠道不会复用旧缓存', () => {
        expect(channelProfitQueryKey(12, '2026-10-02', '2026-11-01')).not.toEqual(
            channelProfitQueryKey(12, '2026-09-01', '2026-09-30'),
        );
        expect(channelProfitQueryKey(12, '2026-10-02', '2026-11-01')).not.toEqual(
            channelProfitQueryKey(13, '2026-10-02', '2026-11-01'),
        );
    });

    it('缺日期时不发请求(避免把后端默认区间错当用户选择)', () => {
        expect(channelProfitQueryOptions(12, '', '').enabled).toBe(false);
        expect(channelProfitQueryOptions(12, '2026-10-02', '').enabled).toBe(false);
        expect(channelProfitQueryOptions(0, '2026-10-02', '2026-11-01').enabled).toBe(false);
    });
});
