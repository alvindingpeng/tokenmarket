import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { QRCodeSVG } from 'qrcode.react';
import { apiRequest } from '@/api/client';
import { useAuthStore, type UserView } from '@/api/user';
import { setupTOTP, enableTOTP, disableTOTP, type TOTPSetup } from '@/api/totp';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useTOTPResult } from './totp-result';

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
