import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
    ApiError,
    apiDownload,
    apiRequest,
    apiUpload,
    apiUnauthorizedEvent,
    clearStepUpToken,
    isStepUpRequired,
    setStepUpHandler,
    stepUpTokenHeader,
    stepUpVerifyPath,
    verifyStepUpPassword,
} from './client';

// 高危操作二次验证(step-up)的前端契约, 三条红线:
//   1. 只有服务端明确的 step-up 错误(428 + step_up_required)才弹密码确认;
//   2. 确认后只把"被拦下的那一次"原样重发一次(服务端保证它从未执行过), 绝不重复已执行的交易;
//   3. 其它任何错误照常抛出, 不弹窗也不自动重试。
// 这些行为在真实浏览器里很难稳定复现(要人工点弹窗), 用假 fetch 与假弹窗固化下来。

type StubCall = { url: string; init: RequestInit };

const stepUpBody = JSON.stringify({ code: 428, message: 'Step-up verification required', error_code: 'step_up_required' });

function stepUpResponse(): Response {
    return new Response(stepUpBody, { status: 428, headers: { 'Content-Type': 'application/json' } });
}

function jsonResponse(payload: unknown, status = 200): Response {
    return new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } });
}

function headerOf(init: RequestInit | undefined, name: string): string | null {
    return init?.headers ? new Headers(init.headers).get(name) : null;
}

describe('apiRequest 的 step-up 交互', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: StubCall[];

    beforeEach(() => {
        calls = [];
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        setStepUpHandler(null);
        clearStepUpToken();
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
        setStepUpHandler(null);
        clearStepUpToken();
    });

    it('logout invalidates a pending prompt and discards its late token', async () => {
        let resolve!: (token: string) => void;
        setStepUpHandler(() => new Promise<string>((done) => { resolve = done; }));
        stubFetch(() => stepUpResponse());
        const result = apiRequest('/danger', { method: 'POST' }).catch((e) => e);
        await vi.waitFor(() => expect(resolve).toBeTypeOf('function'));
        clearStepUpToken();
        resolve('late-credential');
        expect(await result).toBeInstanceOf(ApiError);
        expect(calls).toHaveLength(1);
        await apiRequest('/read', { skipStepUp: true }).catch(() => null);
        expect(headerOf(calls[1].init, stepUpTokenHeader)).toBeNull();
    });

    it('401 invalidates a pending prompt even without a browser event target', async () => {
        let resolve!: (token: string) => void;
        setStepUpHandler(() => new Promise<string>((done) => { resolve = done; }));
        stubFetch((call) => call.url.includes('/expired') ? jsonResponse({}, 401) : stepUpResponse());
        const result = apiRequest('/danger').catch((e) => e);
        await vi.waitFor(() => expect(resolve).toBeTypeOf('function'));
        await apiRequest('/expired').catch(() => null);
        resolve('late-after-401');
        expect(await result).toBeInstanceOf(ApiError);
        expect(calls).toHaveLength(2);
    });

    it('abort during the password prompt prevents retry', async () => {
        let resolve!: (token: string) => void;
        const controller = new AbortController();
        setStepUpHandler(() => new Promise<string>((done) => { resolve = done; }));
        stubFetch(() => stepUpResponse());
        const result = apiRequest('/danger', { signal: controller.signal }).catch((e) => e);
        await vi.waitFor(() => expect(resolve).toBeTypeOf('function'));
        controller.abort();
        resolve('verified');
        expect(await result).toMatchObject({ name: 'AbortError' });
        expect(calls).toHaveLength(1);
    });

    const stubFetch = (responder: (call: StubCall, index: number) => Response | Promise<Response>) => {
        vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
            const call = { url: String(url), init };
            calls.push(call);
            return responder(call, calls.length - 1);
        });
    };

    it('明确的 step-up 错误才弹确认, 确认后带凭证把原请求重发一次', async () => {
        stubFetch((_call, index) => index === 0
            ? stepUpResponse()
            : jsonResponse({ code: 200, message: 'success', data: { id: 7 } }));
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        await expect(apiRequest('/api/v1/user/manage/update', { method: 'POST', body: { id: 1, balance: 3 } }))
            .resolves.toEqual({ id: 7 });

        expect(handler).toHaveBeenCalledTimes(1);
        expect(calls).toHaveLength(2);
        // 重发的是同一个请求: 同路径、同方法、同请求体。
        expect(calls[0].url).toBe(calls[1].url);
        expect(calls[1].init.method).toBe('POST');
        expect(calls[1].init.body).toBe(calls[0].init.body);
        // 只有重发才带凭证 —— 凭证跟着"确认"走, 不是请求的常驻属性。
        expect(headerOf(calls[0].init, stepUpTokenHeader)).toBeNull();
        expect(headerOf(calls[1].init, stepUpTokenHeader)).toBe('step-token-1');
    });

    it('用户取消密码确认时抛出原 step-up 错误, 原请求不重发, 取消不遗留凭证', async () => {
        stubFetch(() => stepUpResponse());
        setStepUpHandler(async () => null);

        const error = await apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } })
            .catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect(isStepUpRequired(error)).toBe(true);
        expect(calls).toHaveLength(1);

        // 取消后一切归零: 下一笔高危请求重新弹窗, 且第一发不带任何残留凭证。
        calls = [];
        stubFetch(() => stepUpResponse());
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);
        await apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'c', value: 'd' } }).catch(() => undefined);
        expect(handler).toHaveBeenCalledTimes(1);
        expect(headerOf(calls[0].init, stepUpTokenHeader)).toBeNull();
    });

    it('没有弹窗实现时照常抛出 step-up 错误, 不静默重试', async () => {
        stubFetch(() => stepUpResponse());
        const error = await apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } })
            .catch((caught: unknown) => caught);
        expect(isStepUpRequired(error)).toBe(true);
        expect(calls).toHaveLength(1);
    });

    it.each([
        ['校验失败 400', jsonResponse({ code: 400, message: 'bad input' }, 400)],
        ['未认证 401', jsonResponse({ code: 401, message: 'Authentication failed' }, 401)],
        ['权限不足 403', jsonResponse({ code: 403, message: 'insufficient permissions' }, 403)],
        ['服务错误 500', jsonResponse({ code: 500, message: 'boom' }, 500)],
        ['缺前置条件但不是 step-up 的 428', jsonResponse({ code: 428, message: 'other precondition' }, 428)],
    ])('非 step-up 错误(%s)不弹确认也不重发', async (_label, response) => {
        stubFetch(() => response);
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        const error = await apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } })
            .catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(ApiError);
        expect(handler).not.toHaveBeenCalled();
        expect(calls).toHaveLength(1);
    });

    it('超时与网络错误也不触发 step-up', async () => {
        vi.stubGlobal('fetch', (_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
            calls.push({ url: 'timeout', init });
            init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        const error = await apiRequest('/api/v1/slow', { timeoutMs: 10 }).catch((caught: unknown) => caught);
        expect((error as ApiError).status).toBe(408);
        expect(handler).not.toHaveBeenCalled();
    });

    it('重发结果就是最终结果: 再次 step-up 或换成别的错误都到此为止', async () => {
        stubFetch((call, index) => {
            void call;
            return index === 0 ? stepUpResponse() : stepUpResponse();
        });
        const handler = vi.fn(async () => 'step-token-1');
        setStepUpHandler(handler);

        let error = await apiRequest('/api/v1/settlement/generate', { method: 'POST', body: { day: '20260101' } })
            .catch((caught: unknown) => caught);
        expect(isStepUpRequired(error)).toBe(true);
        expect(handler).toHaveBeenCalledTimes(1);
        expect(calls).toHaveLength(2); // 没有第三次请求、没有第二次弹窗。

        clearStepUpToken();
        calls = [];
        stubFetch((_call, index) => index === 0 ? stepUpResponse() : jsonResponse({ code: 500, message: 'settlement failed' }, 500));
        error = await apiRequest('/api/v1/settlement/generate', { method: 'POST', body: { day: '20260101' } })
            .catch((caught: unknown) => caught);
        expect((error as ApiError).message).toBe('settlement failed');
        expect(isStepUpRequired(error)).toBe(false);
        // 每个被拦下的请求各弹一次确认; 同一个请求内部绝不弹第二次。
        expect(handler).toHaveBeenCalledTimes(2);
        expect(calls).toHaveLength(2);
    });

    it('旧凭证被服务端判失效时清掉重换, 不带着死凭证空转', async () => {
        // 第一轮: 428 -> 弹窗 -> stale-token -> 重发成功; 凭证留在内存里等待复用。
        stubFetch((_call, index) => index === 0
            ? stepUpResponse()
            : jsonResponse({ code: 200, message: 'success', data: { ok: 'first' } }));
        const handler = vi.fn(async () => 'stale-token');
        setStepUpHandler(handler);
        await expect(apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } })).resolves.toEqual({ ok: 'first' });

        // 第二轮: 带着 stale-token 仍被拒(过期/改密/换会话), 必须清掉旧凭证换新的再发。
        calls = [];
        stubFetch((_call, index) => index === 0
            ? stepUpResponse() // 带着 stale-token 被拒
            : jsonResponse({ code: 200, message: 'success', data: { ok: 'second' } }));
        handler.mockImplementation(async () => 'fresh-token');

        await expect(apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } })).resolves.toEqual({ ok: 'second' });
        expect(handler).toHaveBeenCalledTimes(2);
        expect(calls).toHaveLength(2);
        expect(headerOf(calls[0].init, stepUpTokenHeader)).toBe('stale-token');
        expect(headerOf(calls[1].init, stepUpTokenHeader)).toBe('fresh-token');
    });

    it('并发撞上 step-up 只弹一次确认, 两个请求都重发成功', async () => {
        stubFetch(async (call, index) => {
            if (headerOf(call.init, stepUpTokenHeader) === null) return stepUpResponse();
            void index;
            return jsonResponse({ code: 200, message: 'success', data: { ok: true } });
        });
        const handler = vi.fn(async () => {
            await new Promise((resolve) => setTimeout(resolve, 10));
            return 'step-token-1';
        });
        setStepUpHandler(handler);

        const results = await Promise.all([
            apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'a', value: 'b' } }),
            apiRequest('/api/v1/setting/set', { method: 'POST', body: { key: 'c', value: 'd' } }),
        ]);
        expect(results).toEqual([{ ok: true }, { ok: true }]);
        expect(handler).toHaveBeenCalledTimes(1);
        expect(calls).toHaveLength(4); // 两次被拦 + 两次重发。
    });

    it('verifyStepUpPassword 换凭证时 401 不广播"登录失效"', async () => {
        const dispatched: string[] = [];
        vi.stubGlobal('window', {
            dispatchEvent: (event: Event) => { dispatched.push(event.type); return true; },
        });
        vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
            calls.push({ url: String(url), init });
            return jsonResponse({ code: 401, message: 'password verification failed' }, 401);
        });

        await expect(verifyStepUpPassword('wrong-password')).rejects.toBeInstanceOf(ApiError);
        expect(dispatched).not.toContain(apiUnauthorizedEvent);
        expect(calls).toHaveLength(1);
        expect(calls[0].url).toContain(stepUpVerifyPath);
        expect(calls[0].init.method).toBe('POST');
        expect(JSON.parse(String(calls[0].init.body))).toEqual({ password: 'wrong-password' });
    });

    it('apiDownload/apiUpload 同样走 step-up: 确认后带凭证重发一次', async () => {
        stubFetch((_call, index) => index === 0
            ? stepUpResponse()
            : new Response('id,amount\n1,2', {
                status: 200,
                headers: { 'Content-Disposition': 'attachment; filename="switchyard-audit.csv"' },
            }));
        setStepUpHandler(async () => 'step-token-1');

        const download = await apiDownload('./api/v1/audit/export');
        expect(await download.blob.text()).toBe('id,amount\n1,2');
        expect(download.filename).toBe('switchyard-audit.csv');
        expect(headerOf(calls[1].init, stepUpTokenHeader)).toBe('step-token-1');

        calls = [];
        clearStepUpToken();
        stubFetch((_call, index) => index === 0
            ? stepUpResponse()
            : jsonResponse({ code: 200, message: 'success', data: { rows_affected: { users: 3 } } }));
        await expect(apiUpload('/api/v1/setting/import', new FormData())).resolves.toEqual({ rows_affected: { users: 3 } });
        expect(headerOf(calls[1].init, stepUpTokenHeader)).toBe('step-token-1');
        // FormData 不能手写 Content-Type(boundary 由浏览器带)。
        expect(headerOf(calls[1].init, 'Content-Type')).toBeNull();
    });
});
