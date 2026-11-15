import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { accountSignInBegin, accountSignInComplete, accountSignInPlatforms } from '@/api/account-signin';

// apiRequest 用 document.baseURI 拼相对地址; node 环境下没有 document, 与 client.test.ts 一样补一个最小替身。
beforeEach(() => {
    (globalThis as { document?: unknown }).document = { baseURI: 'http://localhost/' };
});
afterEach(() => {
    delete (globalThis as { document?: unknown }).document;
    vi.unstubAllGlobals();
});

// 这三个接口的契约必须钉住: 后端返回的字段名(含 state / authorize_url)一旦改动,
// 前端会静默地拿 undefined 去打开一个 "undefined" 地址或提交一个空 state,
// 表现为"点了没反应", 而不是一条可读的错误。
function okResponse(data: unknown) {
    return new Response(JSON.stringify({ code: 200, message: 'success', data }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
    });
}

describe('account signin api', () => {
    it('begin 提交平台并返回授权地址与会话', async () => {
        const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
            okResponse({
                platform: 'claude',
                authorize_url: 'https://platform.claude.com/oauth/authorize?x=1',
                state: 'st-1',
                user_code_hint: '复制 code',
                expires_in: 900,
            }),
        );
        vi.stubGlobal('fetch', fetchMock);

        const result = await accountSignInBegin('claude');
        expect(result.authorize_url).toContain('platform.claude.com');
        expect(result.state).toBe('st-1');
        expect(result.expires_in).toBe(900);

        const [url, init] = fetchMock.mock.calls[0];
        // apiRequest 传的是 resolveAPIURL 出来的 URL 对象, 不是字符串。
        expect(String(url)).toContain('/api/v1/channel/account/signin/begin');
        expect(init?.method).toBe('POST');
        expect(JSON.parse(init?.body as string)).toEqual({ platform: 'claude' });
    });

    it('complete 把贴回来的内容与 state 一起提交', async () => {
        const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) =>
            okResponse({
                account: { channel_id: 1, name: 'acc', kind: 'oauth', auto_renewable: true },
                refreshed_now: true,
            }),
        );
        vi.stubGlobal('fetch', fetchMock);

        await accountSignInComplete({
            platform: 'codex',
            state: 'st-2',
            pasted: 'http://localhost:1455/auth/callback?code=abc&state=st-2',
            channel_id: 1,
            name: 'acc',
        });

        const [url, init] = fetchMock.mock.calls[0];
        expect(String(url)).toContain('/api/v1/channel/account/signin/complete');
        const body = JSON.parse(init?.body as string);
        // pasted 必须原样送出(含地址里的 & 与 =): 被转义坏掉的话后端解析不出 code。
        expect(body.pasted).toBe('http://localhost:1455/auth/callback?code=abc&state=st-2');
        expect(body.state).toBe('st-2');
        expect(body.channel_id).toBe(1);
    });

    it('platforms 返回各平台可用性', async () => {
        const fetchMock = vi.fn(async () =>
            okResponse([
                { platform: 'claude', available: true, auth_style: 'code', client_owner: 'claude-code' },
                { platform: 'gemini', available: false, reason: '未配置 OAuth 客户端' },
            ]),
        );
        vi.stubGlobal('fetch', fetchMock);

        const list = await accountSignInPlatforms();
        expect(list).toHaveLength(2);
        // 不可用的平台必须带回原因: 界面要能解释"为什么没有它", 而不是让它简单消失。
        expect(list[1].available).toBe(false);
        expect(list[1].reason).toBeTruthy();
    });
});
