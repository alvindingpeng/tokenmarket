import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { QRCodeSVG } from 'qrcode.react';
import { apiRequest } from '@/api/client';
import { useAuthStore, type UserView } from '@/api/user';
import {
    setupTOTP,
    enableTOTP,
    disableTOTP,
    listTrustedDevices,
    revokeAllTrustedDevices,
    revokeTrustedDevice,
    regenerateRecoveryCodes,
    type TOTPSetup,
} from '@/api/totp';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useTOTPResult } from './totp-result';
import { canRegenerate, clampRecoveryCount, deviceLabel, formatDeviceTime } from './totpDevices';

export function TOTPSetupDetails({ setup }: { setup: TOTPSetup }) {
    const t = useTranslations('setting.account.totp');
    return <div className="space-y-3">
        <p className="text-sm">{t('scan')}</p>
        <div className="w-fit rounded-lg bg-white p-3"><QRCodeSVG value={setup.otpauth_url} size={192} title={t('qrLabel')} /></div>
        <p className="text-sm">{t('secretLabel')}</p>
        <code className="block break-all select-all">{setup.secret}</code>
        <p className="text-xs text-muted-foreground">{t('secretWarning')}</p>
    </div>;
}

export function SettingTOTP() {
    const t = useTranslations('setting.account.totp');
    const isAPIKeyAuth = useAuthStore((state) => state.isAPIKeyAuth);
    const profile = useQuery({
        queryKey: ['user', 'totp-status'],
        queryFn: ({ signal }) => apiRequest<UserView>('/api/v1/user/status', { signal }),
        enabled: !isAPIKeyAuth,
        retry: false,
    });
    const [setup, setSetup] = useState<TOTPSetup | null>(null);
    const [code, setCode] = useState('');
    const [pending, setPending] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const request = useRef<AbortController | null>(null);
    useEffect(() => () => request.current?.abort(), []);

    const run = async (action: 'setup' | 'enable' | 'disable') => {
        if (request.current) return;
        const controller = new AbortController();
        request.current = controller;
        setPending(true);
        setError(null);
        try {
            if (action === 'setup') {
                setSetup(await setupTOTP(controller.signal));
            } else {
                const recoveryCodes = action === 'enable'
                    ? (await enableTOTP(code.trim(), controller.signal)).recovery_codes
                    : (await disableTOTP(code.trim(), controller.signal), []);
                // Publish before logout: this global, memory-only result survives
                // account unmount and any subsequent unauthorized responses.
                useTOTPResult.getState().show(recoveryCodes);
                setSetup(null);
                setCode('');
                useAuthStore.getState().logout();
            }
        } catch (cause) {
            if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t('failed'));
        } finally {
            request.current = null;
            if (!controller.signal.aborted) setPending(false);
        }
    };

    const enabled = profile.data?.totp_enabled === true;
    return <section className="rounded-3xl border border-border bg-card p-6 space-y-4" aria-labelledby="totp-title">
        <h2 id="totp-title" className="text-lg font-bold">{t('title')}</h2>
        <p className="text-sm text-muted-foreground">{t('description')}</p>
        {isAPIKeyAuth ? <p>{t('passwordOnly')}</p> : <>
            {profile.isPending && <p>{t('loading')}</p>}
            {profile.isError && <div role="alert"><p>{t('loadFailed')}</p><Button variant="outline" onClick={() => void profile.refetch()}>{t('retry')}</Button></div>}
            {profile.data && <>
                <p>{t(enabled ? 'enabled' : 'disabled')}</p>
                {enabled && <TrustedDevices />}
                {canRegenerate(enabled) && <RegenerateRecovery remaining={profile.data.totp_recovery_remaining} />}
                {!enabled && !setup && <Button disabled={pending} onClick={() => void run('setup')}>{t(pending ? 'working' : 'setup')}</Button>}
                {setup && <TOTPSetupDetails setup={setup} />}
                {(enabled || setup) && <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); void run(enabled ? 'disable' : 'enable'); }}>
                    <label htmlFor="totp-confirm" className="text-sm">{t('code')}</label>
                    <Input id="totp-confirm" autoComplete="one-time-code" value={code} onChange={(event) => setCode(event.target.value)} disabled={pending} required />
                    <p className="text-xs text-muted-foreground">{t('reloginWarning')}</p>
                    <div className="flex gap-2">
                        <Button type="submit" disabled={pending || !code.trim()} variant={enabled ? 'destructive' : 'default'}>{t(pending ? 'working' : enabled ? 'disable' : 'enable')}</Button>
                        {setup && <Button type="button" variant="outline" disabled={pending} onClick={() => { setSetup(null); setCode(''); setError(null); }}>{t('cancel')}</Button>}
                    </div>
                </form>}
            </>}
        </>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </section>;
}

// TrustedDevices 列出"30 天内免输动态码"的浏览器并允许逐个/全部吊销。
//
// 四态规则: 加载中 / 失败+重试 / 空 / 列表。任何一档都不显示"0 台"来冒充成功 ——
// 拿不到清单时用户没法判断有没有陌生设备, 那正是这个功能的用途。
function TrustedDevices() {
    const t = useTranslations('setting.account.totp');
    const queryClient = useQueryClient();
    const query = useQuery({
        queryKey: ['user', '2fa-trusted-devices'],
        queryFn: ({ signal }) => listTrustedDevices(signal),
        retry: false,
    });
    const [error, setError] = useState<string | null>(null);
    const [pending, setPending] = useState<number | 'all' | null>(null);
    // 吊销是安全写: 服务端要求 step-up, apiRequest 会自动弹密码确认并重发同一请求。
    const revoke = async (id: number | 'all') => {
        if (pending !== null) return;
        setPending(id);
        setError(null);
        try {
            if (id === 'all') await revokeAllTrustedDevices();
            else await revokeTrustedDevice(id);
            await queryClient.invalidateQueries({ queryKey: ['user', '2fa-trusted-devices'] });
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : t('trustedFailed'));
        } finally {
            setPending(null);
        }
    };
    const items = query.data?.items ?? [];
    return <div className="space-y-2 rounded-2xl border border-border/60 p-3">
        <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-medium">{t('trustedTitle')}</h3>
            {/* 一键吊销只在真有设备时出现: 空列表上摆一个"全部吊销"只会让人以为漏看了什么。 */}
            {items.length > 1 && <Button variant="outline" disabled={pending !== null} onClick={() => void revoke('all')}>{t('trustedRevokeAll')}</Button>}
        </div>
        <p className="text-xs text-muted-foreground">{t('trustedHint')}</p>
        {query.isPending && <p>{t('loading')}</p>}
        {query.isError && <div role="alert"><p>{t('loadFailed')}</p><Button variant="outline" onClick={() => void query.refetch()}>{t('retry')}</Button></div>}
        {query.isSuccess && items.length === 0 && <p className="text-sm text-muted-foreground">{t('trustedEmpty')}</p>}
        {items.length > 0 && <ul className="space-y-2">
            {items.map((device) => <li key={device.id} className="flex items-start justify-between gap-2 rounded-xl bg-muted/40 p-2">
                <div className="min-w-0">
                    {/* UA 与 IP 只为让用户认出"这是我的电脑还是别人的": 它们都不参与鉴权。 */}
                    <p className="truncate text-sm">{deviceLabel(device)}</p>
                    <p className="text-xs text-muted-foreground">
                        {t('trustedAdded', { when: formatDeviceTime(device.created_at) || '—' })}
                        {' · '}
                        {device.last_used_at ? t('trustedLastUsed', { when: formatDeviceTime(device.last_used_at) || '—' }) : t('trustedNeverUsed')}
                    </p>
                </div>
                <Button variant="outline" disabled={pending !== null} onClick={() => void revoke(device.id)}>{t('trustedRevoke')}</Button>
            </li>)}
        </ul>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>;
}

// RegenerateRecovery 用当前动态码(或一张未用的恢复码)换一批新的恢复码。
//
// 为什么要它: 恢复码原本只能在启用那一刻拿一次, 用光后只能"解绑再重绑", 而解绑要输动态码 ——
// 对丢了设备的人是一道死锁。这里的门槛与解绑同档: 登录态 + 密码确认(step-up) + 一个有效码。
// 成功后旧码整体失效, 新码经全局 TOTPResult 面板展示一次(kind='regenerated', 不会被踢下线)。
function RegenerateRecovery({ remaining }: { remaining?: number }) {
    const t = useTranslations('setting.account.totp');
    const [code, setCode] = useState('');
    const [error, setError] = useState<string | null>(null);
    const mutation = useMutation({
        mutationFn: () => regenerateRecoveryCodes(code.trim()),
        onSuccess: (data) => {
            useTOTPResult.getState().show(data.recovery_codes, 'regenerated');
            setCode('');
            setError(null);
        },
        onError: (cause) => setError(cause instanceof Error ? cause.message : t('recoveryRegenerateFailed')),
    });
    const count = clampRecoveryCount(remaining);
    return <div className="space-y-2 rounded-2xl border border-border/60 p-3">
        <h3 className="text-sm font-medium">{t('recoveryTitle')}</h3>
        {/* 数量缺失(旧后端)时只说"需要时可重新生成", 不编一个 0 —— 0 会被读成"已经用光了"。 */}
        <p className="text-xs text-muted-foreground">
            {remaining === undefined ? t('recoveryRegenerateHint') : t('recoveryRemaining', { count })}
        </p>
        <form className="flex flex-wrap items-center gap-2" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
            <label htmlFor="totp-regenerate-code" className="sr-only">{t('code')}</label>
            <Input id="totp-regenerate-code" autoComplete="one-time-code" value={code}
                onChange={(event) => setCode(event.target.value)} disabled={mutation.isPending} required />
            <Button type="submit" variant="outline" disabled={mutation.isPending || !code.trim()}>
                {mutation.isPending ? t('working') : t('recoveryRegenerate')}
            </Button>
        </form>
        <p className="text-xs text-muted-foreground">{t('recoveryRegenerateHint')}</p>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    </div>;
}
