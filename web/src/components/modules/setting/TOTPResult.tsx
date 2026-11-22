import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { useTOTPResult, type TOTPResultKind } from './totp-result';

export function TOTPResult() {
    const { result, clear } = useTOTPResult();
    return result
        ? <TOTPResultDetails recoveryCodes={result.recoveryCodes} kind={result.kind} onConfirm={clear} />
        : null;
}

export function TOTPResultDetails({ recoveryCodes, kind = 'enabled', onConfirm }: {
    recoveryCodes: string[];
    kind?: TOTPResultKind;
    onConfirm: () => void;
}) {
    const t = useTranslations('setting.account.totp');
    // 三档文案: 启用(会被踢下线) / 解绑(会被踢下线, 但没有恢复码) / 重新生成(不被踢下线)。
    const titleKey = kind === 'regenerated' ? 'regeneratedTitle' : recoveryCodes.length ? 'enabledTitle' : 'disabledTitle';
    const noticeKey = kind === 'regenerated' ? 'regeneratedNotice' : 'sessionRevoked';
    const confirmKey = kind === 'regenerated' ? 'saved' : recoveryCodes.length ? 'savedLogin' : 'loginAgain';
    return <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4">
        <section role="dialog" aria-modal="true" aria-labelledby="totp-result-title"
            className="max-h-[90vh] w-full max-w-lg overflow-auto rounded-2xl border bg-card p-6 space-y-4">
            <h2 id="totp-result-title" className="text-lg font-bold">{t(titleKey)}</h2>
            <p>{t(noticeKey)}</p>
            {recoveryCodes.length > 0 && <>
                <p className="text-sm">{t('recoveryWarning')}</p>
                <ul aria-label={t('recoveryCodes')} className="grid grid-cols-2 gap-2 select-all font-mono text-sm">
                    {recoveryCodes.map((code) => <li key={code}>{code}</li>)}
                </ul>
            </>}
            <Button autoFocus onClick={onConfirm}>{t(confirmKey)}</Button>
        </section>
    </div>;
}
