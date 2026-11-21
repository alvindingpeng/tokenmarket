import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiRequest } from './client';
import { explainUpstreamBill, importUpstreamBills, recomputeUpstreamBills } from './upstream-bill';

// 上游账单三条写操作是 P0 回归: 它们此前都把 body 自己 JSON.stringify 了一遍,
// 而 apiRequest 内部还会再序列化一次, 后端 ShouldBindJSON 收到的是字符串字面量 → 400,
// 定性/重算/导入全部等于不存在。导入更特殊: 后端 internal/server/handlers/upstream_bill.go
// 用 c.PostForm("channel_id") 读 multipart 字段, 而 FormData 交给 apiRequest 会被 stringify 成 "{}"
// 且硬带 application/json, channel_id 拿到空串直接 400 Invalid parameter。

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

describe('导入账单走 multipart(apiUpload)', () => {
    it('发 FormData 本体, 且不手写 Content-Type(boundary 必须由浏览器补)', async () => {
        stub(() => ok({ imported: 3 }));
        const file = new File(['model,cost\n'], 'bill.csv', { type: 'text/csv' });
        await expect(importUpstreamBills({ channel_id: 12, period: '2026-10', file })).resolves.toEqual({ imported: 3 });

        expect(calls).toHaveLength(1);
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/upstream-bills/import');
        expect(calls[0].init?.method).toBe('POST');
        // 关键: body 必须是 FormData 实例。交给 apiRequest 会被 JSON.stringify 成 '{}'。
        expect(calls[0].init?.body).toBeInstanceOf(FormData);
        const form = calls[0].init?.body as FormData;
        expect(form.get('channel_id')).toBe('12');
        expect(form.get('period')).toBe('2026-10');
        expect(form.get('file')).toBeInstanceOf(File);
        // 手写 Content-Type 会抹掉 multipart boundary, 后端再也读不到 PostForm。
        expect(new Headers(calls[0].init?.headers).get('Content-Type')).toBeNull();
    });
});

describe('定性/重算发单层 JSON', () => {
    it('explain 的请求体是对象本身, 不是 JSON 字符串字面量', async () => {
        stub(() => ok({}));
        await explainUpstreamBill({ id: 9, note: 'fx 差异' });
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/upstream-bills/explain');
        const parsed = JSON.parse(String(calls[0].init?.body)) as Record<string, unknown>;
        // 二次 stringify 的症状: 解析结果本身还是字符串。
        expect(typeof parsed).toBe('object');
        expect(parsed).toEqual({ id: 9, note: 'fx 差异' });
    });

    it('recompute 的请求体是对象本身, 不是 JSON 字符串字面量', async () => {
        stub(() => ok({ recomputed: 4 }));
        await recomputeUpstreamBills({ channel_id: 3, period: '2026-10' });
        expect(new URL(calls[0].url).pathname).toBe('/api/v1/upstream-bills/recompute');
        const parsed = JSON.parse(String(calls[0].init?.body)) as Record<string, unknown>;
        expect(typeof parsed).toBe('object');
        expect(parsed).toEqual({ channel_id: 3, period: '2026-10' });
    });
});

describe('防复发守卫', () => {
    it('再有人给 apiRequest 传字符串 body 会立刻抛错, 不会发出去等 400', async () => {
        stub(() => ok({}));
        const error = await apiRequest('/api/v1/upstream-bills/explain', { method: 'POST', body: JSON.stringify({ id: 9 }) })
            .catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(Error);
        expect(calls).toHaveLength(0);
    });
});
