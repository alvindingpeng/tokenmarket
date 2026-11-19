import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MutationObserver, QueryClient } from '@tanstack/react-query';

import type { UserView } from './user';
vi.resetModules();
vi.stubGlobal('window', { localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } });
const { apiRequest, apiUnauthorizedEvent, currentStepUpToken, queryClient, setStepUpHandler } = await import('./client');
const { useAuthStore } = await import('./user');
const { channelProfitQueryOptions } = await import('./channel');

const admin = { id: 1, role: 'admin' } as UserView;
const reseller = { id: 2, role: 'reseller' } as UserView;
const profit = channelProfitQueryOptions(12, '2026-10-02', '2026-11-01');
const json = (data: unknown, status = 200) => new Response(JSON.stringify({ data }), { status });
function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
    return { promise, resolve, reject };
}

// 不 mock store 或 QueryClient：覆盖全局实例、真实查询取消和 mutation 回调时序。
describe('authentication session cache isolation', () => {
    beforeEach(() => {
        vi.stubGlobal('document', { baseURI: 'http://localhost/', cookie: '' });
        useAuthStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } });
        useAuthStore.getState().logout();
    });
    afterEach(() => {
        useAuthStore.getState().logout();
        setStepUpHandler(null);
        vi.unstubAllGlobals();
    });

    it('admin -> logout -> reseller 同步清空所有缓存，同键利润重新请求', async () => {
        expect(queryClient).toBeInstanceOf(QueryClient);
        useAuthStore.getState().setAuth(admin);
        const fetch = vi.fn().mockResolvedValueOnce(json({ secret: 'admin' })).mockResolvedValueOnce(json({ own: 'reseller' }));
        vi.stubGlobal('fetch', fetch);
        await queryClient.fetchQuery(profit);
        queryClient.setQueryData(['other-private-data'], 'secret');
        queryClient.getMutationCache().build(queryClient, { mutationFn: async () => null });
        const cancel = vi.spyOn(queryClient, 'cancelQueries');
        useAuthStore.getState().logout();
        expect(cancel).toHaveBeenCalled();
        cancel.mockRestore();
        expect(queryClient.getQueryCache().getAll()).toHaveLength(0);
        expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
        useAuthStore.getState().setAuth(reseller);
        expect(await queryClient.fetchQuery(profit)).toEqual({ own: 'reseller' });
        expect(fetch).toHaveBeenCalledTimes(2);
    });

    it('直接 setAuth、APIKey、再切 cookie 身份都清缓存和旧身份字段', async () => {
        useAuthStore.getState().setAuth(admin);
        queryClient.setQueryData<unknown>(profit.queryKey, 'admin');
        useAuthStore.getState().setAuth(reseller);
        expect(queryClient.getQueryData(profit.queryKey)).toBeUndefined();
        queryClient.setQueryData<unknown>(profit.queryKey, 'reseller');
        useAuthStore.getState().setAPIKeyAuth('key-a');
        expect(queryClient.getQueryData(profit.queryKey)).toBeUndefined();
        expect(useAuthStore.getState()).toMatchObject({ userId: 0, role: null });
        queryClient.setQueryData<unknown>(profit.queryKey, 'key-a');
        useAuthStore.getState().setAPIKeyAuth('key-b');
        expect(queryClient.getQueryData(profit.queryKey)).toBeUndefined();
        const fetch = vi.fn().mockResolvedValue(json(null));
        vi.stubGlobal('fetch', fetch);
        await apiRequest('/read');
        expect(new Headers(fetch.mock.calls[0][1].headers).get('Authorization')).toBe('Bearer key-b');
        useAuthStore.getState().setAuth(reseller);
        await apiRequest('/read');
        expect(new Headers(fetch.mock.calls[1][1].headers).get('Authorization')).toBeNull();
    });

    it.each([200, 401])('旧在途利润 %s 不回填、不登出新会话，即使 fetch 忽略 abort', async (status) => {
        useAuthStore.getState().setAuth(admin);
        const old = deferred<Response>();
        const fetch = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValue(json({ own: true }));
        vi.stubGlobal('fetch', fetch);
        const target = new EventTarget();
        const unauthorized = vi.fn(() => useAuthStore.getState().logout());
        target.addEventListener(apiUnauthorizedEvent, unauthorized);
        vi.stubGlobal('window', target);
        const pending = queryClient.fetchQuery(profit).catch(error => error);
        const signal = fetch.mock.calls[0][1].signal as AbortSignal;
        useAuthStore.getState().logout();
        useAuthStore.getState().setAuth(reseller);
        expect(signal.aborted).toBe(true);
        await queryClient.fetchQuery(profit);
        old.resolve(json({ secret: true }, status));
        await pending;
        await old.promise;
        await new Promise<void>(resolve => queueMicrotask(resolve));
        expect(queryClient.getQueryData(profit.queryKey)).toEqual({ own: true });
        expect(unauthorized).not.toHaveBeenCalled();
        expect(useAuthStore.getState()).toMatchObject({ userId: 2, isAuthenticated: true });
    });

    it('非 query 请求成功和 body 解析迟到也不能返回旧会话数据', async () => {
        const body = deferred<unknown>();
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: () => body.promise }));
        const pending = apiRequest('/read').catch(error => error);
        await vi.waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalled());
        useAuthStore.getState().setAuth(reseller);
        body.resolve({ data: 'old secret' });
        expect(await pending).toMatchObject({ name: 'AbortError' });
    });

    it.each([200, 401])('checkAuth %s 迟到不能复活 logout 或覆盖新登录', async (status) => {
        const old = deferred<Response>();
        vi.stubGlobal('fetch', () => old.promise);
        const pending = useAuthStore.getState().checkAuth();
        useAuthStore.getState().logout();
        old.resolve(json(admin, status));
        await pending;
        expect(useAuthStore.getState()).toMatchObject({ isAuthenticated: false, userId: 0 });
        const next = deferred<Response>();
        vi.stubGlobal('fetch', () => next.promise);
        const checking = useAuthStore.getState().checkAuth();
        useAuthStore.getState().setAuth(reseller);
        queryClient.setQueryData<unknown>(profit.queryKey, 'new');
        next.resolve(json(admin, status));
        await checking;
        expect(useAuthStore.getState()).toMatchObject({ isAuthenticated: true, userId: 2 });
        expect(queryClient.getQueryData(profit.queryKey)).toBe('new');
    });

    it('checkAuth 失效与身份变化清缓存，同身份校验保留缓存', async () => {
        useAuthStore.getState().setAuth(admin);
        vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(json(admin)).mockResolvedValueOnce(json(reseller)).mockResolvedValueOnce(json(null, 401)));
        queryClient.setQueryData<unknown>(profit.queryKey, 'admin');
        await useAuthStore.getState().checkAuth();
        expect(queryClient.getQueryData(profit.queryKey)).toBe('admin');
        await useAuthStore.getState().checkAuth();
        expect(queryClient.getQueryData(profit.queryKey)).toBeUndefined();
        queryClient.setQueryData<unknown>(profit.queryKey, 'reseller');
        await useAuthStore.getState().checkAuth();
        expect(queryClient.getQueryData(profit.queryKey)).toBeUndefined();
        expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });

    it('旧 step-up 弹窗凭证不进入新会话，也不重发旧写操作', async () => {
        const token = deferred<string>();
        const prompt = vi.fn(() => token.promise);
        setStepUpHandler(prompt);
        const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error_code: 'step_up_required' }), { status: 428 }));
        vi.stubGlobal('fetch', fetch);
        const pending = apiRequest('/write', { method: 'POST' }).catch(error => error);
        await vi.waitFor(() => expect(prompt).toHaveBeenCalledOnce());
        useAuthStore.getState().setAPIKeyAuth('new-key');
        // 即使弹窗尚未回传，换会话也必须让旧请求立即结束。
        expect(await pending).toMatchObject({ name: 'AbortError' });
        token.resolve('old-token');
        await token.promise;
        expect(currentStepUpToken()).toBeNull();
        expect(fetch).toHaveBeenCalledTimes(1);
    });

    it.each(['success', 'error'] as const)('clear 后旧 mutation %s 回调不回填、不 logout、不 invalidate', async (outcome) => {
        useAuthStore.getState().setAuth(admin);
        const result = deferred<string>();
        const onSuccess = vi.fn(() => queryClient.setQueryData<unknown>(profit.queryKey, 'old'));
        const onError = vi.fn(() => useAuthStore.getState().logout());
        const onSettled = vi.fn(() => queryClient.invalidateQueries());
        const mutationFn = vi.fn(() => result.promise);
        const mutation = queryClient.getMutationCache().build(queryClient, { mutationFn, onSuccess, onError, onSettled });
        const pending = mutation.execute(undefined).catch(error => error);
        await vi.waitFor(() => expect(mutationFn).toHaveBeenCalled());
        useAuthStore.getState().setAuth(reseller);
        queryClient.setQueryData<unknown>(profit.queryKey, 'new');
        if (outcome === 'success') result.resolve('old');
        else result.reject(new Error('old failure'));
        await pending;
        expect(onSuccess).not.toHaveBeenCalled();
        expect(onError).not.toHaveBeenCalled();
        expect(onSettled).not.toHaveBeenCalled();
        expect(queryClient.getQueryData(profit.queryKey)).toBe('new');
        expect(useAuthStore.getState().userId).toBe(2);
    });

    it('旧 mutation 的 per-call observer 回调也不能跨会话执行', async () => {
        const result = deferred<string>();
        const mutationFn = vi.fn(() => result.promise);
        const observer = new MutationObserver(queryClient, { mutationFn });
        const unsubscribe = observer.subscribe(() => {});
        const onError = vi.fn(() => useAuthStore.getState().logout());
        const onSettled = vi.fn();
        const pending = observer.mutate(undefined, { onError, onSettled }).catch(error => error);
        await vi.waitFor(() => expect(mutationFn).toHaveBeenCalledOnce());
        useAuthStore.getState().setAuth(reseller);
        result.reject(new Error('late'));
        await pending;
        expect(onError).not.toHaveBeenCalled();
        expect(onSettled).not.toHaveBeenCalled();
        expect(observer.getCurrentResult().status).toBe('idle');
        expect(useAuthStore.getState().userId).toBe(2);
        unsubscribe();
    });

    it('后启动的 checkAuth 获胜，旧失败不 logout', async () => {
        const old = deferred<Response>();
        const fetch = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(json(reseller));
        vi.stubGlobal('fetch', fetch);
        const first = useAuthStore.getState().checkAuth();
        await useAuthStore.getState().checkAuth();
        old.resolve(json(null, 401));
        await first;
        expect(useAuthStore.getState()).toMatchObject({ userId: 2, isAuthenticated: true });
    });

    it('APIKey 登录 mutation 同步切身份后，本次失败仍执行 logout', async () => {
        const onError = vi.fn(() => useAuthStore.getState().logout());
        const mutation = queryClient.getMutationCache().build(queryClient, {
            mutationFn: async () => {
                useAuthStore.getState().setAPIKeyAuth('invalid');
                throw new Error('invalid key');
            }, onError,
        });
        await mutation.execute(undefined).catch(() => null);
        expect(onError).toHaveBeenCalledOnce();
        expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });
});
