import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import {
    checkWebhookSecret,
    checkWebhookUrl,
    createWebhook,
    deleteWebhook,
    redactWebhookError,
    requestWebhookTest,
    updateWebhook,
    webhookCollectionPath,
    webhookItemPath,
    webhookListPath,
    webhookListQueryOptions,
    webhookLogsPath,
    webhookLogsQueryOptions,
    webhookSecretByteLength,
    webhookTestPath,
    WEBHOOK_LOG_PAGE_SIZE,
    WEBHOOK_PAGE_SIZE,
} from './webhook';

// 路径回归: 每条请求显式 /api/v1 前缀(此前多处漏前缀打 404 还被默认重试拖慢)。
// 身份回归: queryKey 必须带 userId —— 换账号不能复用上一个账号的订阅缓存。

describe('webhook 请求路径(显式 /api/v1 前缀)', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;
    let calls: { url: string; init?: RequestInit }[] = [];

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
        calls = [];
        vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
            calls.push({ url: String(url), init });
            return new Response(JSON.stringify({ code: 200, message: 'success', data: { id: 7 } }), {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
            });
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    it('列表 GET 打到 /api/v1/webhook/list 带 page/page_size', async () => {
        await webhookListQueryOptions(7, 2).queryFn({ signal: new AbortController().signal });
        expect(calls).toHaveLength(1);
        const url = new URL(calls[0].url);
        expect(url.pathname).toBe('/api/v1/webhook/list');
        expect(url.searchParams.get('page')).toBe('2');
        expect(url.searchParams.get('page_size')).toBe(String(WEBHOOK_PAGE_SIZE));
        expect(calls[0].init?.method ?? 'GET').toBe('GET');
    });

    it('创建 POST 打到 /api/v1/webhook', async () => {
        await createWebhook({ url: 'https://a.example/x', secret: 's'.repeat(16), events: ['ticket'], enabled: true, retry_count: 3 });
        expect(new URL(calls[0].url).pathname).toBe(webhookCollectionPath);
        expect(calls[0].init?.method).toBe('POST');
        const body = JSON.parse(String(calls[0].init?.body));
        expect(body).toMatchObject({ url: 'https://a.example/x', events: ['ticket'], enabled: true, retry_count: 3 });
        expect(body.secret).toBe('s'.repeat(16));
    });

    it('编辑 PUT 打到 /api/v1/webhook/:id', async () => {
        await updateWebhook(7, { url: 'https://a.example/x', events: ['ticket'], enabled: false, retry_count: 0, secret: 't'.repeat(16) });
        expect(new URL(calls[0].url).pathname).toBe(webhookItemPath(7));
        expect(calls[0].init?.method).toBe('PUT');
    });

    it('删除 DELETE 打到 /api/v1/webhook/:id', async () => {
        await deleteWebhook(7);
        expect(new URL(calls[0].url).pathname).toBe(webhookItemPath(7));
        expect(calls[0].init?.method).toBe('DELETE');
    });

    it('测试 POST 打到 /api/v1/webhook/:id/test(只入队)', async () => {
        await requestWebhookTest(7);
        expect(new URL(calls[0].url).pathname).toBe(webhookTestPath(7));
        expect(calls[0].init?.method).toBe('POST');
    });

    it('投递日志 GET 打到 /api/v1/webhook/:id/logs 带 page/page_size', async () => {
        await webhookLogsQueryOptions(7, 9, 1).queryFn({ signal: new AbortController().signal });
        expect(calls).toHaveLength(1);
        const url = new URL(calls[0].url);
        expect(url.pathname).toBe('/api/v1/webhook/9/logs');
        expect(url.searchParams.get('page')).toBe('1');
        expect(url.searchParams.get('page_size')).toBe(String(WEBHOOK_LOG_PAGE_SIZE));
    });

    it('路径构造器钉死契约形状, 页码下限为 1', () => {
        expect(webhookListPath(1, 20)).toBe('/api/v1/webhook/list?page=1&page_size=20');
        expect(webhookListPath(0, 20)).toBe('/api/v1/webhook/list?page=1&page_size=20');
        expect(webhookItemPath(3)).toBe('/api/v1/webhook/3');
        expect(webhookTestPath(3)).toBe('/api/v1/webhook/3/test');
        expect(webhookLogsPath(3, 4, 20)).toBe('/api/v1/webhook/3/logs?page=4&page_size=20');
    });

    it('编辑时空 secret 不上行(留空=保留原密钥), 非空才带上', async () => {
        await updateWebhook(7, { url: 'https://a.example/x', events: [], enabled: true, retry_count: 1, secret: '' });
        expect(JSON.parse(String(calls[0].init?.body))).not.toHaveProperty('secret');

        await updateWebhook(7, { url: 'https://a.example/x', events: [], enabled: true, retry_count: 1, secret: 'u'.repeat(16) });
        expect(JSON.parse(String(calls[1].init?.body)).secret).toBe('u'.repeat(16));
    });
});

describe('webhook AbortSignal 透传', () => {
    const originalDocument = (globalThis as { document?: unknown }).document;

    beforeEach(() => {
        (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        (globalThis as { document?: unknown }).document = originalDocument;
    });

    const stubHanging = (captured: { signal?: AbortSignal }) => {
        vi.stubGlobal('fetch', (_url: string, init?: RequestInit) => new Promise((_resolve, reject) => {
            captured.signal = init?.signal ?? undefined;
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }));
    };

    it('查询把 signal 透传给请求: 取消立刻中止在飞的列表请求', async () => {
        const captured: { signal?: AbortSignal } = {};
        stubHanging(captured);
        const controller = new AbortController();
        const pending = webhookListQueryOptions(7, 1).queryFn({ signal: controller.signal });
        controller.abort();
        await expect(pending).rejects.toThrow();
        expect(captured.signal?.aborted).toBe(true);
    });

    it('写请求同样带 signal: 取消立刻中止在飞的创建请求', async () => {
        const captured: { signal?: AbortSignal } = {};
        stubHanging(captured);
        const controller = new AbortController();
        const pending = createWebhook(
            { url: 'https://a.example/x', events: ['ticket'], enabled: true, retry_count: 1, secret: 'v'.repeat(16) },
            { signal: controller.signal },
        );
        controller.abort();
        await expect(pending).rejects.toThrow();
        expect(captured.signal?.aborted).toBe(true);
    });
});

describe('webhook 重试策略(4xx 不重试)', () => {
    it('4xx 一次失败就报错, 5xx/网络抖动有上限地重试', () => {
        for (const options of [webhookListQueryOptions(7, 1), webhookLogsQueryOptions(7, 7, 1)]) {
            expect(options.retry?.(0, new ApiError(404, 'not found'))).toBe(false);
            expect(options.retry?.(0, new ApiError(400, 'bad'))).toBe(false);
            expect(options.retry?.(0, new ApiError(503, 'unavailable'))).toBe(true);
            expect(options.retry?.(2, new ApiError(503, 'unavailable'))).toBe(false);
        }
    });
});

describe('webhook queryKey 身份隔离', () => {
    it('列表 key 带 userId: 换账号不共用缓存', () => {
        const mine = webhookListQueryOptions(7, 1).queryKey;
        const others = webhookListQueryOptions(8, 1).queryKey;
        expect(mine).toContain(7);
        expect(others).toContain(8);
        expect(JSON.stringify(mine)).not.toBe(JSON.stringify(others));
    });

    it('同用户翻页各自成 key, 列表与日志不同子树', () => {
        const page1 = webhookListQueryOptions(7, 1).queryKey;
        const page2 = webhookListQueryOptions(7, 2).queryKey;
        const logs = webhookLogsQueryOptions(7, 9, 1).queryKey;
        expect(JSON.stringify(page1)).not.toBe(JSON.stringify(page2));
        expect(page1[0]).toBe('webhook');
        expect(logs).toContain(7);
        expect(logs).toContain(9);
        expect(logs[1]).toBe('logs');
        expect(JSON.stringify(page1)).not.toBe(JSON.stringify(logs));
    });

    it('日志 key 同样按用户隔离', () => {
        const mine = webhookLogsQueryOptions(7, 9, 1).queryKey;
        const others = webhookLogsQueryOptions(8, 9, 1).queryKey;
        expect(JSON.stringify(mine)).not.toBe(JSON.stringify(others));
    });
});

describe('webhook 密钥校验(16..128 字节)', () => {
    it('字节长度按 UTF-8 计: 多字节字符不能按字符数蒙混', () => {
        expect(webhookSecretByteLength('a')).toBe(1);
        expect(webhookSecretByteLength('密')).toBe(3);
        // 6 个汉字 = 18 字节: 字符数 6 不足 16, 字节数合格 —— 按字符数判会误杀。
        expect(checkWebhookSecret('密'.repeat(6), 'create')).toBe('ok');
        // 43 个汉字 = 129 字节: 字符数 43 不超 128, 字节数超标 —— 按字符数判会漏放。
        expect(checkWebhookSecret('密'.repeat(43), 'create')).toBe('tooLong');
    });

    it('创建必填, 边界 16/128 合格, 15/129 不合格', () => {
        expect(checkWebhookSecret('', 'create')).toBe('required');
        expect(checkWebhookSecret('a'.repeat(15), 'create')).toBe('tooShort');
        expect(checkWebhookSecret('a'.repeat(16), 'create')).toBe('ok');
        expect(checkWebhookSecret('a'.repeat(128), 'create')).toBe('ok');
        expect(checkWebhookSecret('a'.repeat(129), 'create')).toBe('tooLong');
    });

    it('编辑留空=保持原密钥, 非空仍按 16..128 校验', () => {
        expect(checkWebhookSecret('', 'edit')).toBe('ok');
        expect(checkWebhookSecret('a'.repeat(15), 'edit')).toBe('tooShort');
        expect(checkWebhookSecret('a'.repeat(16), 'edit')).toBe('ok');
        expect(checkWebhookSecret('a'.repeat(129), 'edit')).toBe('tooLong');
    });
});

describe('webhook 地址校验(仅 HTTPS 443)', () => {
    it('https 默认端口与显式 443 放行', () => {
        expect(checkWebhookUrl('https://example.com/hook')).toBe('ok');
        expect(checkWebhookUrl('https://example.com:443/hook')).toBe('ok');
        expect(checkWebhookUrl('  https://example.com/hook  ')).toBe('ok');
    });

    it('http/非 443 端口/非法 URL/空值分别拦截', () => {
        expect(checkWebhookUrl('http://example.com/hook')).toBe('httpsOnly');
        expect(checkWebhookUrl('https://example.com:8443/hook')).toBe('port');
        expect(checkWebhookUrl('not a url')).toBe('invalid');
        expect(checkWebhookUrl('')).toBe('empty');
    });
});

describe('webhook 错误文案脱敏', () => {
    it('URL(可能带 token)与密钥绝不进 toast 文案', () => {
        const url = 'https://example.com/hook?token=abc123';
        const secret = 'top-secret-value-1';
        const message = 'delivery to ' + url + ' rejected with ' + secret;
        const redacted = redactWebhookError(message, [url, secret]);
        expect(redacted).not.toContain(url);
        expect(redacted).not.toContain(secret);
        expect(redacted).not.toContain('abc123');
        expect(redacted).toContain('***');
    });

    it('空值安全: 未提供的敏感串不影响原文', () => {
        expect(redactWebhookError('plain failure', [null, undefined, ''])).toBe('plain failure');
    });
});
