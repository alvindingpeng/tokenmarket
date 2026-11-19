import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearStepUpToken, setStepUpHandler, stepUpTokenHeader, ApiError, apiUnauthorizedEvent } from './client';
import { setupTOTP, enableTOTP, disableTOTP } from './totp';
import { loginUser } from './user';

describe('TOTP requests', () => {
    beforeEach(() => {
        vi.stubGlobal('document', { baseURI: 'https://example.test/app/' });
        clearStepUpToken();
    });
    afterEach(() => { setStepUpHandler(null); clearStepUpToken(); vi.unstubAllGlobals(); });
    it.each([
        ['setup', () => setupTOTP(), undefined, { secret: 'LOCAL', otpauth_url: 'otpauth://totp/test?secret=LOCAL' }],
        ['enable', () => enableTOTP('123456'), { code: '123456' }, { recovery_codes: ['RECOVERY'] }],
        ['disable', () => disableTOTP('RECOVERY'), { code: 'RECOVERY' }, 'ok'],
    ] as const)('%s reuses automatic step-up and replays exactly once', async (path, action, body, result) => {
        const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ error_code: 'step_up_required' }), { status: 428 }))
            .mockResolvedValueOnce(new Response(JSON.stringify({ data: result })));
        vi.stubGlobal('fetch', fetcher);
        const confirm = vi.fn(async () => 'step-credential');
        setStepUpHandler(confirm);
        await expect(action()).resolves.toEqual(result);
        expect(fetcher).toHaveBeenCalledTimes(2);
        expect(confirm).toHaveBeenCalledTimes(1);
        const [url, first] = fetcher.mock.calls[0];
        const second = fetcher.mock.calls[1][1];
        expect(String(url)).toBe('https://example.test/app/api/v1/user/2fa/' + path);
        expect(first.method).toBe('POST');
        expect(first.body).toBe(body === undefined ? undefined : JSON.stringify(body));
        expect(second.body).toBe(first.body);
        expect(new Headers(second.headers).get(stepUpTokenHeader)).toBe('step-credential');
    });
    it('login challenge never dispatches unauthorized or changes input credentials', async () => {
        const target = new EventTarget();
        vi.stubGlobal('window', target);
        const unauthorized = vi.fn();
        target.addEventListener(apiUnauthorizedEvent, unauthorized);
        const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error_code: 'two_factor_required', message: 'Code required' }), { status: 401 }));
        vi.stubGlobal('fetch', fetcher);
        const credentials = { username: 'alice', password: 'private', expire: 86400 };
        await expect(loginUser(credentials)).rejects.toMatchObject({ status: 401, code: 'two_factor_required' });
        expect(unauthorized).not.toHaveBeenCalled();
        expect(credentials).toEqual({ username: 'alice', password: 'private', expire: 86400 });
        fetcher.mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 1 } })));
        await loginUser({ ...credentials, totp_code: 'RECOVERY' });
        expect(JSON.parse(fetcher.mock.calls[1][1].body).totp_code).toBe('RECOVERY');
    });
    it('invalid code does not replay an operation', async () => {
        const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ message: 'Invalid code' }), { status: 400 }));
        vi.stubGlobal('fetch', fetcher);
        await expect(enableTOTP('bad')).rejects.toBeInstanceOf(ApiError);
        expect(fetcher).toHaveBeenCalledTimes(1);
    });
});
