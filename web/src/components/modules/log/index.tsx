import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Activity, ChevronLeft, ChevronRight, Download, Loader2, Logs, Search, X } from 'lucide-react';
import { toast } from 'sonner';
import { useQuery } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';
import { useGroupList, type Group } from '@/api/group';
import { routeEventText, routeReasonText } from './reason';
import { logExportUrl, useLogList, useLogs } from '@/api/log';
import { downloadFile, isStepUpRequired } from '@/api/client';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { LogCard } from './Item';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

// PAGE_SIZE 历史日志每页条数。
const PAGE_SIZE = 20;

// STATUS_OPTIONS 终态筛选选项, 与后端终态定义一致。
const STATUS_OPTIONS = ['success', 'failed', 'canceled', 'committed', 'running'] as const;

interface AutoRoutingSummary {
    requests: number;
    task_classified: number;
    success: number;
    failed: number;
    success_rate: number;
    tasks: { task: string; requests: number; success: number; failed: number; success_rate: number; avg_duration_ms: number }[];
    models: { task: string; model: string; attempts: number; success: number; success_rate: number; avg_duration_ms: number; avg_cost_usd: number }[];
    exclusions: { reason: string; count: number }[];
    events: { reason: string; count: number }[];
    predictions: { task: string; compared: number; same_model: number; same_model_rate: number; predicted_cost_usd: number; actual_cost_usd: number; cost_delta_usd: number }[];
}

// AutoRoutingPanel 从现有调用日志聚合实验数据。看板不要求先打开所有请求详情，
// 因而不会在前端逐条拼装尝试记录，也不会失真于只保留 300 条的实时列表。
function AutoRoutingPanel() {
    const t = useTranslations('log.history');
    const tr = useTranslations('log.autoRouting');
    const [days, setDays] = useState(7);
    const groups = useGroupList();
    const [groupID, setGroupID] = useState(0);
    const groupOptions = useMemo(() => (groups.data ?? []).filter((group: Group) => group.mode === 'score' && group.relay_config.auto_routing), [groups.data]);
    const summary = useQuery({
        queryKey: ['logs', 'auto-routing', 'summary', days, groupID],
        queryFn: () => apiRequest<AutoRoutingSummary>(`/api/v1/log/auto-routing/summary?days=${days}${groupID ? `&group_id=${groupID}` : ''}`),
        refetchInterval: 15 * 1000,
    });
    const data = summary.data;
    if (summary.isLoading) return <div className="flex h-full items-center justify-center"><Loader2 className="size-5 animate-spin text-muted-foreground" /></div>;
    if (summary.isError || !data) return <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><Activity className="size-6" /><span>{tr('loadFailed')}</span></div>;
    return (
        <div className="flex h-full min-h-0 flex-col gap-3 overflow-auto">
            <div className="flex flex-wrap items-center gap-2">
                <Select value={String(groupID)} onValueChange={(value) => setGroupID(Number(value))}>
                    <SelectTrigger className="w-56 rounded-xl"><SelectValue placeholder={tr('allGroups')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="0">{tr('allGroups')}</SelectItem>
                        {groupOptions.map((group) => <SelectItem key={group.id} value={String(group.id)}>{group.name}</SelectItem>)}
                    </SelectContent>
                </Select>
                <Select value={String(days)} onValueChange={(value) => setDays(Number(value))}>
                    <SelectTrigger className="w-36 rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="1">{tr('daysOne')}</SelectItem>
                        <SelectItem value="7">{tr('daysSeven')}</SelectItem>
                        <SelectItem value="30">{tr('daysThirty')}</SelectItem>
                    </SelectContent>
                </Select>
                <span className="ml-auto text-xs text-muted-foreground">{tr('window', { requests: data.requests })}</span>
            </div>
            {data.task_classified === 0 ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 rounded-2xl border border-border/50 bg-muted/20 px-6 text-center text-muted-foreground">
                    <Activity className="size-8" />
                    <p className="text-sm font-medium">{tr('empty')}</p>
                    {groupOptions.length === 0 && <p className="max-w-lg text-xs">{tr('enableFirst')}</p>}
                </div>
            ) : (
                <div className="grid gap-3 lg:grid-cols-3">
                    {[{label:tr('classified'), value:data.task_classified}, {label:tr('successRate'), value:`${(data.success_rate * 100).toFixed(1)}%`}, {label:tr('failures'), value:data.failed}].map((card) => (
                        <div key={card.label} className="rounded-2xl border border-border/60 bg-card p-4">
                            <p className="text-xs text-muted-foreground">{card.label}</p>
                            <p className="mt-1 text-2xl font-semibold">{card.value}</p>
                        </div>
                    ))}
                </div>
            )}
            {data.tasks.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-3 text-sm font-semibold">{tr('tasksTitle')}</h3>
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.tasks.map((task) => (
                            <div key={task.task} className="rounded-xl border border-border/40 bg-muted/20 p-3">
                                <div className="flex items-center justify-between"><span className="text-sm font-medium">{tr('task.' + task.task)}</span><span className="text-xs text-muted-foreground">{tr('requestCount', { count: task.requests })}</span></div>
                                <div className="mt-2 grid grid-cols-3 gap-2 text-xs"><span>{tr('rate', { value: (task.success_rate * 100).toFixed(1) + '%' })}</span><span>{tr('latency', { value: Math.round(task.avg_duration_ms) + ' ms' })}</span><span>{tr('failedShort', { count: task.failed })}</span></div>
                                <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-emerald-500" style={{ width: `${Math.min(100, task.success_rate * 100)}%` }} /></div>
                            </div>
                        ))}
                    </div>
                </section>
            )}
            {(data.exclusions.length > 0 || data.events.length > 0) && (
                <div className="grid gap-3 lg:grid-cols-2">
                    <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('exclusionsTitle')}</h3><div className="space-y-1">{data.exclusions.slice(0, 10).map((item) => <div key={item.reason} className="flex justify-between gap-3 text-xs"><span className="text-muted-foreground">{routeReasonText(t, item.reason)}</span><span className="font-mono">{item.count}</span></div>)}</div></section>
                    <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('eventsTitle')}</h3><div className="space-y-1">{data.events.slice(0, 10).map((item) => <div key={item.reason} className="flex justify-between gap-3 text-xs"><span className="text-muted-foreground">{routeEventText(t, item.reason) || item.reason}</span><span className="font-mono">{item.count}</span></div>)}</div></section>
                </div>
            )}
            {data.models.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('modelsTitle')}</h3><div className="space-y-2">{data.models.slice(0, 20).map((model) => <div key={`${model.task}-${model.model}`} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-3 rounded-lg border border-border/40 px-3 py-2 text-xs"><span className="truncate">{tr('task.' + model.task)} · {model.model}</span><span className="tabular-nums text-muted-foreground">{tr('requestCount', { count: model.attempts })}</span><span className="tabular-nums">{tr('rate', { value: (model.success_rate * 100).toFixed(1) + '%' })}</span></div>)}</div></section>
            )}
            {data.predictions.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-3 text-sm font-semibold">{tr('predictionsTitle')}</h3>
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.predictions.map((item) => <div key={item.task} className="rounded-xl border border-border/40 bg-muted/20 p-3 text-xs">
                            <div className="flex justify-between"><span className="font-medium">{tr('task.' + item.task)}</span><span>{tr('predictionSamples', { count: item.compared })}</span></div>
                            <p className="mt-2">{tr('predictionAgreement', { value: (item.same_model_rate * 100).toFixed(1) + '%' })}</p>
                            <p className="mt-1 text-muted-foreground">{tr('predictionCostDelta', { predicted: item.predicted_cost_usd.toFixed(4), actual: item.actual_cost_usd.toFixed(4), delta: item.cost_delta_usd.toFixed(4) })}</p>
                        </div>)}
                    </div>
                </section>
            )}
        </div>
    );
}

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
    // 日志导出是敏感导出, 走通用客户端(撞上 step-up 会弹密码确认并带凭证重发这一次)。
    const exportLogs = async () => {
        try {
            await downloadFile(logExportUrl({
                keyword: keyword || undefined,
                status: status || undefined,
                clientIp: clientIp || undefined,
                model: model || undefined,
                channel: channel || undefined,
            }), 'switchyard-logs.csv');
        } catch (error) {
            // 用户取消密码确认 = 请求没发出去, 不必再报错。
            if (!isStepUpRequired(error)) toast.error(error instanceof Error ? error.message : String(error));
        }
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
                <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void exportLogs()}>
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
    const [tab, setTab] = useState<'live' | 'history' | 'auto'>('live');

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <Tabs value={tab} onValueChange={(value) => setTab(value as 'live' | 'history' | 'auto')} className="w-auto">
                    <TabsList className="rounded-xl">
                        <TabsTrigger value="live">{t('tabs.live')}</TabsTrigger>
                        <TabsTrigger value="history">{t('tabs.history')}</TabsTrigger>
                        <TabsTrigger value="auto">{t('tabs.autoRouting')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <div className="min-h-0 flex-1">
                {tab === 'live' ? <LogLive /> : tab === 'history' ? <LogHistory /> : <AutoRoutingPanel />}
            </div>
        </div>
    );
}
