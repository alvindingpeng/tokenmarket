import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, ArrowDownToLine, ChevronLeft, ChevronRight, ClipboardList, Loader2, RefreshCw, Search, X } from 'lucide-react';
import { auditExportUrl, useAuditActions, useAuditLogs } from '@/api/audit';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

const PAGE_SIZE = 20;
const KNOWN_ACTIONS = [
    'user.create', 'user.update', 'user.balance', 'user.reset-password', 'user.delete',
    'setting.set', 'backup.export', 'backup.import', 'log.clear', 'channel.publish',
    'model.create', 'model.update', 'model.delete', 'model.update-price', 'model.rebuild-price', 'update.core',
];

function formatTime(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function toBoundary(value: string, end: boolean): string | undefined {
    if (!value) return undefined;
    const date = new Date(value + (end ? 'T23:59:59.999' : 'T00:00:00.000'));
    return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
}

export default function Audit() {
    const t = useTranslations('audit');
    const [page, setPage] = useState(0);
    const [keyword, setKeyword] = useState('');
    const [draftKeyword, setDraftKeyword] = useState('');
    const [operator, setOperator] = useState('');
    const [draftOperator, setDraftOperator] = useState('');
    const [action, setAction] = useState('');
    const [from, setFrom] = useState('');
    const [to, setTo] = useState('');
    const { data: actionData } = useAuditActions();
    const actions = useMemo(() => Array.from(new Set([...KNOWN_ACTIONS, ...(actionData ?? [])])).sort(), [actionData]);
    const offset = page * PAGE_SIZE;
    const { data, isLoading, isError, refetch, isRefetching, isFetching } = useAuditLogs({
        limit: PAGE_SIZE,
        offset,
        keyword: keyword || undefined,
        action: action || undefined,
        operator: operator || undefined,
        from: toBoundary(from, false),
        to: toBoundary(to, true),
    });

    const items = data?.items ?? [];
    const total = data?.total ?? 0;
    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
    const actionLabel = (value: string) => KNOWN_ACTIONS.includes(value) ? t('action.' + value) : value;

    const applyTextFilters = () => {
        setKeyword(draftKeyword.trim());
        setOperator(draftOperator.trim());
        setPage(0);
    };
    const resetFilters = () => {
        setDraftKeyword('');
        setKeyword('');
        setDraftOperator('');
        setOperator('');
        setAction('');
        setFrom('');
        setTo('');
        setPage(0);
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <ClipboardList className="size-5" />
                    {t('title')}
                    <span className="text-sm font-normal text-muted-foreground">{t('total', { total })}</span>
                </h2>
                <Button variant="outline" size="sm" onClick={() => void refetch()} disabled={isRefetching} className="rounded-xl">
                    <RefreshCw className={'size-4' + (isRefetching ? ' animate-spin' : '')} />
                    {t('refresh')}
                </Button>
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <div className="relative min-w-0 flex-1 basis-52">
                    <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        className="rounded-xl pl-9 pr-8"
                        placeholder={t('filters.keyword')}
                        value={draftKeyword}
                        onChange={(event) => setDraftKeyword(event.target.value)}
                        onKeyDown={(event) => { if (event.key === 'Enter') applyTextFilters(); }}
                    />
                    {draftKeyword && <button type="button" aria-label={t('filters.clear')} onClick={() => setDraftKeyword('')} className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"><X className="size-4" /></button>}
                </div>
                <Input className="h-9 w-36 rounded-xl" placeholder={t('filters.operator')} value={draftOperator} onChange={(event) => setDraftOperator(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') applyTextFilters(); }} />
                <Select value={action} onValueChange={(value) => { setAction(value); setPage(0); }}>
                    <SelectTrigger className="w-44 rounded-xl"><SelectValue placeholder={t('filters.actionAll')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="">{t('filters.actionAll')}</SelectItem>
                        {actions.map((value) => <SelectItem key={value} value={value}>{actionLabel(value)}</SelectItem>)}
                    </SelectContent>
                </Select>
                <Input type="date" className="h-9 w-36 rounded-xl" aria-label={t('filters.from')} value={from} onChange={(event) => { setFrom(event.target.value); setPage(0); }} />
                <Input type="date" className="h-9 w-36 rounded-xl" aria-label={t('filters.to')} value={to} onChange={(event) => { setTo(event.target.value); setPage(0); }} />
                <Button variant="outline" size="sm" className="rounded-xl" onClick={applyTextFilters}><Search className="size-4" />{t('filters.search')}</Button>
                <Button variant="ghost" size="sm" className="rounded-xl" onClick={resetFilters}>{t('filters.reset')}</Button>
                {/* 导出与当前筛选同源: 先把草稿条件落到生效态再下载, 避免"看到的和导出的不一样"。 */}
                <Button variant="outline" size="sm" className="rounded-xl" onClick={() => window.open(auditExportUrl({
                    keyword: draftKeyword.trim() || undefined,
                    operator: draftOperator.trim() || undefined,
                    action: action || undefined,
                    from: toBoundary(from, false),
                    to: toBoundary(to, true),
                }), '_blank')}><ArrowDownToLine className="size-4" />{t('filters.export')}</Button>
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card pb-24 md:pb-0">
                {isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                ) : isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void refetch()} className="rounded-xl">{t('retry')}</Button></div>
                ) : items.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                ) : (
                    <table className="w-full min-w-[720px] text-sm">
                        <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                            <th className="px-4 py-3 font-medium">{t('table.time')}</th><th className="px-4 py-3 font-medium">{t('table.operator')}</th><th className="px-4 py-3 font-medium">{t('table.action')}</th><th className="px-4 py-3 font-medium">{t('table.target')}</th><th className="px-4 py-3 font-medium">{t('table.detail')}</th>
                        </tr></thead>
                        <tbody>{items.map((item) => <tr key={item.id} className="border-t border-border/60 hover:bg-muted/40">
                            <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{formatTime(item.created_at)}</td>
                            <td className="whitespace-nowrap px-4 py-2.5">{item.username || '-'}</td>
                            <td className="whitespace-nowrap px-4 py-2.5 font-medium">{actionLabel(item.action)}</td>
                            <td className="max-w-40 truncate px-4 py-2.5" title={item.target}>{item.target || '-'}</td>
                            <td className="max-w-72 truncate px-4 py-2.5 text-muted-foreground" title={item.detail}>{item.detail || '-'}</td>
                        </tr>)}</tbody>
                    </table>
                )}
            </div>

            <div className="flex shrink-0 items-center justify-between gap-4">
                <span className="text-xs text-muted-foreground">{t('pagination.page', { page: page + 1, total })}{isFetching && <Loader2 className="ml-2 inline size-3 animate-spin" />}</span>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={() => setPage((value) => Math.max(0, value - 1))} disabled={page === 0} className="rounded-xl"><ChevronLeft className="size-4" />{t('pagination.prev')}</Button>
                    <Button variant="outline" size="sm" onClick={() => setPage((value) => Math.min(totalPages - 1, value + 1))} disabled={page >= totalPages - 1} className="rounded-xl">{t('pagination.next')}<ChevronRight className="size-4" /></Button>
                </div>
            </div>
        </div>
    );
}
