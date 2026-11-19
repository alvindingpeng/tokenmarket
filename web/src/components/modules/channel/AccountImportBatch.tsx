import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Loader2, Upload, CheckCircle, AlertCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Checkbox } from '@/components/ui/checkbox';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { accountImportBatch, type AccountBatchItem, type AccountBatchResult, type AccountBatchResponse, type AccountPlatform } from '@/api/channel';
import { validateAccountJSON } from './AccountImport';

/**
 * AccountImportBatch 批量导入多个上游账号 JSON 作为渠道凭据。
 *
 * 设计决策:
 * - 批量**不做首刷**: 逐条首刷会 N 次上游调用, 又慢又可能触发风控;
 *   批量的语义是快速录入, 验证留给账号页统一刷新。
 * - 单条导入保持"导入即验证"语义, 因为使用者只导入一个, 等得起。
 * - 输入是每行一个 JSON(紧凑模式)或完整 JSON 数组, 两种都支持, 方便粘贴。
 * - 预检在前端即时跑: 用户贴上去立刻看到哪些行缺字段, 不必等后端返回。
 */

export function AccountImportBatch({ channelId, onImported, channels }: {
    // channelId 有值时是"渠道内批量导入": 目标固定, 不再让使用者选。
    // 无值时是"独立批量导入": 从渠道列表页或其它入口进来, 此时必须让他选目标渠道。
    channelId?: number;
    onImported?: () => void;
    // channels 只在独立导入模式下需要: 列出可选的目标渠道。
    channels?: { id: number; name: string }[];
}) {
    const t = useTranslations('channel.account');
    const [platform, setPlatform] = useState<AccountPlatform>('codex');
    const [text, setText] = useState('');
    const [enabled, setEnabled] = useState(true);
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState<AccountBatchResponse | null>(null);
    const [targetChannel, setTargetChannel] = useState<string | undefined>(channelId ? String(channelId) : channels?.[0]?.id ? String(channels[0].id) : undefined);
    // 预检结果: 每行一个错误数组, 对应 Textarea 里的行号。
    const [validateErrors, setValidateErrors] = useState<string[][]>([]);

    // 解析文本为条目数组: 支持两种格式
    // 1) 每行一个 JSON 对象(紧凑模式, 最常见, 方便从 Excel/文本粘贴)
    // 2) 一个 JSON 数组
    const parseItems = (raw: string): AccountBatchItem[] => {
        const trimmed = raw.trim();
        if (!trimmed) return [];
        // 先尝试解析为数组
        try {
            const parsed = JSON.parse(trimmed);
            if (Array.isArray(parsed)) {
                return parsed.map((item: any, i: number) => ({
                    name: item.name || `${t('batchItem')}${i + 1}`,
                    raw: JSON.stringify(item.raw ?? item), // 兼容 {name, raw} 与直接贴凭据 JSON 两种写法
                }));
            }
        } catch { /* 忽略, 当作逐行解析 */ }
        // 逐行解析: 每行一个 JSON 对象
        return trimmed.split('\n').map((line, i) => {
            const trimmedLine = line.trim();
            // 该 JSON 自带 name 时用它: 自动名(条目 N)在重复导入同一批时会撞名(渠道内唯一),
            // 让使用者能自命名是必要的逃生口.
            let autoName = t('batchItem') + (i + 1);
            try {
                const parsed = JSON.parse(trimmedLine);
                if (parsed && typeof parsed.name === 'string' && parsed.name.trim() !== '') {
                    autoName = parsed.name.trim();
                }
            } catch { /* 非法 JSON 留给预检报错 */ }
            if (!trimmedLine) return { name: autoName, raw: '' };
            return { name: autoName, raw: trimmedLine };
        }).filter(item => item.raw);
    };

    // 即时预检: 文本变化时跑
    const runValidation = (raw: string) => {
        const items = parseItems(raw);
        const errors = items.map(item => validateAccountJSON(platform, item.raw));
        setValidateErrors(errors);
    };

    const submit = async () => {
        const items = parseItems(text);
        if (items.length === 0) {
            toast.error(t('batchEmpty'));
            return;
        }
        // 有任何预检错误就不提交
        if (validateErrors.some(e => e.length > 0)) {
            toast.error(t('batchHasErrors'));
            return;
        }
        const target = channelId ?? (targetChannel ? Number(targetChannel) : undefined);
        if (target === undefined) {
            toast.error(t('channelRequired'));
            return;
        }
        setBusy(true);
        try {
            const imported = await accountImportBatch({
                channel_id: target,
                platform,
                items,
                enabled,
            });
            setResult(imported);
            if (imported.failed > 0) {
                toast.warning(t('batchPartial', { succeeded: imported.succeeded, failed: imported.failed }));
            } else {
                toast.success(t('batchSuccess', { count: imported.succeeded }));
            }
            onImported?.();
        } catch (error) {
            toast.error((error as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const hasErrors = validateErrors.some(e => e.length > 0);
    const errorCount = validateErrors.reduce((sum, e) => sum + e.length, 0);

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">
                <Button size="sm" variant="outline" className="rounded-xl">
                    <Upload className="size-3.5" />
                    {t('batchOpen')}
                </Button>
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-full max-w-2xl rounded-2xl border bg-card p-6 shadow-lg">
                    <MorphingDialogTitle className="text-lg font-semibold">{t('batchTitle')}</MorphingDialogTitle>
                    <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                        {t('batchHint')}
                    </MorphingDialogDescription>

                    <div className="mt-4 space-y-3">
                        {channelId === undefined && (
                            <div className="space-y-1.5">
                                <Label>{t('targetChannel')}</Label>
                                <Select value={targetChannel} onValueChange={setTargetChannel}>
                                    <SelectTrigger className="w-full">
                                        <SelectValue placeholder={t('selectChannel')} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {channels?.map(ch => (
                                            <SelectItem key={ch.id} value={String(ch.id)}>{ch.name}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        )}

                        <div className="space-y-1.5">
                            <Label>{t('platform')}</Label>
                            <div className="flex flex-wrap gap-1">
                                {(['codex', 'kiro', 'claude', 'gemini', 'grok'] as AccountPlatform[]).map(item => (
                                    <Button
                                        key={item}
                                        variant={platform === item ? 'default' : 'outline'}
                                        className="rounded-lg"
                                        onClick={() => { setPlatform(item); runValidation(text); }}
                                    >
                                        {item}
                                    </Button>
                                ))}
                            </div>
                        </div>

                        <div className="space-y-1.5">
                            <Label htmlFor="batch-text">{t('batchPasteLabel', { count: parseItems(text).length })}</Label>
                            <Textarea
                                id="batch-text"
                                value={text}
                                onChange={(event) => {
                                    const v = event.target.value;
                                    setText(v);
                                    runValidation(v);
                                }}
                                placeholder={t('batchPlaceholder')}
                                rows={10}
                                className="font-mono text-xs"
                            />
                            {hasErrors && (
                                <div className="text-xs text-destructive space-y-1">
                                    <div className="flex items-center gap-1">
                                        <AlertCircle className="size-3.5" />
                                        {t('batchValidationErrors', { count: errorCount })}
                                    </div>
                                    {validateErrors.map((itemErrors, i) =>
                                        itemErrors.length > 0 && (
                                            <div key={i} className="ml-6 space-y-0.5">
                                                {itemErrors.map((err, j) => (
                                                    <div key={j} className="flex items-center gap-1">
                                                        <span className="font-mono">✗</span>
                                                        <span>{t('batchItem') + (i + 1)}: {err}</span>
                                                    </div>
                                                ))}
                                            </div>
                                        )
                                    )}
                                </div>
                            )}
                        </div>

                        <div className="flex items-center gap-2">
                            <Checkbox
                                id="batch-enabled"
                                checked={enabled}
                                onCheckedChange={(checked) => setEnabled(checked as boolean)}
                            />
                            <Label htmlFor="batch-enabled" className="cursor-pointer">{t('batchEnabled')}</Label>
                        </div>

                        {result && (
                            <div className="mt-4 space-y-2 rounded-xl border bg-muted/40 p-3 text-xs">
                                <div className="font-medium">{t('batchResultTitle')}</div>
                                <div className="flex items-center gap-2 text-green-600 dark:text-green-500">
                                    <CheckCircle className="size-3.5" />
                                    <span>{t('batchResultSucceeded', { count: result.succeeded })}</span>
                                </div>
                                {result.failed > 0 && (
                                    <div className="flex items-center gap-2 text-destructive">
                                        <AlertCircle className="size-3.5" />
                                        <span>{t('batchResultFailed', { count: result.failed })}</span>
                                    </div>
                                )}
                                <details className="mt-2">
                                    <summary className="cursor-pointer text-sm text-muted-foreground">{t('batchResultDetails')}</summary>
                                    <div className="mt-1 space-y-1 font-mono text-[10px] max-h-40 overflow-auto">
                                        {result.results.map((r: AccountBatchResult, i: number) => (
                                            <div key={i} className={`flex items-center gap-2 ${r.ok ? 'text-green-600 dark:text-green-500' : 'text-destructive'}`}>
                                                <span>{r.ok ? '✓' : '✗'}</span>
                                                <span className="truncate">{r.name}</span>
                                                {r.ok && r.channel_key_id && (
                                                    <span className="text-muted-foreground">id: ${r.channel_key_id}</span>
                                                )}
                                                {!r.ok && r.error && (
                                                    <span className="text-destructive">— ${r.error}</span>
                                                )}
                                            </div>
                                        ))}
                                    </div>
                                </details>
                            </div>
                        )}

                        <div className="mt-5 flex items-center justify-end gap-2">
                            <MorphingDialogClose className="static rounded-xl px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
                                {t('cancel')}
                            </MorphingDialogClose>
                            <Button type="button" onClick={submit} disabled={busy || parseItems(text).length === 0 || hasErrors}>
                                {busy && <Loader2 className="size-3.5 animate-spin" />}
                                {t('batchSubmit')}
                            </Button>
                        </div>
                    </div>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
