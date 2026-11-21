import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAlertSilence, createBudget, updateBudget } from './ops';

// 静默窗口与花费预算是 P0 回归: 三处调用方曾自己 JSON.stringify 了一次, 而 apiRequest 内部
// 已经 stringify, 发出去的是 JSON 字符串字面量, 后端 ShouldBindJSON 100% 回 400。
// 静默窗口尤其糟: 它要 step-up, 用户输完密码才失败。
// 这些函数从 hook 里抽出来正是为了能在没有 QueryClientProvider 的环境里直接断言请求体形状。

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

// parseBody 解析发出的 body; 二次 stringify 的调用方在这里会解析出一个字符串。
const parseBody = (call: Call | undefined): Record<string, unknown> => {
    const raw = JSON.parse(String(call?.init?.body));
    expect(typeof raw).toBe('object');
    return raw as Record<string, unknown>;
};

describe('静默窗口创建(走 step-up 的写操作)', () => {
    it('POST /ops/silence/create, body 是单层对象', async () => {
        stub(() => ok({ item: { id: 1 } }));
        await createAlertSilence({ start_at: '2026-11-21T10:00:00+08:00', end_at: '2026-11-21T12:00:00+08:00', kind: 'channel', note: '维护' });

        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/ops/silence/create');
        expect(calls[0].init?.method).toBe('POST');
        expect(parseBody(calls[0])).toEqual({
            start_at: '2026-11-21T10:00:00+08:00',
            end_at: '2026-11-21T12:00:00+08:00',
            kind: 'channel',
            note: '维护',
        });
    });

    it('可选字段不填就不出现在请求体(留空=不改)', async () => {
        stub(() => ok({ item: { id: 1 } }));
        await createAlertSilence({ start_at: 'a', end_at: 'b' });
        expect(parseBody(calls[0])).toEqual({ start_at: 'a', end_at: 'b' });
    });
});

describe('花费预算新建/修改', () => {
    it('createBudget 打 POST /budgets, body 是单层对象', async () => {
        stub(() => ok({ item: { id: 2 } }));
        await createBudget({ user_id: 7, period: 'day', action: 'warn', limit_amount: 50, warn_ratio: 0.8 });

        expect(new URL(calls[0].url).pathname).toBe('/api/v1/budgets');
        expect(calls[0].init?.method).toBe('POST');
        expect(parseBody(calls[0])).toEqual({ user_id: 7, period: 'day', action: 'warn', limit_amount: 50, warn_ratio: 0.8 });
    });

    it('updateBudget 打 PUT /budgets/{id}, body 仍是单层对象且带 enabled', async () => {
        stub(() => ok({ item: { id: 5 } }));
        await updateBudget({ id: 5, period: 'month', action: 'block', limit_amount: 200, warn_ratio: 0.9, enabled: true });

        expect(new URL(calls[0].url).pathname).toBe('/api/v1/budgets/5');
        expect(calls[0].init?.method).toBe('PUT');
        // id 同时出现在路径与请求体里: 后端按 body.id 校验归属, 少一个就 400。
        expect(parseBody(calls[0])).toEqual({ id: 5, period: 'month', action: 'block', limit_amount: 200, warn_ratio: 0.9, enabled: true });
    });
});
