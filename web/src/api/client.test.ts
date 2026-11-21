import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
    ApiError,
    apiRequest,
    assertJSONBody,
    clearStepUpToken,
    isStepUpRequired,
    setStepUpHandler,
    stepUpTokenHeader,
} from './client';

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

// step-up 的重试契约是安全边界: 只有明确的 step_up_required 才允许「确认密码后原样重发一次」。
// 任何别的错误自动重试都可能把一笔状态未知的交易再推一遍(重复扣款/重复出款),
// 而重发超过一次等于给同样的风险第二次机会。这里逐条钉死。
describe('step-up 重试契约(安全边界)', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    type Call = { url: string; init: RequestInit | undefined };
    let calls: Call[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        clearStepUpToken();
    });

    afterEach(() => {
        setStepUpHandler(null);
        clearStepUpToken();
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    const stepUpResponse = () => new Response(JSON.stringify({ code: 428, message: 'Step-up verification required', error_code: 'step_up_required' }), {
        status: 428,
        headers: { 'Content-Type': 'application/json' },
    });

    const stub = (respond: (call: Call, index: number) => Promise<Response>) => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            const call = { url: String(url), init };
            calls.push(call);
            return respond(call, calls.length - 1);
        });
    };

    it('只有明确的 step-up 错误会重发: 恰好一次, 附凭证, body 原样', async () => {
        stub(async (_call, index) => index === 0
            ? stepUpResponse()
            : new Response(JSON.stringify({ code: 200, message: 'success', data: { done: true } }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            }));
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        await expect(apiRequest('/api/v1/thing', { method: 'POST', body: { a: 1 } })).resolves.toEqual({ done: true });
        expect(calls).toHaveLength(2); // 只补发一次
        expect(handler).toHaveBeenCalledTimes(1);
        expect(calls[1].init?.body).toBe(calls[0].init?.body); // 原样重发, 不是重新构造
        expect(new Headers(calls[1].init?.headers).get(stepUpTokenHeader)).toBe('step-token-1'); // fetch 头是 Headers 实例
    });

    it.each([400, 401, 403, 500, 502])('非 step-up 错误(%i)绝不弹窗、绝不重发', async (status) => {
        stub(async () => new Response(JSON.stringify({ code: status, message: 'nope' }), {
            status,
            headers: { 'Content-Type': 'application/json' },
        }));
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        const error = await apiRequest('/api/v1/thing', { method: 'POST', body: { a: 1 } }).catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect(isStepUpRequired(error)).toBe(false);
        expect(handler).not.toHaveBeenCalled();
        expect(calls).toHaveLength(1); // 原样抛给调用方, 一次就是一次
    });

    it('重发结果就是最终结果: 再失败也不再重发、不再弹第二次窗', async () => {
        stub(async (_call, index) => {
            if (index === 0) return stepUpResponse();
            return new Response(JSON.stringify({ code: 500, message: 'upstream exploded' }), {
                status: 500,
                headers: { 'Content-Type': 'application/json' },
            });
        });
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        const error = await apiRequest('/api/v1/thing', { method: 'POST', body: { a: 1 } }).catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).status).toBe(500);
        expect(calls).toHaveLength(2);
        expect(handler).toHaveBeenCalledTimes(1);
    });

    it('取消密码确认: 原请求一次都不发', async () => {
        stub(async () => stepUpResponse());
        setStepUpHandler(async () => null);

        const error = await apiRequest('/api/v1/thing', { method: 'POST', body: { a: 1 } }).catch((caught: unknown) => caught);
        expect(isStepUpRequired(error)).toBe(true);
        expect(calls).toHaveLength(1); // 一次都没有补发
    });
});

// 请求体序列化是 P0 回归: apiRequest 内部已经 JSON.stringify(body), 调用方若再 stringify 一次,
// 发出去的就是 JSON 字符串字面量("{...}"), 后端 ShouldBindJSON 必回 400 —— 6 个写操作
// (静默窗口/花费预算增改/上游账单定性/重算/导入)曾因此 100% 不可用, 且静默窗口要先输一次 step-up 密码才失败。
// 这里既钉住「对象只序列化一层」的正向行为, 也钉住「传字符串或 FormData 直接报错且不发请求」。
describe('apiRequest 请求体序列化', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    type Call = { url: string; init: RequestInit | undefined };
    let calls: Call[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    const ok = (data: unknown) => new Response(JSON.stringify({ code: 200, message: 'success', data }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
    });

    const stub = (respond: () => Response) => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return respond();
        });
    };

    it('对象 body 只序列化一层, 并带 application/json', async () => {
        stub(() => ok({ done: true }));
        await apiRequest('/api/v1/ops/silence/create', { method: 'POST', body: { start_at: 'a', end_at: 'b' } });
        expect(calls).toHaveLength(1);
        // 单层: 解析一次就得到原对象, 不会解析出字符串。
        expect(JSON.parse(String(calls[0].init?.body))).toEqual({ start_at: 'a', end_at: 'b' });
        // 二次 stringify 的典型症状: 解析结果本身是字符串。
        expect(typeof JSON.parse(String(calls[0].init?.body))).toBe('object');
        expect(new Headers(calls[0].init?.headers).get('Content-Type')).toBe('application/json');
    });

    it('body 传字符串直接抛错, 一次请求都不发', async () => {
        stub(() => ok({}));
        const error = await apiRequest('/api/v1/ops/silence/create', { method: 'POST', body: JSON.stringify({ start_at: 'a' }) })
            .catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(Error);
        expect((error as Error).message).toContain('JSON.stringify');
        expect(calls).toHaveLength(0); // 宁可不发, 也不发一个注定 400 的请求
    });

    it('body 传 FormData 直接抛错并指向 apiUpload, 一次请求都不发', async () => {
        stub(() => ok({}));
        const form = new FormData();
        form.append('channel_id', '7');
        const error = await apiRequest('/api/v1/upstream-bills/import', { method: 'POST', body: form })
            .catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(Error);
        expect((error as Error).message).toContain('apiUpload');
        expect(calls).toHaveLength(0);
    });

    it('assertJSONBody 放行普通对象与 undefined, 拦下字符串/FormData/数组字符串化', () => {
        expect(() => assertJSONBody({ a: 1 })).not.toThrow();
        expect(() => assertJSONBody(undefined)).not.toThrow();
        expect(() => assertJSONBody('{"a":1}')).toThrow();
        expect(() => assertJSONBody(new FormData())).toThrow();
    });

    it('GET 无 body 时不发 Content-Type: 空 body 不该装成 JSON 请求', async () => {
        stub(() => ok({ items: [] }));
        await apiRequest('/api/v1/ops/silence/list');
        expect(calls[0].init?.body).toBeUndefined();
        expect(new Headers(calls[0].init?.headers).get('Content-Type')).toBeNull();
    });
});