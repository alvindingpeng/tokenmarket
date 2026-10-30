import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { QueryClient, type QueryKey } from '@tanstack/react-query';
import { ApiError } from './client';
import {
    userStatsModelsPath,
    userStatsModelsQueryOptions,
    userStatsOverviewPath,
    userStatsOverviewQueryOptions,
    userStatsTrendPath,
    userStatsTrendQueryOptions,
} from './user-stats';

// 路径回归: 这三条查询曾漏掉 /api/v1 前缀, 打到 /user-stats/* 全是 404。
// 404 又被默认重试拖着慢慢失败, 页面看起来只是「一直转圈」。

describe('user-stats 查询路径', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: { url: string; init?: RequestInit }[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return new Response(JSON.stringify({ code: 200, message: 'success', data: [] }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            });
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    // ctx 构造 react-query 传给 queryFn 的上下文; 测试只用到 signal。
    const ctx = (signal?: AbortSignal) => ({ signal, queryKey: [], meta: undefined }) as never;

    it.each([
        ['overview', userStatsOverviewPath, () => userStatsOverviewQueryOptions()],
        ['trend', userStatsTrendPath, () => userStatsTrendQueryOptions()],
        ['models', userStatsModelsPath, () => userStatsModelsQueryOptions()],
    ])('%s 打到 %s(含 /api/v1 前缀)', async (_name, path, build) => {
        await build().queryFn?.(ctx());
        expect(calls).toHaveLength(1);
        const url = new URL(calls[0].url);
        expect(url.pathname).toBe(path);
        expect(url.pathname).toContain('/api/v1/user-stats/');
    });

    it('起止日期与粒度逐个落到查询串', async () => {
        await userStatsTrendQueryOptions({ start_date: '2026-01-01', end_date: '2026-01-31', granularity: 'week' }).queryFn?.(ctx());
        const url = new URL(calls[0].url);
        expect(url.searchParams.get('start_date')).toBe('2026-01-01');
        expect(url.searchParams.get('end_date')).toBe('2026-01-31');
        expect(url.searchParams.get('granularity')).toBe('week');
    });

    it('queryFn 把 react-query 的 signal 透传给请求: 取消立刻中止在飞的请求', async () => {
        let captured: AbortSignal | undefined;
        vi.stubGlobal('fetch', (_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
            captured = init?.signal ?? undefined;
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));

        const controller = new AbortController();
        const pending = userStatsOverviewQueryOptions().queryFn?.(ctx(controller.signal));
        controller.abort();
        await expect(pending).rejects.toThrow();
        expect(captured?.aborted).toBe(true);
    });
});

// retryOf 取查询选项上的重试函数: queryOptions 把 retry 收窄成 boolean | number | 函数的联合,
// 这里只断言函数形态的策略本身。
const retryOf = (options: { retry?: unknown }) => options.retry as (failureCount: number, error: unknown) => boolean;

describe('user-stats 重试策略', () => {
    it('404/400 等 4xx 不重试: 一次失败立刻交错误, 不靠重试拖慢', () => {
        for (const options of [userStatsOverviewQueryOptions(), userStatsTrendQueryOptions(), userStatsModelsQueryOptions()]) {
            const retry = retryOf(options);
            expect(retry(0, new ApiError(404, 'not found'))).toBe(false);
            expect(retry(1, new ApiError(400, 'bad request'))).toBe(false);
        }
    });

    it('网络抖动/5xx 允许重试, 但有上限', () => {
        const retry = retryOf(userStatsOverviewQueryOptions());
        expect(retry(0, new ApiError(503, 'unavailable'))).toBe(true);
        expect(retry(1, new ApiError(503, 'unavailable'))).toBe(true);
        expect(retry(2, new ApiError(503, 'unavailable'))).toBe(false);
        expect(retry(0, new Error('network down'))).toBe(true);
    });

    it('404 端到端只发一次请求(曾被默认重试拖成多次)', async () => {
        const originalDocument = (globalThis as { document?: unknown }).document;
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        let calls = 0;
        vi.stubGlobal('fetch', async () => {
            calls += 1;
            return new Response(JSON.stringify({ code: 404, message: 'not found' }), {
                status: 404,
                headers: { 'Content-Type': 'application/json' },
            });
        });
        try {
            const client = new QueryClient();
            await expect(client.fetchQuery(userStatsOverviewQueryOptions())).rejects.toBeInstanceOf(ApiError);
            expect(calls).toBe(1);
        } finally {
            vi.unstubAllGlobals();
            (globalThis as { document?: unknown }).document = originalDocument;
        }
    });
});

describe('user-stats cache isolation', () => {
    it('never shares cached statistics between user identities', () => {
        const client = new QueryClient();
        for (const build of [userStatsOverviewQueryOptions, userStatsTrendQueryOptions, userStatsModelsQueryOptions]) {
            const first: QueryKey = build({}, 41).queryKey;
            const second = build({}, 42).queryKey;
            expect(first).not.toEqual(second);
            client.setQueryData(first, { total_requests: 41, success_rate: 100, total_cost: 1, total_tokens: 2 });
            expect(client.getQueryData(second)).toBeUndefined();
        }
        client.clear();
    });
});
