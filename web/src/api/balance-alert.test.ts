import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import {
    balanceAlertPath,
    balanceAlertQueryOptions,
    deleteBalanceAlert,
    saveBalanceAlert,
} from './balance-alert';

// 路径回归: GET/POST/DELETE 三条曾漏掉 /api/v1 前缀, 打到 /user/balance-alert 全是 404。

describe('balance-alert 请求路径', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: { url: string; init?: RequestInit }[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return new Response(JSON.stringify({ code: 200, message: 'success', data: { threshold_cny: 10, enabled: true } }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            });
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    it('GET 打到 /api/v1/user/balance-alert', async () => {
        await balanceAlertQueryOptions.queryFn({ signal: new AbortController().signal });
        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe(balanceAlertPath);
        expect(calls[0].init?.method ?? 'GET').toBe('GET');
    });

    it('POST 保存走同一路径', async () => {
        await saveBalanceAlert({ threshold_cny: 20, enabled: true });
        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/user/balance-alert');
        expect(calls[0].init?.method).toBe('POST');
    });

    it('DELETE 删除走同一路径', async () => {
        await deleteBalanceAlert();
        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/user/balance-alert');
        expect(calls[0].init?.method).toBe('DELETE');
    });

    it('查询把 signal 透传给请求: 取消立刻中止在飞的请求', async () => {
        let captured: AbortSignal | undefined;
        vi.stubGlobal('fetch', (_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
            captured = init?.signal ?? undefined;
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));

        const controller = new AbortController();
        const pending = balanceAlertQueryOptions.queryFn({ signal: controller.signal });
        controller.abort();
        await expect(pending).rejects.toThrow();
        expect(captured?.aborted).toBe(true);
    });
});

describe('balance-alert 重试策略', () => {
    it('4xx 不重试, 网络抖动/5xx 有上限地重试', () => {
        expect(balanceAlertQueryOptions.retry?.(0, new ApiError(404, 'not found'))).toBe(false);
        expect(balanceAlertQueryOptions.retry?.(0, new ApiError(503, 'unavailable'))).toBe(true);
        expect(balanceAlertQueryOptions.retry?.(2, new ApiError(503, 'unavailable'))).toBe(false);
    });
});
