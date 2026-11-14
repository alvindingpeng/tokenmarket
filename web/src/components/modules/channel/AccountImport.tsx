import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Loader2, RefreshCw, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
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


// validateAccountJSON 本地预检账号 JSON。
//
// 与后端 model.AccountJSONParse 的解析分支保持一致: 同一份 JSON
// 在这边能发现的缺字段, 就不必等后端返回 400 再告诉用户。
// 返回空数组表示通过; 否则返回缺失/错误字段的文案列表。
export function validateAccountJSON(platform: AccountPlatform, raw: string): string[] {
    const errors: string[] = [];
    if (!raw.trim()) {
        errors.push('JSON 不能为空');
        return errors;
    }
    let fields: any;
    try {
        fields = JSON.parse(raw);
    } catch {
        errors.push('不是合法的 JSON 对象');
        return errors;
    }
    // 兼容 Codex CLI 导出的外层 tokens 封装
    if (fields.tokens && typeof fields.tokens === 'object') {
        fields = fields.tokens;
    }

    switch (platform) {
        case 'codex': {
            if (!fields.access_token) errors.push('缺少 access_token (CLI 导出应包含 tokens 字段)');
            if (!fields.refresh_token) errors.push('缺少 refresh_token, 该账号无法自动续期');
            break;
        }
        case 'claude': {
            const nested = fields.claudeAiOauth ?? fields;
            if (!nested.accessToken && !nested.access_token) errors.push('缺少 accessToken (Claude Code 凭据文件应为 claudeAiOauth.accessToken)');
            if (!nested.refreshToken && !nested.refresh_token) errors.push('缺少 refreshToken, 该账号无法自动续期');
            break;
        }
        case 'kiro': {
            if (!fields.refreshToken) errors.push('缺少 refreshToken');
            if (!fields.clientId || !fields.clientSecret) errors.push('缺少 clientId/clientSecret');
            break;
        }
        case 'gemini': {
            if (!fields.c_ses) errors.push('缺少 c_ses');
            break;
        }
        case 'grok': {
            if (!fields.sso) errors.push('缺少 sso');
            break;
        }
    }
    return errors;
}

export function AccountImport({ channelId, onImported, channels }: {
    // channelId 有值时是"渠道内导入": 目标固定, 不再让使用者选。
    // 无值时是"独立导入": 从渠道列表页或其它入口进来, 此时必须让他选目标渠道 ——
    // 凭据在数据模型上必须属于某个渠道(ChannelKey.ChannelID 非空), 所以"先选渠道"
    // 是绕不开的, 但至少不必先钻进某个渠道的编辑表单再回来。
    channelId?: number;
    onImported?: () => void;
    // channels 只在独立导入模式下需要: 列出可选的目标渠道。
    channels?: { id: number; name: string }[];
}) {
    const t = useTranslations('channel.account');
    const [platform, setPlatform] = useState<AccountPlatform>('codex');
    const [name, setName] = useState('');
    const [raw, setRaw] = useState('');
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState<AccountImportResult | null>(null);
    // 独立导入模式下的目标渠道。默认取第一个: 只有一个渠道时省掉一次点击, 多个时也要让人看到这一栏。
    const [targetChannel, setTargetChannel] = useState<number | undefined>(channelId ?? channels?.[0]?.id);
    // 本地预检结果: 用户在 Textarea 里贴 JSON 时即时跑, 不等后端。
    // 这是第 6 项: 避免一轮 30 秒往返才发现 "缺 refresh_token"。
    const [validateErrors, setValidateErrors] = useState<string[]>([]);

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
            // 目标渠道在两种模式下都必须是确定的: 拿不到就无法提交, 早在这里挡住比让后端返回一个
        // 看不出原因的错误要好。
        const target = channelId ?? targetChannel;
        if (target === undefined) {
            toast.error(t('channelRequired'));
            return;
        }
        const imported = await accountImport({ channel_id: target, platform, name: name.trim(), raw });
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
                        {/* 目标渠道只在独立导入模式下出现: 渠道内导入时目标已经确定, 再摆一个不可改的
                            下拉只会让人以为可以改。 */}
                        {channelId === undefined && (
                            <div className="space-y-1.5">
                                <Label>{t('targetChannel')}</Label>
                                <Select
                                    value={targetChannel === undefined ? '' : String(targetChannel)}
                                    onValueChange={(value) => setTargetChannel(Number(value))}
                                    disabled={!channels || channels.length === 0}
                                >
                                    <SelectTrigger className="w-full">
                                        <SelectValue placeholder={t('targetChannelPlaceholder')} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {(channels ?? []).map((item) => (
                                            <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                                {(!channels || channels.length === 0) && (
                                    <p className="text-xs text-muted-foreground">{t('targetChannelEmpty')}</p>
                                )}
                            </div>
                        )}
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
                            {/* 失焦/变化时即时预检: 用户贴上 JSON 就能看到缺什么字段, 不必等后端。 */}
                            <Textarea
                                id="account-raw"
                                value={raw}
                                onChange={(event) => {
                                    const v = event.target.value;
                                    setRaw(v);
                                    setValidateErrors(validateAccountJSON(platform, v));
                                }}
                                placeholder={t('rawPlaceholder')}
                                rows={8}
                                className="font-mono text-xs"
                            />
                            <p className="text-xs text-muted-foreground">{t(`rawHint.${platform}`)}</p>
                            {validateErrors.length > 0 && (
                                <div className="mt-1 space-y-0.5">
                                    {validateErrors.map((err, i) => (
                                        <div key={i} className="text-xs text-destructive">
                                            <span className="font-mono">✗</span> {err}
                                        </div>
                                    ))}
                                </div>
                            )}
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
