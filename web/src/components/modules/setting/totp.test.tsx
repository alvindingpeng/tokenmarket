import { afterEach, describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import hans from '@/locales/zh_hans.json';
import hant from '@/locales/zh_hant.json';
import { useAuthStore } from '@/api/user';
import { SettingTOTP, TOTPSetupDetails } from './TOTP';
import { TOTPResult, TOTPResultDetails } from './TOTPResult';
import { useTOTPResult } from './totp-result';
import { LoginForm } from '../login';

afterEach(() => { useTOTPResult.getState().clear(); useAuthStore.setState({ isAPIKeyAuth: false }); });
describe('TOTP SSR', () => {
    it.each([['en', en], ['zh-Hans', hans], ['zh-Hant', hant]] as const)('renders enabled and disabled settings in %s', (locale, messages) => {
        for (const enabled of [false, true]) {
            const html = renderSSR(<SettingTOTP />, client => client.setQueryData(['user', 'totp-status'], { totp_enabled: enabled }), { locale, messages });
            expect(html).toContain(messages.setting.account.totp[enabled ? 'enabled' : 'disabled']);
            expect(html).toContain(messages.setting.account.totp[enabled ? 'disable' : 'setup']);
        }
    });
    it('renders local inline SVG and manual secret without external image URLs', () => {
        const html = renderSSR(<TOTPSetupDetails setup={{ secret: 'LOCAL-SECRET', otpauth_url: 'otpauth://totp/test?secret=LOCAL-SECRET' }} />);
        expect(html).toContain('<svg');
        expect(html).toContain('LOCAL-SECRET');
        expect(html).not.toContain('<img');
        expect(html).not.toContain('src=');
        expect(html).not.toContain('href=');
    });
    it('keeps recovery codes visible across logout until explicit acknowledgement', () => {
        useTOTPResult.getState().show(['ONLY-ONCE-1', 'ONLY-ONCE-2']);
        useAuthStore.getState().logout();
        const result = useTOTPResult.getState().result!;
        const html = renderSSR(<TOTPResultDetails recoveryCodes={result.recoveryCodes} onConfirm={useTOTPResult.getState().clear} />);
        expect(html).toContain('ONLY-ONCE-1');
        expect(html).toContain('ONLY-ONCE-2');
        expect(html).toContain('shown only once');
        expect(html).toContain('role="dialog"');
        useTOTPResult.getState().clear();
        expect(renderSSR(<TOTPResult />)).toBe('');
    });
    it('shows relogin confirmation without codes after disable', () => {
        useTOTPResult.getState().show([]);
        const result = useTOTPResult.getState().result!;
        const html = renderSSR(<TOTPResultDetails recoveryCodes={result.recoveryCodes} onConfirm={useTOTPResult.getState().clear} />);
        expect(html).toContain('2FA disabled');
        expect(html).toContain('Continue to sign in');
        expect(html).not.toContain('<li');
    });
    it('password login always has an optional one-time-code input', () => {
        const html = renderSSR(<LoginForm />);
        expect(html).toContain('Authenticator or recovery code (optional)');
        expect(html).toContain('autoComplete="one-time-code"');
        const input = html.match(/<input[^>]*id="totp-code"[^>]*>/)?.[0];
        expect(input).toBeDefined();
        expect(input).not.toContain('required');
    });
});
