import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronLeft, ChevronRight, Download, Loader2, Logs, RefreshCw, Search, X } from 'lucide-react';
import { logExportUrl, useLogList, useLogs } from '@/api/log';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { LogCard } from './Item';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

// PAGE_SIZE 历史日志每页条数。
const PAGE_SIZE = 20;

// STATUS_OPTIONS 终态筛选选项, 与后端终态定义一致。
const STATUS_OPTIONS = ['success', 'failed', 'canceled', 'committed', 'running'] as const;

// LogHistory 渲染历史日志的分页视图: 关键字/状态筛选 + 页码导航。
function LogHistory() {
    const t = useTranslations('log.history');
    const [page, setPage] = useState(0);
    const [keyword, setKeyword] = useState('');
    const [draftKeyword, setDraftKeyword] = useState('');
    const [status, setStatus] = useState('');
    const [clientIp, setClientIp] = useState('');
    const [draftIp, setDraftIp] = useState('');
    const [model, setModel] = useState('');
    const [draftModel, setDraftModel] = useState('');
    const [channel, setChannel] = useState('');
    const [draftChannel, setDraftChannel] = useState('');

    // 筛选条件变化时重置页码, 避免停留在不存在的页。
    const offset = page * PAGE_SIZE;
    const { data, isLoading, isError, refetch, isFetching } = useLogList({
        limit: PAGE_SIZE,
        offset,
        keyword: keyword || undefined,
        status: status || undefined,
        clientIp: clientIp || undefined,
        model: model || undefined,
        channel: channel || undefined,
    });

    const items = data?.items ?? [];
    const total = data?.total ?? 0;
    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    const applyKeyword = () => {
        setKeyword(draftKeyword.trim());
        setClientIp(draftIp.trim());
        setModel(draftModel.trim());
        setChannel(draftChannel.trim());
        setPage(0);
    };

    // 导出使用与列表一致的筛选条件, 浏览器直接下载 CSV。
    const exportLogs = () => {
        window.open(logExportUrl({
            keyword: keyword || undefined,
            status: status || undefined,
            clientIp: clientIp || undefined,
            model: model || undefined,
            channel: channel || undefined,
        }), '_blank');
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <div className="relative min-w-0 flex-1 basis-56">
                    <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        className="rounded-xl pl-9 pr-8"
                        placeholder={t('searchPlaceholder')}
                        value={draftKeyword}
                        onChange={(e) => setDraftKeyword(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') applyKeyword();
                        }}
                    />
                    {draftKeyword && (
                        <button
                            type="button"
                            onClick={() => {
                                setDraftKeyword('');
                                setKeyword('');
                                setPage(0);
                            }}
                            className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                        >
                            <X className="size-4" />
                        </button>
                    )}
                </div>
                <Select value={status} onValueChange={(value) => { setStatus(value); setPage(0); }}>
                    <SelectTrigger className="w-36 rounded-xl">
                        <SelectValue placeholder={t('statusAll')} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="">{t('statusAll')}</SelectItem>
                        {STATUS_OPTIONS.map((option) => (
                            <SelectItem key={option} value={option}>{t('status.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Input className="w-28 rounded-xl" placeholder={t('filterIp')}
                    value={draftIp}
                    onChange={(e) => setDraftIp(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Input className="w-32 rounded-xl" placeholder={t('filterModel')}
                    value={draftModel}
                    onChange={(e) => setDraftModel(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Input className="w-32 rounded-xl" placeholder={t('filterChannel')}
                    value={draftChannel}
                    onChange={(e) => setDraftChannel(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Button variant="outline" size="sm" className="rounded-xl" onClick={applyKeyword}>
                    <Search className="size-4" />
                    {t('search')}
                </Button>
                <Button variant="outline" size="sm" className="rounded-xl" onClick={exportLogs}>
                    <Download className="size-4" />
                    {t('export')}
                </Button>
            </div>

            <div className="min-h-0 flex-1">
                {isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                ) : isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <p>{t('loadFailed')}</p>
                        <Button variant="outline" size="sm" onClick={() => void refetch()} className="rounded-xl">
                            {t('retry')}
                        </Button>
                    </div>
                ) : items.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <Logs className="size-8" />
                        <span className="text-sm">{t('empty')}</span>
                    </div>
                ) : (
                    <VirtualizedGrid
                        items={items}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={104}
                        overscan={8}
                        getItemKey={(log) => `log-${log.id}`}
                        renderItem={(log) => <LogCard log={log} />}
                    />
                )}
            </div>

            <div className="flex shrink-0 items-center justify-between gap-4">
                <span className="text-xs text-muted-foreground">
                    {t('pagination.page', { page: page + 1, total })}
                    {isFetching && <Loader2 className="ml-2 inline size-3 animate-spin" />}
                </span>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setPage((p) => Math.max(0, p - 1))}
                        disabled={page === 0}
                        className="rounded-xl"
                    >
                        <ChevronLeft className="size-4" />
                        {t('pagination.prev')}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
                        disabled={page >= totalPages - 1}
                        className="rounded-xl"
                    >
                        {t('pagination.next')}
                        <ChevronRight className="size-4" />
                    </Button>
                </div>
            </div>
        </div>
    );
}

// LogLive 渲染实时日志流视图。
function LogLive() {
    const t = useTranslations('log');
    const { logs, isLoading, error } = useLogs();

    if (isLoading) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (logs.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                {!error && <Logs className="size-8" />}
                <span className="text-sm">{error ? t('list.disconnected') : t('list.empty')}</span>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {error && (
                <div className="flex shrink-0 items-center justify-center px-1 pb-3 text-xs text-destructive">
                    <span>{t('list.disconnected')}</span>
                </div>
            )}
            <div className="min-h-0 flex-1">
                <VirtualizedGrid
                    items={logs}
                    layout="list"
                    columns={{ default: 1 }}
                    estimateItemHeight={104}
                    overscan={8}
                    getItemKey={(log) => `log-${log.id}`}
                    renderItem={(log) => <LogCard log={log} />}
                />
            </div>
        </div>
    );
}

// Log 展示进程内调用日志: 实时流视图与可查询/筛选/分页的历史视图。
export function Log() {
    const t = useTranslations('log');
    const [tab, setTab] = useState<'live' | 'history'>('live');

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <Tabs value={tab} onValueChange={(value) => setTab(value as 'live' | 'history')} className="w-auto">
                    <TabsList className="rounded-xl">
                        <TabsTrigger value="live">{t('tabs.live')}</TabsTrigger>
                        <TabsTrigger value="history">{t('tabs.history')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <div className="min-h-0 flex-1">
                {tab === 'live' ? <LogLive /> : <LogHistory />}
            </div>
        </div>
    );
}
