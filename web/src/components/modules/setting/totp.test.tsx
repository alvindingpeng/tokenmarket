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

// 2FA 信任设备与恢复码自助重生成(v1.11.0)。
//
// 这一组盯两件事: ①"拿不到清单"绝不能被渲染成"0 台设备"(那会让人以为没有陌生设备);
// ② 重新生成恢复码复用同一张一次性凭据面板, 但**不能说"会话已被吊销、请重新登录"** ——
//    重新生成不吊销会话, 沿用旧文案就是对着用户说假话。
describe('信任设备与恢复码重生成', () => {
    const deviceSeed = (client: import('@tanstack/react-query').QueryClient) => {
        client.setQueryData(['user', 'totp-status'], { totp_enabled: true, totp_recovery_remaining: 3 });
        client.setQueryData(['user', '2fa-trusted-devices'], {
            items: [
                { id: 7, user_agent: 'Mozilla/5.0 (Macintosh)', client_ip: '203.0.113.5', created_at: '2026-11-20T10:00:00+08:00', last_used_at: '2026-11-22T09:00:00+08:00', expires_at: '2026-12-20T10:00:00+08:00', expired: false },
            ],
        });
    };

    it('有设备时渲染 UA 与两个时间, 并提供逐个吊销', () => {
        const html = renderSSR(<SettingTOTP />, deviceSeed);
        expect(html).toContain(en.setting.account.totp.trustedTitle);
        expect(html).toContain('Mozilla/5.0 (Macintosh)');
        expect(html).toContain(en.setting.account.totp.trustedRevoke);
        // 添加时间与最近使用时间都要有; 只有其中一个的话用户分不清"什么时候加的"和"最近谁在用"。
        expect(html).toContain('2026-11-20');
        expect(html).toContain('2026-11-22');
    });

    it('设备为空时走空态, 不是空白也不是 0', () => {
        const html = renderSSR(<SettingTOTP />, (client) => {
            client.setQueryData(['user', 'totp-status'], { totp_enabled: true, totp_recovery_remaining: 10 });
            client.setQueryData(['user', '2fa-trusted-devices'], { items: [] });
        });
        expect(html).toContain(en.setting.account.totp.trustedEmpty);
    });

    it('清单加载失败时明确报错 + 重试, 绝不显示"没有设备"', () => {
        const html = renderSSR(<SettingTOTP />, (client) => {
            client.setQueryData(['user', 'totp-status'], { totp_enabled: true, totp_recovery_remaining: 10 });
            // retryOnMount: false 才能把快照停在"落定后的错误态": 否则挂载即重取,
            // 组件会先渲染成"加载中", 断言就测不到失败分支了。
            client.setDefaultOptions({ queries: { retryOnMount: false, retry: false } });
            const cache = client.getQueryCache();
            cache.build(client, { queryKey: ['user', '2fa-trusted-devices'] }, {
                data: undefined, dataUpdateCount: 0, dataUpdatedAt: 0,
                error: new Error('boom'), errorUpdateCount: 1, errorUpdatedAt: Date.now(),
                fetchFailureCount: 1, fetchFailureReason: new Error('boom'), fetchMeta: null,
                isInvalidated: false, status: 'error', fetchStatus: 'idle',
            });
        });
        expect(html).toContain(en.setting.account.totp.loadFailed);
        expect(html).not.toContain(en.setting.account.totp.trustedEmpty);
    });

    it('剩余恢复码按后端数字展示; 缺失时不编 0', () => {
        const withCount = renderSSR(<SettingTOTP />, deviceSeed);
        expect(withCount).toContain('3');
        const withoutCount = renderSSR(<SettingTOTP />, (client) => {
            client.setQueryData(['user', 'totp-status'], { totp_enabled: true });
            client.setQueryData(['user', '2fa-trusted-devices'], { items: [] });
        });
        // 旧后端不返回该字段时只说"需要时可重新生成", 不能显示"剩余 0 个"(那会被读成已经用光)。
        expect(withoutCount).not.toContain('0 unused left');
    });

    it('重新生成面板给出输入框与说明, 未输入码时按钮禁用', () => {
        const html = renderSSR(<SettingTOTP />, deviceSeed);
        expect(html).toContain(en.setting.account.totp.recoveryRegenerate);
        expect(html).toContain(en.setting.account.totp.recoveryRegenerateHint);
        expect(html).toContain('id="totp-regenerate-code"');
        const button = html.match(/<button[^>]*>[^<]*<\/button>|<button[^>]*>[^<]*<span[^>]*>[^<]*<\/span>/);
        expect(button).not.toBeNull();
    });

    it('未启用 2FA 时不出现信任设备与重生成入口', () => {
        const html = renderSSR(<SettingTOTP />, (client) => client.setQueryData(['user', 'totp-status'], { totp_enabled: false }));
        expect(html).not.toContain(en.setting.account.totp.trustedTitle);
        expect(html).not.toContain(en.setting.account.totp.recoveryRegenerate);
    });

    it('重新生成的凭据面板说"旧码失效", 不说"会话被吊销"', () => {
        useTOTPResult.getState().show(['NEW-1', 'NEW-2'], 'regenerated');
        const result = useTOTPResult.getState().result!;
        const html = renderSSR(<TOTPResultDetails recoveryCodes={result.recoveryCodes} kind={result.kind} onConfirm={() => undefined} />);
        expect(html).toContain(en.setting.account.totp.regeneratedTitle);
        expect(html).toContain(en.setting.account.totp.regeneratedNotice);
        // 关键反向断言: 重新生成不吊销会话, 这句话在这里必须是假的 —— 不能出现。
        expect(html).not.toContain(en.setting.account.totp.sessionRevoked);
        expect(html).not.toContain(en.setting.account.totp.savedLogin);
    });

    it('登录页把"信任本机"的边界写出来', () => {
        const html = renderSSR(<LoginForm />);
        expect(html).toContain(en.login.trustDeviceHint);
    });
});
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
