import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Loader2, RefreshCw, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { accountImport, accountRefreshKey, type AccountImportResult, type AccountPlatform } from '@/api/channel';

/**
 * AccountImport 导入上游账号 JSON 作为渠道凭据。
 *
 * 只做导入与「导入后立刻验证」两件事, 不碰模型授权: 账号进来之后怎么分配模型权限, 是凭据页的活。
 * 把两件事混在一个弹层里, 用户会以为「导入」同时决定了「能对哪些模型转发」, 而实际不会。
 *
 * 平台列表写死在前端而不是从后端拉: 支持哪些平台由代码里的解析分支决定,
 * 后端没有「列出我支持哪些平台」的接口, 硬造一个只会多一份需要同步的清单。
 */
export function AccountImport({ channelId, onImported }: { channelId: number; onImported?: () => void }) {
    const t = useTranslations('channel.account');
    const [platform, setPlatform] = useState<AccountPlatform>('codex');
    const [name, setName] = useState('');
    const [raw, setRaw] = useState('');
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState<AccountImportResult | null>(null);

    const submit = async () => {
        if (!name.trim()) {
            toast.error(t('nameRequired'));
            return;
        }
        if (!raw.trim()) {
            toast.error(t('rawRequired'));
            return;
        }
        setBusy(true);
        try {
            const imported = await accountImport({ channel_id: channelId, platform, name: name.trim(), raw });
            setResult(imported);
            // 带 warning 表示已导入但首次刷新失败: 用警告色而不是失败色, 因为数据确实落库了。
            if (imported.warning) toast.warning(imported.warning);
            else toast.success(t('imported'));
            onImported?.();
        } catch (error) {
            toast.error((error as Error).message);
        } finally {
            setBusy(false);
        }
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">
                <Button size="sm" variant="outline" className="rounded-xl">
                    <Upload className="size-3.5" />
                    {t('open')}
                </Button>
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-full max-w-lg rounded-2xl border bg-card p-6 shadow-lg">
                    <MorphingDialogTitle className="text-lg font-semibold">{t('title')}</MorphingDialogTitle>
                    <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                        {t('hint')}
                    </MorphingDialogDescription>

                    <div className="mt-4 space-y-3">
                        <div className="space-y-1.5">
                            <Label>{t('platform')}</Label>
                            <div className="flex flex-wrap gap-2">
                                {(['codex', 'kiro', 'gemini', 'grok', 'claude'] as AccountPlatform[]).map((item) => (
                                    <Button
                                        key={item}
                                        type="button"
                                        size="sm"
                                        variant={platform === item ? 'default' : 'outline'}
                                        className="rounded-lg"
                                        onClick={() => setPlatform(item)}
                                    >
                                        {item}
                                    </Button>
                                ))}
                            </div>
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="account-name">{t('name')}</Label>
                            <Input
                                id="account-name"
                                value={name}
                                onChange={(event) => setName(event.target.value)}
                                placeholder={t('namePlaceholder')}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="account-raw">{t('raw')}</Label>
                            <Textarea
                                id="account-raw"
                                value={raw}
                                onChange={(event) => setRaw(event.target.value)}
                                placeholder={t('rawPlaceholder')}
                                rows={8}
                                className="font-mono text-xs"
                            />
                            <p className="text-xs text-muted-foreground">{t(`rawHint.${platform}`)}</p>
                        </div>
                    </div>

                    {result && (
                        <div className="mt-4 space-y-1 rounded-xl border bg-muted/40 p-3 text-xs">
                            <div className="font-medium">{t('resultTitle')}</div>
                            <div>{t('resultKind', { kind: result.kind })}</div>
                            {result.email && <div>{t('resultEmail', { email: result.email })}</div>}
                            {result.expires_at && <div>{t('resultExpires', { at: result.expires_at })}</div>}
                            {!result.auto_renewable && (
                                <div className="text-amber-600 dark:text-amber-500">{t('cookieWarning')}</div>
                            )}
                            {result.refreshed_now && <div>{t('resultRefreshed')}</div>}
                        </div>
                    )}

                    <div className="mt-5 flex items-center justify-end gap-2">
                        <MorphingDialogClose className="static rounded-xl px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
                            {t('cancel')}
                        </MorphingDialogClose>
                        {result && (
                            <Button
                                type="button"
                                variant="outline"
                                onClick={async () => {
                                    try {
                                        await accountRefreshKey(result.channel_key_id);
                                        toast.success(t('refreshed'));
                                    } catch (error) {
                                        toast.error((error as Error).message);
                                    }
                                }}
                            >
                                <RefreshCw className="size-3.5" />
                                {t('refreshNow')}
                            </Button>
                        )}
                        <Button type="button" onClick={submit} disabled={busy}>
                            {busy && <Loader2 className="size-3.5 animate-spin" />}
                            {t('submit')}
                        </Button>
                    </div>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
