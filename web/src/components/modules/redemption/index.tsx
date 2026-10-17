import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, Ban, ChevronLeft, ChevronRight, Copy, Download, Loader2, Plus, RefreshCw, Ticket } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import {
    useCreateRedemptionBatch,
    useDisableRedemptionCode,
    useRedemptionBatches,
    useRedemptionCodes,
    type RedemptionCode,
} from '@/api/redemption';
import { formatAmount, formatDateTime } from '@/lib/format';

const PAGE_SIZE = 20;

// GenerateDialogContent 生成兑换码表单; 成功后留在弹窗内给出新码, 复制或导出后再关闭。
function GenerateDialogContent() {
    const t = useTranslations('redemption');
    const { setIsOpen } = useMorphingDialog();
    const create = useCreateRedemptionBatch();
    const [name, setName] = useState('');
    const [credits, setCredits] = useState('10');
    const [count, setCount] = useState('10');
    const [prefix, setPrefix] = useState('');
    const [perUserLimit, setPerUserLimit] = useState('0');
    const [expiresAt, setExpiresAt] = useState('');
    const [generated, setGenerated] = useState<RedemptionCode[]>([]);

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (create.isPending) return;
        create.mutate(
            {
                name: name.trim(),
                credits: Number(credits),
                count: Number(count),
                prefix: prefix.trim(),
                per_user_limit: Number(perUserLimit),
                expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
            },
            {
                onSuccess: (data) => {
                    setGenerated(data.codes);
                    toast.success(t('toast.generated', { count: data.codes.length }));
                },
                onError: (error) => toast.error(t('toast.generateFailed'), { description: error.message }),
            },
        );
    };

    // copyCodes 全部码复制到剪贴板, 发码走线下渠道的场景最常用。
    const copyCodes = () => {
        void navigator.clipboard.writeText(generated.map((item) => item.code).join('\n'));
        toast.success(t('toast.copied'));
    };

    // exportCodes 导出 CSV, 便于存档与批量发码。
    const exportCodes = () => {
        const lines = ['code,credits,status', ...generated.map((item) => `${item.code},${item.credits},${item.status}`)];
        const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8' }));
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = 'redemption-codes.csv';
        anchor.click();
        URL.revokeObjectURL(url);
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                {generated.length > 0 ? (
                    <div className="space-y-4">
                        <p className="text-sm text-muted-foreground">{t('result.desc', { count: generated.length })}</p>
                        <div className="max-h-64 overflow-auto rounded-2xl border border-border bg-muted/40 p-3 font-mono text-xs">
                            {generated.map((item) => (
                                <div key={item.id} className="py-0.5">{item.code}</div>
                            ))}
                        </div>
                        <div className="flex flex-wrap gap-2">
                            <Button type="button" variant="outline" size="sm" className="rounded-xl" onClick={copyCodes}>
                                <Copy className="size-4" />{t('result.copy')}
                            </Button>
                            <Button type="button" variant="outline" size="sm" className="rounded-xl" onClick={exportCodes}>
                                <Download className="size-4" />{t('result.export')}
                            </Button>
                            <Button type="button" size="sm" className="rounded-xl" onClick={() => setIsOpen(false)}>{t('result.close')}</Button>
                        </div>
                    </div>
                ) : (
                    <form onSubmit={handleSubmit} className="space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="redemption-name">{t('form.name')}</Label>
                            <Input id="redemption-name" value={name} onChange={(event) => setName(event.target.value)} className="rounded-xl" autoComplete="off" />
                        </div>
                        <div className="grid grid-cols-2 gap-3">
                            <div className="space-y-2">
                                <Label htmlFor="redemption-credits">{t('form.credits')}</Label>
                                <Input id="redemption-credits" type="number" min="0" step="0.01" value={credits} onChange={(event) => setCredits(event.target.value)} className="rounded-xl" />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="redemption-count">{t('form.count')}</Label>
                                <Input id="redemption-count" type="number" min="1" step="1" value={count} onChange={(event) => setCount(event.target.value)} className="rounded-xl" />
                            </div>
                        </div>
                        <div className="grid grid-cols-2 gap-3">
                            <div className="space-y-2">
                                <Label htmlFor="redemption-prefix">{t('form.prefix')}</Label>
                                <Input id="redemption-prefix" value={prefix} onChange={(event) => setPrefix(event.target.value)} className="rounded-xl" autoComplete="off" maxLength={8} />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="redemption-limit">{t('form.perUserLimit')}</Label>
                                <Input id="redemption-limit" type="number" min="0" step="1" value={perUserLimit} onChange={(event) => setPerUserLimit(event.target.value)} className="rounded-xl" />
                            </div>
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="redemption-expires">{t('form.expiresAt')}</Label>
                            <Input id="redemption-expires" type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} className="rounded-xl" />
                            <p className="text-xs text-muted-foreground">{t('form.expiresHint')}</p>
                        </div>
                        <div className="flex justify-end gap-2">
                            <Button type="button" variant="outline" size="sm" className="rounded-xl" onClick={() => setIsOpen(false)}>{t('form.cancel')}</Button>
                            <Button type="submit" size="sm" className="rounded-xl" disabled={create.isPending || name.trim() === ''}>
                                {create.isPending && <Loader2 className="size-4 animate-spin" />}
                                {t('form.submit')}
                            </Button>
                        </div>
                    </form>
                )}
            </MorphingDialogDescription>
        </div>
    );
}

// GenerateButton 工具栏上的生成兑换码入口。
function GenerateButton() {
    const t = useTranslations('redemption');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90">
                <Plus className="size-4" />
                {t('generate')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <GenerateDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// Redemption 兑换码管理页: 批次发放进度 + 码明细 + 生成与禁用。
export default function Redemption() {
    const t = useTranslations('redemption');
    const batches = useRedemptionBatches();
    const [batchId, setBatchId] = useState(0);
    const [status, setStatus] = useState('');
    const [page, setPage] = useState(0);
    const codes = useRedemptionCodes(batchId, status, page * PAGE_SIZE);
    const disable = useDisableRedemptionCode();

    const items = codes.data?.items ?? [];
    const total = codes.data?.total ?? 0;
    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    // handleDisable 禁用未兑兑换码; 已兑的不可逆, 后端会拒绝并透出原因。
    const handleDisable = (id: number) => {
        disable.mutate(id, {
            onSuccess: () => toast.success(t('toast.disabled')),
            onError: (error) => toast.error(t('toast.disableFailed'), { description: error.message }),
        });
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Ticket className="size-5" />
                    {t('title')}
                </h2>
                <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => { void batches.refetch(); void codes.refetch(); }} disabled={codes.isRefetching} className="rounded-xl">
                        <RefreshCw className={'size-4' + (codes.isRefetching ? ' animate-spin' : '')} />
                        {t('refresh')}
                    </Button>
                    <GenerateButton />
                </div>
            </div>

            <div className="shrink-0 overflow-x-auto rounded-3xl border border-border bg-card">
                <table className="w-full min-w-[820px] text-sm">
                    <thead className="text-left text-xs text-muted-foreground"><tr>
                        <th className="px-4 py-3 font-medium">{t('batch.name')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.credits')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.count')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.used')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.limit')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.expires')}</th>
                        <th className="px-4 py-3 font-medium">{t('batch.created')}</th>
                    </tr></thead>
                    <tbody>
                        {(batches.data?.items ?? []).length === 0 ? (
                            <tr><td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">{t('batch.empty')}</td></tr>
                        ) : (batches.data?.items ?? []).map((batch) => (
                            <tr key={batch.id} className="border-t border-border">
                                <td className="px-4 py-3 font-medium">{batch.name}{batch.prefix ? <span className="ml-1 text-xs text-muted-foreground">({batch.prefix}-)</span> : null}</td>
                                <td className="px-4 py-3 tabular-nums">{formatAmount(batch.credits)}</td>
                                <td className="px-4 py-3 tabular-nums">{batch.count}</td>
                                <td className="px-4 py-3 tabular-nums text-muted-foreground">
                                    {t('batch.usedOf', { used: batch.used, enabled: batch.enabled, disabled: batch.disabled })}
                                </td>
                                <td className="px-4 py-3">{batch.per_user_limit > 0 ? batch.per_user_limit : t('batch.unlimited')}</td>
                                <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(batch.expires_at, t('batch.never'))}</td>
                                <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(batch.created_at, '-')}</td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <div className="text-sm font-medium text-card-foreground">{t('codes.title')}</div>
                <Select value={batchId ? String(batchId) : 'all'} onValueChange={(value) => { setBatchId(value === 'all' ? 0 : Number(value)); setPage(0); }}>
                    <SelectTrigger className="w-44 rounded-xl"><SelectValue placeholder={t('codes.batchAll')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('codes.batchAll')}</SelectItem>
                        {(batches.data?.items ?? []).map((batch) => (
                            <SelectItem key={batch.id} value={String(batch.id)}>{batch.name}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={status || 'all'} onValueChange={(value) => { setStatus(value === 'all' ? '' : value); setPage(0); }}>
                    <SelectTrigger className="w-36 rounded-xl"><SelectValue placeholder={t('codes.statusAll')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('codes.statusAll')}</SelectItem>
                        <SelectItem value="enabled">{t('status.enabled')}</SelectItem>
                        <SelectItem value="used">{t('status.used')}</SelectItem>
                        <SelectItem value="disabled">{t('status.disabled')}</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {codes.isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                ) : codes.isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void codes.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                ) : items.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[720px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                <th className="px-4 py-3 font-medium">{t('codes.code')}</th>
                                <th className="px-4 py-3 font-medium">{t('codes.credits')}</th>
                                <th className="px-4 py-3 font-medium">{t('codes.status')}</th>
                                <th className="px-4 py-3 font-medium">{t('codes.usedBy')}</th>
                                <th className="px-4 py-3 font-medium">{t('codes.usedAt')}</th>
                                <th className="px-4 py-3 font-medium">{t('codes.action')}</th>
                            </tr></thead>
                            <tbody>
                                {items.map((item) => (
                                    <tr key={item.id} className="border-t border-border">
                                        <td className="px-4 py-3 font-mono text-xs">{item.code}</td>
                                        <td className="px-4 py-3 tabular-nums">{formatAmount(item.credits)}</td>
                                        <td className="px-4 py-3">{t('status.' + item.status)}</td>
                                        <td className="px-4 py-3 tabular-nums text-muted-foreground">{item.used_by ?? '-'}</td>
                                        <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(item.used_at, '-')}</td>
                                        <td className="px-4 py-3">
                                            {item.status === 'enabled' ? (
                                                <Button variant="outline" size="sm" className="h-8 rounded-xl" onClick={() => handleDisable(item.id)}>
                                                    <Ban className="size-4" />{t('codes.disable')}
                                                </Button>
                                            ) : '-'}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <div className="text-xs text-muted-foreground">{t('codes.total', { total })}</div>
                <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" className="rounded-xl" disabled={page <= 0} onClick={() => setPage((value) => value - 1)}>
                        <ChevronLeft className="size-4" />{t('codes.prev')}
                    </Button>
                    <span className="text-xs tabular-nums text-muted-foreground">{page + 1} / {totalPages}</span>
                    <Button variant="outline" size="sm" className="rounded-xl" disabled={page + 1 >= totalPages} onClick={() => setPage((value) => value + 1)}>
                        {t('codes.next')}<ChevronRight className="size-4" />
                    </Button>
                </div>
            </div>
        </div>
    );
}
