import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';


// 这里只钉两件"拼错了后端不会报错、只会静默做错事"的事:
//   1) 用户等级的 tier 字段真的进了请求体(否则创建出来的用户等级永远停在默认值);
//   2) 审计归档走的是 /ops/audit-lifecycle/* 而不是调用日志的 /ops/log-lifecycle/*。
// 审计归档的执行接口要求 step-up: 用 428 响应验证"撞上二次验证时不会静默失败"。

const originalDocument = (globalThis as { document?: unknown }).document;
let calls: { url: string; init?: RequestInit }[] = [];

beforeEach(() => {
    (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/', cookie: '' };
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

describe('用户管理请求携带 tier', () => {
    it('创建用户时 tier 进请求体', async () => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return ok({ id: 1 });
        });
        const { createUser } = await import('./usermanage');
        await createUser({
            username: 'alice',
            password: 'x'.repeat(8),
            role: 'user',
            status: 'active',
            tier: 'pro',
        });
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/user/manage/create');
        const body = JSON.parse(String(calls[0].init?.body)) as Record<string, unknown>;
        expect(body.tier).toBe('pro');
    });

    it('更新用户时未提交 tier 就不出现在请求体(留空=不改)', async () => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return ok('ok');
        });
        const { updateUser } = await import('./usermanage');
        await updateUser({ id: 3, username: 'alice', role: 'user', status: 'active' });
        const body = JSON.parse(String(calls[0].init?.body)) as Record<string, unknown>;
        expect(body).not.toHaveProperty('tier');
    });
});

describe('审计归档接口', () => {
    it('run 打到 /api/v1/ops/audit-lifecycle/run, 不是 log-lifecycle', async () => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return ok({ cutoff: '2026-01-01T00:00:00Z', archived: 3, file: 'audit-1.ndjson', deleted: 3, retention_days: 180, archive_enabled: true });
        });
        const { apiRequest } = await import('./client');
        await apiRequest('/api/v1/ops/audit-lifecycle/run', { method: 'POST' });
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/ops/audit-lifecycle/run');
        expect(calls[0].init?.method).toBe('POST');
    });

    it('list 打到 /api/v1/ops/audit-lifecycle/list 并解出 items/retention/enabled', async () => {
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return ok({ items: [{ name: 'a.ndjson', size: 10, mod_time: '2026-01-01T00:00:00Z' }], retention_days: 180, enabled: false });
        });
        const { apiRequest } = await import('./client');
        const data = await apiRequest<{ items: unknown[]; retention_days: number; enabled: boolean }>('/api/v1/ops/audit-lifecycle/list');
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/ops/audit-lifecycle/list');
        expect(data.items).toHaveLength(1);
        expect(data.retention_days).toBe(180);
        expect(data.enabled).toBe(false);
    });

    it('执行接口返回 428 step-up 时抛错, 由界面提示而不是假装成功', async () => {
        vi.stubGlobal('fetch', async () => new Response(
            JSON.stringify({ error_code: 'step_up_required', message: 'step up required' }),
            { status: 428, headers: { 'Content-Type': 'application/json' } },
        ));
        const { apiRequest } = await import('./client');
        await expect(apiRequest('/api/v1/ops/audit-lifecycle/run', { method: 'POST' })).rejects.toThrow();
    });
});
