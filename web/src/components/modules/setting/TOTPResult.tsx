import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { useTOTPResult } from './totp-result';

export function TOTPResult() {
    const { result, clear } = useTOTPResult();
    return result ? <TOTPResultDetails recoveryCodes={result.recoveryCodes} onConfirm={clear} /> : null;
}

export function TOTPResultDetails({ recoveryCodes, onConfirm }: { recoveryCodes: string[]; onConfirm: () => void }) {
    const t = useTranslations('setting.account.totp');
    return <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4">
        <section role="dialog" aria-modal="true" aria-labelledby="totp-result-title"
            className="max-h-[90vh] w-full max-w-lg overflow-auto rounded-2xl border bg-card p-6 space-y-4">
            <h2 id="totp-result-title" className="text-lg font-bold">{t(recoveryCodes.length ? 'enabledTitle' : 'disabledTitle')}</h2>
            <p>{t('sessionRevoked')}</p>
            {recoveryCodes.length > 0 && <>
                <p className="text-sm">{t('recoveryWarning')}</p>
                <ul aria-label={t('recoveryCodes')} className="grid grid-cols-2 gap-2 select-all font-mono text-sm">
                    {recoveryCodes.map((code) => <li key={code}>{code}</li>)}
                </ul>
            </>}
            <Button autoFocus onClick={onConfirm}>{t(recoveryCodes.length ? 'savedLogin' : 'loginAgain')}</Button>
        </section>
    </div>;
}
