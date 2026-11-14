import { describe, expect, it } from 'vitest';
import { validateAccountJSON } from './AccountImport';

// validateAccountJSON 的本地预检规则必须与后端 model.AccountJSONParse 对齐。
//
// 两者漂移的后果是: 前端说"通过", 后端返回 400 —— 使用者等一轮往返才看到错误,
// 而本地预检的全部意义就是不让他等那一轮。所以每个平台的必需字段都要钉住。

describe('validateAccountJSON', () => {
    it('空输入报错', () => {
        expect(validateAccountJSON('codex', '')).toEqual(['JSON 不能为空']);
        expect(validateAccountJSON('codex', '   ')).toEqual(['JSON 不能为空']);
    });

    it('非法 JSON 报错', () => {
        const errors = validateAccountJSON('codex', 'not json');
        expect(errors).toHaveLength(1);
        expect(errors[0]).toContain('不是合法的 JSON');
    });

    describe('codex', () => {
        it('缺 access_token 与 refresh_token 时报两条', () => {
            const errors = validateAccountJSON('codex', '{}');
            expect(errors).toHaveLength(2);
        });

        it('缺 refresh_token 时说清无法自动续期', () => {
            const errors = validateAccountJSON('codex', JSON.stringify({ access_token: 'at' }));
            expect(errors).toHaveLength(1);
            // 这是"这个账号能不能自动续期"的信息, 比笼统的"缺字段"有用得多。
            expect(errors[0]).toContain('refresh_token');
            expect(errors[0]).toContain('无法自动续期');
        });

        it('齐全时通过', () => {
            const raw = JSON.stringify({ access_token: 'at', refresh_token: 'rt' });
            expect(validateAccountJSON('codex', raw)).toEqual([]);
        });

        it('CLI 导出的 tokens 封装也通过', () => {
            const raw = JSON.stringify({ tokens: { access_token: 'at', refresh_token: 'rt' } });
            expect(validateAccountJSON('codex', raw)).toEqual([]);
        });
    });

    describe('claude', () => {
        it('读 claudeAiOauth 下的 camelCase 字段', () => {
            const raw = JSON.stringify({
                claudeAiOauth: { accessToken: 'at', refreshToken: 'rt', expiresAt: 0 },
            });
            expect(validateAccountJSON('claude', raw)).toEqual([]);
        });

        it('也容忍顶层 snake_case', () => {
            const raw = JSON.stringify({ access_token: 'at', refresh_token: 'rt' });
            expect(validateAccountJSON('claude', raw)).toEqual([]);
        });

        it('缺 accessToken 与 refreshToken 时报两条', () => {
            const errors = validateAccountJSON('claude', JSON.stringify({ email: 'a@b.c' }));
            expect(errors).toHaveLength(2);
        });
    });

    describe('kiro', () => {
        it('缺 clientId/clientSecret 时报错', () => {
            const errors = validateAccountJSON('kiro', JSON.stringify({ refreshToken: 'rt' }));
            expect(errors).toHaveLength(1);
            expect(errors[0]).toContain('clientId');
        });

        it('齐全时通过', () => {
            const raw = JSON.stringify({ refreshToken: 'rt', clientId: 'c', clientSecret: 's' });
            expect(validateAccountJSON('kiro', raw)).toEqual([]);
        });
    });

    describe('cookie 平台', () => {
        it('gemini 缺 c_ses 报错', () => {
            const errors = validateAccountJSON('gemini', JSON.stringify({ c_oses: 'x' }));
            expect(errors).toHaveLength(1);
            expect(errors[0]).toContain('c_ses');
        });

        it('gemini 有 c_ses 通过', () => {
            expect(validateAccountJSON('gemini', JSON.stringify({ c_ses: 's' }))).toEqual([]);
        });

        // Gemini 同时支持 Gemini CLI 的 OAuth 令牌 —— 预检必须允许它,
        // 否则后端支持了、前端却把人拦在门外, 等于功能不存在。
        it('gemini 的 OAuth 令牌通过', () => {
            const raw = JSON.stringify({ access_token: 'at', refresh_token: 'rt', expiry_date: 1900000000000 });
            expect(validateAccountJSON('gemini', raw)).toEqual([]);
        });

        it('gemini 有 refresh_token 但缺 access_token 报错', () => {
            expect(validateAccountJSON('gemini', JSON.stringify({ refresh_token: 'rt' }))).toHaveLength(1);
        });

        it('grok 缺 sso 报错', () => {
            const errors = validateAccountJSON('grok', JSON.stringify({ 'sso-rw': 'x' }));
            expect(errors).toHaveLength(1);
            expect(errors[0]).toContain('sso');
        });

        it('grok 有 sso 通过', () => {
            expect(validateAccountJSON('grok', JSON.stringify({ sso: 's' }))).toEqual([]);
        });
    });

    // 平台与字段是对应的: 拿 codex 的 JSON 去校验 claude 应当报错,
    // 否则点错平台按钮会被静默放过, 直到后端才失败。
    it('平台选错时不会通过', () => {
        const codexRaw = JSON.stringify({ access_token: 'at', refresh_token: 'rt' });
        expect(validateAccountJSON('claude', codexRaw)).toEqual([]); // claude 也容忍 snake_case, 这是有意的
        // gemini 现在两种形状都收: 纯 c_ses 是 Cookie, 带 refresh_token 是 CLI 的 OAuth 令牌。
        // codexRaw 属于后一种, 所以这里必须通过 —— 这条断言变了, 是因为能力变了, 不是放宽了标准。
        expect(validateAccountJSON('gemini', codexRaw)).toEqual([]);
        expect(validateAccountJSON('grok', codexRaw)).toHaveLength(1); // grok 要 sso
    });
});
