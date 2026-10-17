import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, apiRequest } from './client';

// apiRequest 的超时与取消是「按钮永久转圈」这类问题的唯一出口, 必须有回归保护。
// 这两个路径在真实浏览器里极难复现(要等上游卡住 20 秒), 用假 fetch 固化下来。
describe('apiRequest', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;

    beforeEach(() => {
        // apiRequest 用 document.baseURI 拼相对地址, node 环境需要补一个最小替身。
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    it('上游不响应时按超时抛出 408', async () => {
        vi.stubGlobal('fetch', (_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));

        const error = await apiRequest('/api/v1/slow', { timeoutMs: 10 }).catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).status).toBe(408);
        expect((error as ApiError).message).toBe('Request timed out');
    });

    it('调用方主动取消时原样抛出, 不伪装成超时', async () => {
        vi.stubGlobal('fetch', (_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
            init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));

        const controller = new AbortController();
        const pending = apiRequest('/api/v1/slow', { signal: controller.signal, timeoutMs: 10000 });
        controller.abort();
        const error = await pending.catch((caught: unknown) => caught);
        expect(error).not.toBeInstanceOf(ApiError);
        expect((error as DOMException).name).toBe('AbortError');
    });

    it('成功时只返回 data 字段', async () => {
        vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ code: 200, message: 'success', data: { id: 7 } }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
        }));

        await expect(apiRequest<{ id: number }>('/api/v1/thing')).resolves.toEqual({ id: 7 });
    });

    it('非 2xx 时带上服务端的错误说明', async () => {
        vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ code: 400, message: 'bad input' }), {
            status: 400,
            headers: { 'Content-Type': 'application/json' },
        }));

        const error = await apiRequest('/api/v1/thing').catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).status).toBe(400);
        expect((error as ApiError).message).toBe('bad input');
    });

    it('响应体不是 JSON 时不抛解析错误', async () => {
        vi.stubGlobal('fetch', async () => new Response('<html>gateway</html>', { status: 502 }));
        const error = await apiRequest('/api/v1/thing').catch((caught: unknown) => caught);
        expect((error as ApiError).status).toBe(502);
        expect((error as ApiError).message).toBe('Request failed: 502');
    });
});
