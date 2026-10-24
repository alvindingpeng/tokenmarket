import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { ShieldCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ApiError, setStepUpHandler, verifyStepUpPassword } from '@/api/client';

// attempt 一次密码确认: resolve 把结论交回那笔被拦下的请求(凭证 / null=取消)。
type StepUpAttempt = { resolve: (token: string | null) => void };

// StepUpDialog 高危操作前的密码确认弹窗: 遇到明确的 step-up 错误(428 + step_up_required)时,
// 通用请求层会调用这里注册的 handler。本组件只负责"再确认一次身份"这一件事:
//
//   - 拿到密码 -> 走 /api/v1/user/step-up 换短期凭证 -> 把凭证交回请求层, 由它原样重发被拦下的那一次;
//   - 口令不对/被限流 -> 就地显示服务端说明, 由用户决定再试还是取消(不会自动重试);
//   - 取消 -> 交回 null, 请求层原样抛出 step-up 错误, 原请求根本不发。
//
// 取消可以发生在验证在途时(Escape 是唯一的在途入口, 取消键此时禁用): 迟到的验证响应
// 属于上一代确认, 换来的凭证整体作废, 不得替任何后一个请求盖章(settle 的代际守卫)。
// 密码只在内存里过一道手, 不进日志、不进存储、不进任何持久化状态。
export function StepUpDialog() {
    const t = useTranslations('stepUp');
    const [open, setOpen] = useState(false);
    const [password, setPassword] = useState('');
    const [error, setError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    // attemptRef 当前这一代确认; settle 只认这一代的结论。
    const attemptRef = useRef<StepUpAttempt | null>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    // settle 结算一代确认: 迟到的验证结果/取消不能替新一代做主。
    const settle = (which: StepUpAttempt | null, token: string | null) => {
        if (attemptRef.current !== which || which === null) return;
        attemptRef.current = null;
        setOpen(false);
        setPassword(''); // 口令不留在组件状态里等着下次渲染。
        setError(null);
        which.resolve(token);
    };

    useEffect(() => {
        setStepUpHandler(() => new Promise<string | null>((resolve) => {
            attemptRef.current = { resolve };
            setPassword('');
            setError(null);
            setBusy(false);
            setOpen(true);
            globalThis.setTimeout(() => inputRef.current?.focus(), 0);
        }));
        return () => {
            // 卸载时把挂着的请求按"取消"了结, 免得调用方永远 pending。
            settle(attemptRef.current, null);
            setStepUpHandler(null);
        };
    }, []);

    const cancel = () => settle(attemptRef.current, null);

    const confirm = async () => {
        const which = attemptRef.current;
        if (busy || !which) return;
        setBusy(true);
        setError(null);
        try {
            const token = await verifyStepUpPassword(password);
            settle(which, token); // 已被取消/换代时 settle 直接吞掉, 迟到凭证作废。
        } catch (caught) {
            if (attemptRef.current !== which) return;
            // 验证失败留在弹窗里重试; 错误文案直接采用服务端说明(口令错/被限流各有各的话)。
            setError(caught instanceof ApiError ? caught.message : t('errorGeneric'));
            setPassword('');
            inputRef.current?.focus();
        } finally {
            // A cancelled older attempt must not clear the busy state of a newer attempt.
            if (attemptRef.current === which) setBusy(false);
        }
    };

    if (!open) return null;

    return (
        <div className="fixed inset-0 z-70 flex items-center justify-center overflow-y-auto bg-background/80 p-4 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label={t('title')}>
            <div className="w-full max-w-sm space-y-4 rounded-3xl border border-border bg-card p-6">
                <div className="flex items-center gap-2">
                    <ShieldCheck className="size-5 text-primary" />
                    <h2 className="text-lg font-bold text-card-foreground">{t('title')}</h2>
                </div>
                <p className="text-sm text-muted-foreground">{t('description')}</p>
                <div className="space-y-2">
                    <Input
                        ref={inputRef}
                        type="password"
                        autoComplete="current-password"
                        placeholder={t('passwordPlaceholder')}
                        value={password}
                        aria-invalid={error ? true : undefined}
                        onChange={(event) => setPassword(event.target.value)}
                        onKeyDown={(event) => {
                            if (event.key === 'Enter') void confirm();
                            // Escape 是验证在途时唯一的取消入口(取消键此时禁用)。
                            if (event.key === 'Escape') cancel();
                        }}
                    />
                    {error ? <p className="text-sm text-destructive">{error}</p> : null}
                    <p className="text-xs text-muted-foreground">{t('hint')}</p>
                </div>
                <div className="flex flex-wrap justify-end gap-2">
                    <Button type="button" variant="outline" className="rounded-xl" onClick={cancel} disabled={busy}>
                        {t('cancel')}
                    </Button>
                    <Button type="button" className="rounded-xl" onClick={() => void confirm()} disabled={busy || password.length === 0}>
                        {busy ? t('busy') : t('confirm')}
                    </Button>
                </div>
            </div>
        </div>
    );
}
