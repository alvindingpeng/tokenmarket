import { useState, type ReactNode } from 'react';
import BillingRecovery from './BillingRecovery';
import { useTranslations } from 'use-intl';
import { Archive, DatabaseBackup, Gauge, HeartPulse, ListFilter, RefreshCw, Scale, ShieldAlert, StickyNote, Timer, Wallet } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { toast } from 'sonner';
import {
    useAlertDelivery, useArchives, useBackups, useBalanceFlow, useChannelAlerts, useChannelHealth,
    useAlertSilence, useCreateAlertSilence, useDeleteAlertSilence,
    useBudgets, useCreateBudget, useDeleteBudget, useUpdateBudget,
    useLatencyBreakdown, useOpsOverview, useReconcileList, useRunAlertRedeliver, useRunBackup, useRunHealthCheck, useRunLogLifecycle, useRunReconcile, useRequeueAlert, useSetReconcileNote, useTaskStatus, useUndeliveredAlerts,
    type AlertSilenceWindow, type BalanceFlow, type ChannelAlert, type ChannelHealth, type LatencyRow, type ReconcileRow, type SpendBudget, type TaskStatus, type UndeliveredAlert,
} from '@/api/ops';
import { useRateUsageTop } from '@/api/ratelimit';

// formatTime 把 RFC3339 时间转成本地展示, 无效值原样返回。
function formatTime(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

// formatBytes 文件大小人读格式。
function formatBytes(size: number): string {
    if (!size) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let value = size;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit += 1; }
    return (unit === 0 ? value : value.toFixed(1)) + ' ' + units[unit];
}

// formatMoney 金额展示: 保留到 6 位再裁掉尾零。
function formatMoney(value: number): string {
    const fixed = Number(value ?? 0).toFixed(6);
    return fixed.includes('.') ? fixed.replace(/0+$/, '').replace(/\.$/, '') : fixed;
}

// OpsTable 是运维页通用表格: 空态、横向滚动、行悬停统一处理。
type Col<T> = { key: string; label: string; render?: (row: T) => ReactNode };
function OpsTable<T>({ columns, rows, empty, rowKey }: { columns: Col<T>[]; rows: T[]; empty: string; rowKey: (row: T) => string }) {
    if (rows.length === 0) {
        return <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{empty}</div>;
    }
    return (
        <div className='overflow-x-auto rounded-3xl border border-border bg-card'>
            <table className='w-full min-w-[760px] text-sm'>
                <thead>
                    <tr className='border-b border-border text-left text-xs text-muted-foreground'>
                        {columns.map((column) => (
                            <th key={column.key} className='whitespace-nowrap px-4 py-3 font-medium'>{column.label}</th>
                        ))}
                    </tr>
                </thead>
                <tbody>
                    {rows.map((row) => (
                        <tr key={rowKey(row)} className='border-b border-border/60 last:border-0 hover:bg-muted/40'>
                            {columns.map((column) => (
                                <td key={column.key} className='px-4 py-2.5 align-middle'>
                                    {column.render ? column.render(row) : String((row as Record<string, unknown>)[column.key] ?? '-')}
                                </td>
                            ))}
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
}

// RefreshButton 通用刷新按钮, 转圈跟随 isRefetching。
function RefreshButton({ onClick, busy, label }: { onClick: () => void; busy: boolean; label: string }) {
    return (
        <Button variant='outline' size='sm' onClick={onClick} disabled={busy} className='rounded-xl'>
            <RefreshCw className={'size-4' + (busy ? ' animate-spin' : '')} />
            {label}
        </Button>
    );
}

// OpsOverviewCards 运维概览卡片: 24 小时请求/成功率/用量/费用 + 实时进行中/渠道/告警。
function OpsOverviewCards() {
    const t = useTranslations('ops.overview');
    const { data } = useOpsOverview();
    const requests = data?.requests_24h ?? 0;
    const successRate = requests > 0 ? (((data?.success_24h ?? 0) / requests) * 100).toFixed(1) + '%' : '-';
    const cards = [
        { key: 'requests', label: t('requests'), value: data ? String(requests) : '-' },
        { key: 'successRate', label: t('successRate'), value: data ? successRate : '-' },
        { key: 'tokens', label: t('tokens'), value: data ? String((data.input_tokens_24h + data.output_tokens_24h).toLocaleString()) : '-' },
        { key: 'cost', label: t('cost'), value: data ? formatMoney(data.cost_24h) : '-' },
        { key: 'active', label: t('active'), value: data ? String(data.active_requests) : '-' },
        { key: 'channels', label: t('channels'), value: data ? `${data.channels_enabled}/${data.channels_total}` : '-' },
        { key: 'alerts', label: t('alerts'), value: data ? String(data.alerts_24h) : '-' },
    ];
    return (
        <div className='grid shrink-0 grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7'>
            {cards.map((card) => (
                <div key={card.key} className='rounded-2xl border border-border bg-card px-4 py-3'>
                    <div className='text-xs text-muted-foreground'>{card.label}</div>
                    <div className='truncate text-lg font-semibold text-card-foreground'>{card.value}</div>
                </div>
            ))}
        </div>
    );
}

// REDELIVER_MAX_ATTEMPTS 后端补发重试上限(alertRedeliverMaxAttempts)。
// 前端用它区分"已放弃"(达到上限)与"超窗未补发"(窗口外的历史告警), 两态在界面上
// 必须分开显示 —— 见 deliveryState。改动此值时需同步 internal/op/alert.go。
const REDELIVER_MAX_ATTEMPTS = 8;

const TABS = [
    { id: 'reconcile', icon: Scale },
    { id: 'recovery', icon: RefreshCw },
    { id: 'flow', icon: Wallet },
    { id: 'backup', icon: DatabaseBackup },
    { id: 'archive', icon: Archive },
    { id: 'health', icon: HeartPulse },
    { id: 'alerts', icon: ShieldAlert },
    { id: 'tasks', icon: Timer },
    { id: 'budgets', icon: Wallet },
    { id: 'latency', icon: Gauge },
    { id: 'ratelimit', icon: ListFilter },
] as const;

// Ops 运维中心: 对账/流水/备份/归档/健康/告警六个页签, 全部管理员可见。
export default function Ops() {
    const t = useTranslations('ops');
    const [tab, setTab] = useState('reconcile');
    return (
        <div className='flex h-full min-h-0 flex-col gap-3'>
            <h2 className='flex shrink-0 items-center gap-2 text-lg font-bold text-card-foreground'>
                <HeartPulse className='size-5' />
                {t('title')}
            </h2>
            <OpsOverviewCards />
            <Tabs value={tab} onValueChange={setTab}>
                <TabsList className='h-auto shrink-0 flex-wrap'>
                    {TABS.map((item) => {
                        const Icon = item.icon;
                        return (
                            <TabsTrigger key={item.id} value={item.id} className='gap-1.5'>
                                <Icon className='size-4' />
                                {t('tabs.' + item.id)}
                            </TabsTrigger>
                        );
                    })}
                </TabsList>
            </Tabs>
            <div className='min-h-0 flex-1 space-y-3 overflow-y-auto pb-4'>
                {tab === 'reconcile' && <ReconcileSection />}
                {tab === 'recovery' && <BillingRecovery />}
                {tab === 'flow' && <FlowSection />}
                {tab === 'backup' && <BackupSection />}
                {tab === 'archive' && <ArchiveSection />}
                {tab === 'health' && <HealthSection />}
                {tab === 'alerts' && <AlertsSection />}
                {tab === 'latency' && <LatencySection />}
                {tab === 'ratelimit' && <RateLimitTopSection />}
                {tab === 'tasks' && <TasksSection />}
                {tab === 'budgets' && <BudgetsSection />}
            </div>
        </div>
    );
}

// ReconcileStatusBadge 对账状态徽章颜色: 对平绿/差异红/待核查黄。
function ReconcileStatusBadge({ status }: { status: string }) {
    const t = useTranslations('ops');
    const map: Record<string, { label: string; className: string }> = {
        balanced: { label: t('reconcile.statusBalanced'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
        mismatch: { label: t('reconcile.statusMismatch'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
        review: { label: t('reconcile.statusReview'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
        explained: { label: t('reconcile.statusExplained'), className: 'bg-sky-500/15 text-sky-600 dark:text-sky-400' },
    };
    const item = map[status] ?? { label: status, className: 'bg-muted text-muted-foreground' };
    return <Badge variant='outline' className={item.className}>{item.label}</Badge>;
}

// ReconcileNoteCell 差异处置说明单元格: 已登记的日子显示说明与登记入口, 未登记则显示入口。
// 只有存在差异的行才给入口 —— 对平的日子没有差异可解释。
function ReconcileNoteCell({ row }: { row: ReconcileRow }) {
    const t = useTranslations('ops');
    const hasDiff = row.missing > 0 || row.duplicated > 0 || row.stale_hold > 0;
    if (!hasDiff && !row.note) {
        return <span className='text-muted-foreground'>-</span>;
    }
    return (
        <div className='flex max-w-[320px] flex-col gap-1'>
            {row.note && <span className='line-clamp-2 text-xs text-muted-foreground'>{row.note}</span>}
            <MorphingDialog>
                <MorphingDialogTrigger className='w-fit text-xs text-primary underline-offset-2 hover:underline'>
                    {t('reconcile.noteAction')}
                </MorphingDialogTrigger>
                <MorphingDialogContainer>
                    <MorphingDialogContent
                        dismissOnClickOutside={false}
                        className='flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground'>
                        <ReconcileNoteDialogContent row={row} />
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>
        </div>
    );
}

// ReconcileNoteDialogContent 登记/撤销处置说明。
// 界面把「只写说明与状态」写明白: 差异数字一个都不动是这条路径的全部意义。
function ReconcileNoteDialogContent({ row }: { row: ReconcileRow }) {
    const t = useTranslations('ops');
    const { setIsOpen } = useMorphingDialog();
    const [note, setNote] = useState(row.note ?? '');
    const save = useSetReconcileNote();

    const submit = (next: string) => {
        save.mutate({ day: row.day, note: next }, {
            onSuccess: () => {
                toast.success(next.trim() ? t('reconcile.noteSaved') : t('reconcile.noteCleared'));
                setIsOpen(false);
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : t('reconcile.noteFailed')),
        });
    };

    return (
        <div className='flex w-[min(92vw,520px)] flex-col gap-3 overflow-y-auto'>
            <div className='flex items-center gap-2 text-base font-semibold'><StickyNote className='size-4 text-muted-foreground' />{t('reconcile.noteTitle')}</div>
            <p className='text-xs text-muted-foreground'>{t('reconcile.noteDesc')}</p>
            <div className='flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground'>
                <span>{t('reconcile.noteDay')}: <span className='font-mono'>{row.day}</span></span>
                <span>{t('reconcile.missing')}: {row.missing}</span>
                <span>{t('reconcile.duplicated')}: {row.duplicated}</span>
                <span>{t('reconcile.staleHold')}: {row.stale_hold}</span>
            </div>
            <Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={6} placeholder={t('reconcile.notePlaceholder')} />
            <p className='text-xs text-muted-foreground'>{t('reconcile.noteHint')}</p>
            <div className='flex items-center justify-end gap-2'>
                <Button variant='outline' size='sm' className='rounded-xl' disabled={save.isPending || !row.note} onClick={() => submit('')}>{t('reconcile.noteClear')}</Button>
                <Button size='sm' className='rounded-xl' disabled={save.isPending || !note.trim()} onClick={() => submit(note)}>{t('reconcile.noteSave')}</Button>
            </div>
        </div>
    );
}

// ReconcileSection 每日对账结果: 支持回溯 1/3/7 天手动执行。
function ReconcileSection() {
    const t = useTranslations('ops');
    const [days, setDays] = useState('1');
    const { data, isLoading, isError, refetch, isRefetching } = useReconcileList(30);
    const run = useRunReconcile();

    const onRun = () => {
        run.mutate(Number(days), {
            onSuccess: () => toast.success(t('reconcile.runSuccess')),
            onError: (error) => toast.error(error instanceof Error ? error.message : t('reconcile.runFailed')),
        });
    };

    const columns: Col<ReconcileRow>[] = [
        { key: 'day', label: t('reconcile.day') },
        { key: 'records', label: t('reconcile.records') },
        { key: 'user_cost', label: t('reconcile.userCost'), render: (row) => formatMoney(row.user_cost) },
        { key: 'owner_revenue', label: t('reconcile.ownerRevenue'), render: (row) => formatMoney(row.owner_revenue) },
        { key: 'platform_revenue', label: t('reconcile.platformRevenue'), render: (row) => formatMoney(row.platform_revenue) },
        { key: 'duplicated', label: t('reconcile.duplicated') },
        { key: 'missing', label: t('reconcile.missing') },
        { key: 'stale_hold', label: t('reconcile.staleHold') },
        { key: 'unsettled', label: t('reconcile.unsettled') },
        { key: 'status', label: t('reconcile.status'), render: (row) => <ReconcileStatusBadge status={row.status} /> },
        { key: 'note', label: t('reconcile.note'), render: (row) => <ReconcileNoteCell row={row} /> },
        { key: 'created_at', label: t('reconcile.time'), render: (row) => formatTime(row.created_at) },
    ];

    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('reconcile.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Select value={days} onValueChange={setDays}>
                        <SelectTrigger className='h-9 w-32 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='1'>{t('reconcile.days1')}</SelectItem>
                            <SelectItem value='3'>{t('reconcile.days3')}</SelectItem>
                            <SelectItem value='7'>{t('reconcile.days7')}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Button size='sm' onClick={onRun} disabled={run.isPending} className='rounded-xl'>
                        {run.isPending ? t('running') : t('reconcile.run')}
                    </Button>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => row.day} />
            )}
        </div>
    );
}

// flowKindBadge 流水类型徽章。
function FlowSection() {
    const t = useTranslations('ops');
    const [userId, setUserId] = useState('');
    const parsedUserId = Number.parseInt(userId, 10);
    const { data, isLoading, isError, refetch, isRefetching } = useBalanceFlow(Number.isFinite(parsedUserId) && parsedUserId > 0 ? parsedUserId : undefined, 100);
    const kindLabel = (kind: string) => t('flow.kind' + kind.charAt(0).toUpperCase() + kind.slice(1));
    const columns: Col<BalanceFlow>[] = [
        { key: 'request_id', label: t('flow.requestId') },
        { key: 'user_id', label: t('flow.userId') },
        { key: 'kind', label: t('flow.kind'), render: (row) => <Badge variant='secondary'>{kindLabel(row.kind)}</Badge> },
        { key: 'amount', label: t('flow.amount'), render: (row) => formatMoney(row.amount) },
        { key: 'settled', label: t('flow.settled'), render: (row) => (row.settled ? t('flow.settledYes') : <span className='text-amber-600 dark:text-amber-400'>{t('flow.settledNo')}</span>) },
        { key: 'user_cost', label: t('flow.userCost'), render: (row) => formatMoney(row.user_cost) },
        { key: 'created_at', label: t('flow.time'), render: (row) => formatTime(row.created_at) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('flow.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Input className='h-9 w-44 rounded-xl' placeholder={t('flow.userFilter')} value={userId}
                        onChange={(event) => setUserId(event.target.value.replace(/[^0-9]/g, ''))} />
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => row.request_id + ':' + row.kind + ':' + row.created_at} />
            )}
        </div>
    );
}

// BackupSection 一致性备份: 手动触发 + 列表。
function BackupSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, refetch, isRefetching } = useBackups();
    const run = useRunBackup();
    const onRun = () => {
        run.mutate(undefined, {
            onSuccess: (result) => toast.success(t('backup.runSuccess', { file: result.file })),
            onError: (error) => toast.error(error instanceof Error ? error.message : t('backup.runFailed')),
        });
    };
    const columns: Col<{ name: string; size: number; mod_time: string }>[] = [
        { key: 'name', label: t('backup.name') },
        { key: 'size', label: t('backup.size'), render: (row) => formatBytes(row.size) },
        { key: 'mod_time', label: t('backup.time'), render: (row) => formatTime(row.mod_time) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('backup.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Button size='sm' onClick={onRun} disabled={run.isPending} className='rounded-xl'>
                        {run.isPending ? t('running') : t('backup.run')}
                    </Button>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => row.name} />
            )}
        </div>
    );
}

// ArchiveSection 日志归档: 显示留存天数, 手动执行归档清理。
function ArchiveSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, refetch, isRefetching } = useArchives();
    const run = useRunLogLifecycle();
    const onRun = () => {
        run.mutate(undefined, {
            onSuccess: (result) => toast.success(t('archive.runSuccess', { archived: result.archived, deleted: result.requests_deleted })),
            onError: (error) => toast.error(error instanceof Error ? error.message : t('archive.runFailed')),
        });
    };
    const columns: Col<{ name: string; size: number; mod_time: string }>[] = [
        { key: 'name', label: t('archive.name') },
        { key: 'size', label: t('archive.size'), render: (row) => formatBytes(row.size) },
        { key: 'mod_time', label: t('archive.time'), render: (row) => formatTime(row.mod_time) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('archive.desc', { days: data?.retention_days ?? 7 })}</p>
                <div className='flex items-center gap-2'>
                    <Button size='sm' onClick={onRun} disabled={run.isPending} className='rounded-xl'>
                        {run.isPending ? t('running') : t('archive.run')}
                    </Button>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data?.items ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => row.name} />
            )}
        </div>
    );
}

// healthBadge 渠道健康状态徽章。
function HealthSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, refetch, isRefetching } = useChannelHealth(undefined, 50);
    const runCheck = useRunHealthCheck();
    const columns: Col<ChannelHealth>[] = [
        { key: 'channel_code', label: t('health.channel') },
        { key: 'healthy', label: t('health.status'), render: (row) => (row.healthy
            ? <Badge variant='outline' className='bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'>{t('health.healthyOk')}</Badge>
            : <Badge variant='outline' className='bg-red-500/15 text-red-600 dark:text-red-400'>{t('health.healthyFail')}</Badge>) },
        { key: 'latency_ms', label: t('health.latency'), render: (row) => (row.latency_ms > 0 ? row.latency_ms + ' ms' : '-') },
        { key: 'status_code', label: t('health.statusCode'), render: (row) => (row.status_code > 0 ? String(row.status_code) : '-') },
        { key: 'error', label: t('health.error'), render: (row) => <span className='block max-w-72 truncate' title={row.error}>{row.error || '-'}</span> },
        { key: 'checked_at', label: t('health.time'), render: (row) => formatTime(row.checked_at) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('health.desc')}</p>
                <Button size='sm' onClick={() => runCheck.mutate(undefined, {
                    onSuccess: () => toast.success(t('health.runSuccess')),
                    onError: (error) => toast.error(error instanceof Error ? error.message : t('health.runFailed')),
                })} disabled={runCheck.isPending} className='rounded-xl'>
                    {runCheck.isPending ? t('running') : t('health.runNow')}
                </Button>
                <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => String(row.id)} />
            )}
        </div>
    );
}

// pair 把一对分位值渲染成 "p50 / p95"; 单位在表头标注, 单元格只放数字便于横向比较。
function pair(value: { p50_ms: number; p95_ms: number }): string {
    return value.p50_ms + ' / ' + value.p95_ms;
}

// LatencySection 延迟归因: 把首字节耗时拆成建连各段与上游等待, 用于判断瓶颈在自家出口还是上游渠道。
// 复用率单列是必要的: 复用连接的请求建连列必然为 0, 不标出复用率, 这些 0 会被读成"建连极快"。
function LatencySection() {
    const t = useTranslations('ops');
    const [days, setDays] = useState('7');
    const { data, isLoading, isError, refetch, isRefetching } = useLatencyBreakdown(Number(days));
    const columns: Col<LatencyRow>[] = [
        { key: 'channel', label: t('latency.channel'), render: (row) => (
            <span className='block max-w-56 truncate' title={row.channel_key || row.channel_code}>
                {row.channel_key || (row.channel_code ? '#' + row.channel_code : '-')}
            </span>
        ) },
        { key: 'country', label: t('latency.country'), render: (row) => row.country || t('latency.countryUnknown') },
        { key: 'egress', label: t('latency.egress'), render: (row) => row.egress_name || t('latency.egressDefault') },
        { key: 'requests', label: t('latency.requests') },
        { key: 'reused', label: t('latency.reused'), render: (row) => (row.reused_rate * 100).toFixed(0) + '%' },
        { key: 'ttfb', label: t('latency.ttfb') + ' p50/p95', render: (row) => pair(row.ttfb) + ' ms' },
        { key: 'connect', label: t('latency.connect') + ' p50/p95', render: (row) => pair(row.connect) + ' ms' },
        { key: 'tls', label: t('latency.tls') + ' p50/p95', render: (row) => pair(row.tls) + ' ms' },
        { key: 'upstreamWait', label: t('latency.upstreamWait') + ' p50/p95', render: (row) => pair(row.upstream_wait) + ' ms' },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='max-w-3xl text-sm text-muted-foreground'>{t('latency.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Select value={days} onValueChange={setDays}>
                        <SelectTrigger className='h-9 w-32 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='1'>{t('reconcile.days1')}</SelectItem>
                            <SelectItem value='3'>{t('reconcile.days3')}</SelectItem>
                            <SelectItem value='7'>{t('reconcile.days7')}</SelectItem>
                        </SelectContent>
                    </Select>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')}
                    rowKey={(row) => row.channel_code + '|' + row.country + '|' + row.egress_id} />
            )}
        </div>
    );
}

// alertKindBadge 告警类型徽章: 宕机红/恢复绿/降级黄。
function AlertsSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, refetch, isRefetching } = useChannelAlerts(50);
    // 送达状态单独查询: 告警列表给的是历史, 这里回答的是"现在还有多少条没送出去"。
    const delivery = useAlertDelivery();
    const undelivered = useUndeliveredAlerts(50);
    const redeliver = useRunAlertRedeliver();
    const requeue = useRequeueAlert();
    const deliveryStats = delivery.data;
    // kindBadge 告警类型徽章。新增的熔断/任务/配额类型同样要有中文标签,
    // 否则管理员在列表里只看到裸英文 kind, 分不清是渠道挂了还是后台任务挂了。
    const kindBadge = (kind: string) => {
        const map: Record<string, { label: string; className: string }> = {
            down: { label: t('alerts.kindDown'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            recovered: { label: t('alerts.kindRecovered'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
            degraded: { label: t('alerts.kindDegraded'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
            key_disabled: { label: t('alerts.kindKeyDisabled'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            model_disabled: { label: t('alerts.kindModelDisabled'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            mismatch: { label: t('alerts.kindMismatch'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            backup_failed: { label: t('alerts.kindBackup'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            archive_failed: { label: t('alerts.kindArchive'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
            billing_failed: { label: t('alerts.kindBilling'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            low_balance: { label: t('alerts.kindLowBalance'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            quota_exceeded: { label: t('alerts.kindQuota'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            task_failed: { label: t('alerts.kindTaskFailed'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            task_recovered: { label: t('alerts.kindTaskRecovered'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
            update_available: { label: t('alerts.kindUpdate'), className: 'bg-sky-500/15 text-sky-600 dark:text-sky-400' },
            // 看门狗与路由落库失败都是 critical: 前者是站内探针看不见的反复重启, 后者是路由热状态没有落盘。
            watchdog_alert: { label: t('alerts.kindWatchdog'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            route_persist_failed: { label: t('alerts.kindRoutePersist'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            route_persist_recovered: { label: t('alerts.kindRoutePersistRecovered'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
            test: { label: t('alerts.kindTest'), className: 'bg-muted text-muted-foreground' },
        };
        const item = map[kind] ?? { label: kind, className: 'bg-muted text-muted-foreground' };
        return <Badge variant='outline' className={item.className}>{item.label}</Badge>;
    };
    // 主体列取 subject(真实渠道名/用户名/任务名): 旧数据没有 subject 时回退分享码,
    // 再回退渠道 ID —— 总比空单元格好读。detail 用原生 details 折叠, 列表保持一屏,
    // 点开就能看到延迟、错误、建议这些与外发通知同源的明细。
    const columns: Col<ChannelAlert>[] = [
        { key: 'subject', label: t('alerts.subject'), render: (row) => (
            <span className='block max-w-56 truncate' title={row.subject || row.channel_code}>
                {row.subject || (row.channel_code ? '#' + row.channel_code : '') || (row.channel_id ? '#' + row.channel_id : t('alerts.system'))}
            </span>
        ) },
        { key: 'kind', label: t('alerts.kind'), render: (row) => kindBadge(row.kind) },
        { key: 'reason', label: t('alerts.reason'), render: (row) => (
            <span className='block max-w-80 truncate' title={row.reason}>{row.reason || '-'}</span>
        ) },
        { key: 'detail', label: t('alerts.detail'), render: (row) => (
            row.detail ? (
                <details className='max-w-80'>
                    <summary className='cursor-pointer text-xs text-muted-foreground'>{t('alerts.detailShow')}</summary>
                    <pre className='mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-xl bg-muted/60 p-2 text-xs'>{row.detail}</pre>
                </details>
            ) : <span className='text-xs text-muted-foreground'>-</span>
        ) },
        { key: 'notified', label: t('alerts.notified'), render: (row) => (row.notified ? t('alerts.yes') : t('alerts.no')) },
        { key: 'created_at', label: t('alerts.time'), render: (row) => formatTime(row.created_at) },
    ];
    // 未送达列表: 只在真的有待补发/已放弃时才展开, 平时不占版面。
    // 已放弃的行排期为空(后端如此排序), 界面直接标"已放弃", 不让管理员以为还在重试。
    // deliveryState 三态判定, 与后端 AlertDeliveryStatsGet 逐条对应:
    //   有排期 = 待补发; 无排期且达上限 = 已放弃; 无排期且未达上限 = 超窗(落在补发窗口之外)。
    // 界面必须能区分后两者: 都显示"已放弃"会让管理员以为系统试过并放弃了, 而超窗的那些
    // 一次都没发过 —— 处置方式完全不同(前者要先修配置, 后者可以直接放行重发)。
    const deliveryState = (row: UndeliveredAlert) => {
        if (row.next_notify_at) return 'pending' as const;
        return (row.notify_attempts ?? 0) >= REDELIVER_MAX_ATTEMPTS ? 'gaveUp' as const : 'expired' as const;
    };
    const undeliveredColumns: Col<UndeliveredAlert>[] = [
        { key: 'subject', label: t('alerts.subject'), render: (row) => (
            <span className='block max-w-48 truncate' title={row.subject}>{row.subject || '-'}</span>
        ) },
        { key: 'kind', label: t('alerts.kind'), render: (row) => kindBadge(row.kind) },
        { key: 'attempts', label: t('delivery.attempts'), render: (row) => String(row.notify_attempts ?? 0) },
        { key: 'next', label: t('delivery.nextAttempt'), render: (row) => {
            const state = deliveryState(row);
            if (state === 'pending') return formatTime(row.next_notify_at as string);
            return <span className='text-xs text-muted-foreground'>{state === 'gaveUp' ? t('delivery.givenUp') : t('delivery.expired')}</span>;
        } },
        { key: 'error', label: t('delivery.lastError'), render: (row) => (
            <span className='block max-w-80 truncate text-xs text-muted-foreground' title={row.last_notify_error}>{row.last_notify_error || '-'}</span>
        ) },
        { key: 'created_at', label: t('alerts.time'), render: (row) => formatTime(row.created_at) },
        // 手动放行: 只对"超窗"的行开放。已放弃的行要先修配置, 待补发的行按排期自己会发,
        // 两者点了也是白发一次, 徒增困惑。
        { key: 'action', label: '', render: (row) => (
            deliveryState(row) === 'expired' ? (
                <Button size='sm' variant='ghost' className='h-7 px-2 text-xs' disabled={requeue.isPending}
                    onClick={() => requeue.mutate(row.id, {
                        onSuccess: () => toast.success(t('delivery.requeued')),
                        onError: (error) => toast.error(error instanceof Error ? error.message : t('delivery.requeueFailed')),
                    })}>
                    {t('delivery.requeue')}
                </Button>
            ) : null
        ) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('alerts.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Button
                        size='sm'
                        variant='outline'
                        disabled={redeliver.isPending}
                        onClick={() => redeliver.mutate(undefined, {
                            onSuccess: (result) => toast.success(t('delivery.resent', { sent: result.sent, gaveUp: result.gave_up })),
                            onError: () => toast.error(t('delivery.resendFailed')),
                        })}
                    >
                        {redeliver.isPending ? t('delivery.resending') : t('delivery.resend')}
                    </Button>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {deliveryStats && (deliveryStats.pending > 0 || deliveryStats.gave_up > 0 || deliveryStats.expired > 0) ? (
                <div className='space-y-3 rounded-3xl border border-amber-500/40 bg-amber-500/10 p-4'>
                    <p className='text-sm font-medium text-amber-700 dark:text-amber-400'>
                        {t('delivery.warning', {
                            pending: deliveryStats.pending,
                            gaveUp: deliveryStats.gave_up,
                            expired: deliveryStats.expired,
                            window: deliveryStats.redeliver_window_hours,
                        })}
                    </p>
                    {deliveryStats.failure_reasons && deliveryStats.failure_reasons.length > 0 ? (
                        <div className='space-y-1'>
                            <p className='text-xs font-medium text-muted-foreground'>{t('delivery.reasonTitle')}</p>
                            <ul className='space-y-0.5'>
                                {deliveryStats.failure_reasons.map((item) => (
                                    <li key={item.reason} className='flex items-start gap-2 text-xs text-muted-foreground'>
                                        <span className='shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]'>{item.count}×</span>
                                        <span className='break-all'>{item.reason}</span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    ) : deliveryStats.last_error ? (
                        <p className='text-xs text-muted-foreground'>{t('delivery.lastError')}: {deliveryStats.last_error}</p>
                    ) : null}
                    <OpsTable
                        columns={undeliveredColumns}
                        rows={undelivered.data ?? []}
                        empty={undelivered.isLoading ? t('loading') : t('delivery.noneUndelivered')}
                        rowKey={(row) => String(row.id)}
                    />
                </div>
            ) : null}
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => String(row.id)} />
            )}
            <SilenceWindowsSection />
        </div>
    );
}

// SilenceWindowsSection 告警静默窗口: 计划内维护期间告警照常落库但不外发。
// 有截止时间, 到点自动恢复 —— 不做无期限开关, 忘了关的静默比吵闹更危险。
function SilenceWindowsSection() {
    const t = useTranslations('ops');
    const { data, isLoading, refetch, isRefetching } = useAlertSilence();
    const create = useCreateAlertSilence();
    const remove = useDeleteAlertSilence();
    const now = new Date();
    const nowLocal = new Date(now.getTime() - now.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    const endDefault = new Date(now.getTime() + 60 * 60000 - now.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    const [startAt, setStartAt] = useState(nowLocal);
    const [endAt, setEndAt] = useState(endDefault);
    const [kind, setKind] = useState('all');
    const [note, setNote] = useState('');

    const submit = () => {
        if (!startAt || !endAt) {
            toast.error(t('silence.timeRequired'));
            return;
        }
        create.mutate(
            { start_at: new Date(startAt).toISOString(), end_at: new Date(endAt).toISOString(), kind: kind === 'all' ? '' : kind, note },
            {
                onSuccess: () => { toast.success(t('silence.created')); setNote(''); void refetch(); },
                onError: (error) => toast.error(error instanceof Error ? error.message : t('silence.createFailed')),
            },
        );
    };

    const windows = (data ?? []).slice(0, 20);
    const nowMs = Date.now();
    const columns: Col<AlertSilenceWindow>[] = [
        { key: 'range', label: t('silence.range'), render: (row) => (
            <span className='text-xs'>{formatTime(row.start_at)} ~ {formatTime(row.end_at)}</span>
        ) },
        { key: 'kind', label: t('silence.kind'), render: (row) => (
            row.kind ? <Badge variant='outline' className='bg-muted text-muted-foreground'>{row.kind}</Badge>
                : <Badge variant='outline' className='bg-amber-500/15 text-amber-700 dark:text-amber-400'>{t('silence.allKinds')}</Badge>
        ) },
        { key: 'state', label: t('silence.state'), render: (row) => {
            const active = new Date(row.start_at).getTime() <= nowMs && nowMs < new Date(row.end_at).getTime();
            return active
                ? <Badge className='bg-amber-500/15 text-amber-700 dark:text-amber-400'>{t('silence.active')}</Badge>
                : <Badge variant='outline' className='bg-muted text-muted-foreground'>{t('silence.past')}</Badge>;
        } },
        { key: 'note', label: t('silence.note'), render: (row) => (
            <span className='block max-w-56 truncate text-xs text-muted-foreground' title={row.note}>{row.note || '-'}</span>
        ) },
        { key: 'action', label: '', render: (row) => {
            const active = new Date(row.start_at).getTime() <= nowMs && nowMs < new Date(row.end_at).getTime();
            if (!active) return null;
            return (
                <Button size='sm' variant='ghost' className='h-7 px-2 text-xs' disabled={remove.isPending}
                    onClick={() => remove.mutate(row.id, {
                        onSuccess: () => toast.success(t('silence.deleted')),
                        onError: (error) => toast.error(error instanceof Error ? error.message : t('silence.deleteFailed')),
                    })}>
                    {t('silence.cancel')}
                </Button>
            );
        } },
    ];

    return (
        <div className='space-y-3 rounded-3xl border border-border bg-card p-4'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <div>
                    <p className='text-sm font-medium'>{t('silence.title')}</p>
                    <p className='text-xs text-muted-foreground'>{t('silence.desc')}</p>
                </div>
                <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
            </div>
            <div className='flex flex-wrap items-end gap-2'>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('silence.start')}
                    <Input type='datetime-local' value={startAt} onChange={(e) => setStartAt(e.target.value)} className='h-9 w-48 rounded-xl' />
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('silence.end')}
                    <Input type='datetime-local' value={endAt} onChange={(e) => setEndAt(e.target.value)} className='h-9 w-48 rounded-xl' />
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('silence.kind')}
                    <Select value={kind} onValueChange={setKind}>
                        <SelectTrigger className='h-9 w-52 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='all'>{t('silence.allKinds')}</SelectItem>
                            {['down', 'recovered', 'degraded', 'key_disabled', 'model_disabled', 'low_balance', 'quota_exceeded',
                                'mismatch', 'billing_failed', 'backup_failed', 'archive_failed', 'watchdog_alert',
                                'route_persist_failed', 'route_persist_recovered', 'task_failed', 'task_recovered'].map((k) => (
                                <SelectItem key={k} value={k}>{k}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('silence.note')}
                    <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('silence.notePlaceholder')} className='h-9 w-56 rounded-xl' maxLength={200} />
                </label>
                <Button size='sm' disabled={create.isPending} onClick={submit}>{create.isPending ? t('silence.creating') : t('silence.create')}</Button>
            </div>
            <OpsTable columns={columns} rows={windows} empty={isLoading ? t('loading') : t('silence.none')} rowKey={(row) => String(row.id)} />
        </div>
    );
}

// BudgetsSection 用户花费预算: 周期上限, warn/block 两档。
// block 在请求预扣前拒绝, warn 只发去重通知 —— 预算应"接近"就提醒, 不是到 100% 才从失败请求里得知。
function BudgetsSection() {
    const t = useTranslations('ops');
    const { data, isLoading, refetch, isRefetching } = useBudgets();
    const create = useCreateBudget();
    const update = useUpdateBudget();
    const remove = useDeleteBudget();
    const [scope, setScope] = useState('user');
    const [scopeId, setScopeId] = useState('');
    const [period, setPeriod] = useState('day');
    const [action, setAction] = useState('warn');
    const [limit, setLimit] = useState('');
    const [warnRatio, setWarnRatio] = useState('80');

    const submit = () => {
        const id = Number(scopeId);
        const amount = Number(limit);
        const ratio = Number(warnRatio) || 80;
        if (!id || !amount || amount <= 0) {
            toast.error(t('budgets.inputInvalid'));
            return;
        }
        create.mutate(
            scope === 'user' ? { user_id: id, period, action, limit_amount: amount, warn_ratio: ratio }
                : { api_key_id: id, period, action, limit_amount: amount, warn_ratio: ratio },
            {
                onSuccess: () => { toast.success(t('budgets.created')); setScopeId(''); setLimit(''); void refetch(); },
                onError: (error) => toast.error(error instanceof Error ? error.message : t('budgets.createFailed')),
            },
        );
    };

    const scopeLabel = (row: SpendBudget) => (row.user_id > 0 ? t('budgets.userScope', { id: row.user_id }) : t('budgets.keyScope', { id: row.api_key_id }));
    const columns: Col<SpendBudget>[] = [
        { key: 'scope', label: t('budgets.scope'), render: (row) => <span className='text-sm'>{scopeLabel(row)}</span> },
        { key: 'period', label: t('budgets.period'), render: (row) => (
            <Badge variant='outline' className='bg-muted text-muted-foreground'>{row.period === 'month' ? t('budgets.month') : t('budgets.day')}</Badge>
        ) },
        { key: 'limit', label: t('budgets.limit'), render: (row) => <span className='font-mono text-xs'>{'$' + row.limit_amount.toFixed(2)}</span> },
        { key: 'action', label: t('budgets.action'), render: (row) => (
            <Badge variant='outline' className={row.action === 'block' ? 'bg-red-500/15 text-red-600 dark:text-red-400' : 'bg-amber-500/15 text-amber-700 dark:text-amber-400'}>
                {row.action === 'block' ? t('budgets.block') : t('budgets.warn')}
            </Badge>
        ) },
        { key: 'warn', label: t('budgets.warnRatio'), render: (row) => String(row.warn_ratio) + '%' },
        { key: 'state', label: t('budgets.state'), render: (row) => (
            <Badge variant='outline' className={row.enabled ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'}>
                {row.enabled ? t('budgets.enabled') : t('budgets.disabled')}
            </Badge>
        ) },
        { key: 'ops', label: '', render: (row) => (
            <span className='flex gap-1'>
                <Button size='sm' variant='ghost' className='h-7 px-2 text-xs'
                    onClick={() => update.mutate({ id: row.id, period: row.period, action: row.action, limit_amount: row.limit_amount, warn_ratio: row.warn_ratio, enabled: !row.enabled },
                        { onSuccess: () => void refetch(), onError: () => toast.error(t('budgets.updateFailed')) })}>
                    {row.enabled ? t('budgets.disable') : t('budgets.enable')}
                </Button>
                <Button size='sm' variant='ghost' className='h-7 px-2 text-xs text-red-600 dark:text-red-400' disabled={remove.isPending}
                    onClick={() => remove.mutate(row.id, {
                        onSuccess: () => toast.success(t('budgets.deleted')),
                        onError: (error) => toast.error(error instanceof Error ? error.message : t('budgets.deleteFailed')),
                    })}>
                    {t('budgets.delete')}
                </Button>
            </span>
        ) },
    ];

    return (
        <div className='space-y-3'>
            <p className='text-sm text-muted-foreground'>{t('budgets.desc')}</p>
            <div className='flex flex-wrap items-end gap-2'>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('budgets.scope')}
                    <Select value={scope} onValueChange={setScope}>
                        <SelectTrigger className='h-9 w-32 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='user'>{t('budgets.user')}</SelectItem>
                            <SelectItem value='key'>{t('budgets.key')}</SelectItem>
                        </SelectContent>
                    </Select>
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {scope === 'user' ? t('budgets.userId') : t('budgets.keyId')}
                    <Input value={scopeId} onChange={(e) => setScopeId(e.target.value)} className='h-9 w-24 rounded-xl' inputMode='numeric' />
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('budgets.period')}
                    <Select value={period} onValueChange={setPeriod}>
                        <SelectTrigger className='h-9 w-28 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='day'>{t('budgets.day')}</SelectItem>
                            <SelectItem value='month'>{t('budgets.month')}</SelectItem>
                        </SelectContent>
                    </Select>
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('budgets.action')}
                    <Select value={action} onValueChange={setAction}>
                        <SelectTrigger className='h-9 w-28 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='warn'>{t('budgets.warn')}</SelectItem>
                            <SelectItem value='block'>{t('budgets.block')}</SelectItem>
                        </SelectContent>
                    </Select>
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('budgets.limit')}
                    <Input value={limit} onChange={(e) => setLimit(e.target.value)} className='h-9 w-28 rounded-xl' inputMode='decimal' placeholder='10.00' />
                </label>
                <label className='flex flex-col gap-1 text-xs text-muted-foreground'>
                    {t('budgets.warnRatio')}
                    <Input value={warnRatio} onChange={(e) => setWarnRatio(e.target.value)} className='h-9 w-20 rounded-xl' inputMode='numeric' />
                </label>
                <Button size='sm' disabled={create.isPending} onClick={submit}>{create.isPending ? t('budgets.creating') : t('budgets.create')}</Button>
            </div>
            <OpsTable columns={columns} rows={data ?? []} empty={isLoading ? t('loading') : t('budgets.none')} rowKey={(row) => String(row.id)} />
        </div>
    );
}

function RateLimitTopSection() {
    const t = useTranslations('ops');
    const [hours, setHours] = useState('24');
    const { data, isLoading, isError, refetch, isRefetching } = useRateUsageTop(Number(hours), 10);
    
    const scopeTypeBadge = (scopeType: string) => {
        const map: Record<string, { label: string; className: string }> = {
            system: { label: t('ratelimit.scopeSystem'), className: 'bg-slate-500/15 text-slate-600 dark:text-slate-400' },
            user: { label: t('ratelimit.scopeUser'), className: 'bg-sky-500/15 text-sky-600 dark:text-sky-400' },
            api_key: { label: t('ratelimit.scopeApiKey'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
            group: { label: t('ratelimit.scopeGroup'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
            channel: { label: t('ratelimit.scopeChannel'), className: 'bg-purple-500/15 text-purple-600 dark:text-purple-400' },
            channel_key: { label: t('ratelimit.scopeChannelKey'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            channel_model: { label: t('ratelimit.scopeChannelModel'), className: 'bg-pink-500/15 text-pink-600 dark:text-pink-400' },
        };
        const item = map[scopeType] ?? { label: scopeType, className: 'bg-muted text-muted-foreground' };
        return <Badge variant='outline' className={item.className}>{item.label}</Badge>;
    };
    
    const columns: Col<import('@/api/ratelimit').RateUsageTopItem>[] = [
        { key: 'entity_name', label: t('ratelimit.entity'), render: (row) => (
            <span className='block max-w-56 truncate font-medium' title={row.entity_name}>
                {row.entity_name}
            </span>
        ) },
        { key: 'scope_type', label: t('ratelimit.scope'), render: (row) => scopeTypeBadge(row.scope_type) },
        { key: 'rejected', label: t('ratelimit.rejected'), render: (row) => (
            <span className='font-semibold text-red-600 dark:text-red-400'>{row.rejected.toLocaleString()}</span>
        ) },
        { key: 'requests', label: t('ratelimit.requests'), render: (row) => row.requests.toLocaleString() },
        { key: 'rejected_pct', label: t('ratelimit.rejectedPct'), render: (row) => (
            <span className={'font-medium ' + (row.rejected_pct > 10 ? 'text-red-600 dark:text-red-400' : row.rejected_pct > 5 ? 'text-amber-600 dark:text-amber-500' : '')}>
                {row.rejected_pct.toFixed(1)}%
            </span>
        ) },
    ];
    
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='max-w-3xl text-sm text-muted-foreground'>{t('ratelimit.desc')}</p>
                <div className='flex items-center gap-2'>
                    <Select value={hours} onValueChange={setHours}>
                        <SelectTrigger className='h-9 w-32 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='1'>{t('reconcile.days1')}</SelectItem>
                            <SelectItem value='24'>{t('ratelimit.hours24')}</SelectItem>
                            <SelectItem value='72'>{t('ratelimit.hours72')}</SelectItem>
                            <SelectItem value='168'>{t('reconcile.days7')}</SelectItem>
                        </SelectContent>
                    </Select>
                    <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable 
                    columns={columns} 
                    rows={data?.items ?? []} 
                    empty={isLoading ? t('loading') : t('ratelimit.empty')}
                    rowKey={(row) => row.scope_type + '-' + row.scope_id} 
                />
            )}
        </div>
    );
}

// TasksSection 后台任务运行状态: 回答"上次何时跑的、成了没有、现在是否卡着"。
// 纯只读展示 —— 这里没有任何操作入口, 管任务的去设置页改开关/周期, 出问题的看告警。
function TasksSection() {
    const t = useTranslations('ops');
    const { data, error, isLoading, isRefetching, refetch } = useTaskStatus();
    const tasks: TaskStatus[] = data ?? [];

    // intervalLabel 周期人读格式; 未调度的任务显示关闭。
    const intervalLabel = (task: TaskStatus) => {
        if (!task.scheduled) return t('tasks.off');
        const ms = task.interval_ns / 1e6;
        if (ms >= 86400000 && ms % 86400000 === 0) return (ms / 86400000) + 'd';
        if (ms >= 3600000 && ms % 3600000 === 0) return (ms / 3600000) + 'h';
        if (ms >= 60000 && ms % 60000 === 0) return (ms / 60000) + 'm';
        if (ms >= 1000) return (ms / 1000) + 's';
        return ms + 'ms';
    };

    // ranAt 把运行时间转成人读; 零值(RFC3339 的 0001 年)表示从未发生。
    const ranAt = (value: string) => {
        if (!value || value.startsWith('0001-')) return t('tasks.never');
        return formatTime(value);
    };

    const columns: Col<TaskStatus>[] = [
        { key: 'name', label: t('tasks.name'), render: (row) => <span className='font-mono text-xs'>{row.name}</span> },
        {
            key: 'status',
            label: t('tasks.status'),
            render: (row) => row.running
                ? <Badge className='bg-sky-500/15 text-sky-600 dark:text-sky-400'>{t('tasks.running')}</Badge>
                : row.consecutive_failures > 0
                    ? <Badge className='bg-red-500/15 text-red-600 dark:text-red-400'>{t('tasks.failing', { count: row.consecutive_failures })}</Badge>
                    : row.scheduled
                        ? <Badge className='bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'>{t('tasks.ok')}</Badge>
                        : <Badge className='bg-zinc-500/15 text-zinc-600 dark:text-zinc-400'>{t('tasks.off')}</Badge>,
        },
        { key: 'interval', label: t('tasks.interval'), render: (row) => <span className='font-mono text-xs'>{intervalLabel(row)}</span> },
        { key: 'lastSuccess', label: t('tasks.lastSuccess'), render: (row) => ranAt(row.last_success) },
        { key: 'lastFinish', label: t('tasks.lastFinish'), render: (row) => ranAt(row.last_finish) },
        {
            key: 'lastDuration',
            label: t('tasks.lastDuration'),
            render: (row) => row.last_duration_ns > 0 ? (row.last_duration_ns / 1e9).toFixed(2) + 's' : '-',
        },
        {
            key: 'lastError',
            label: t('tasks.lastError'),
            render: (row) => row.last_error
                ? <span className='line-clamp-2 max-w-72 text-xs text-red-600 dark:text-red-400'>{row.last_error}</span>
                : <span className='text-xs text-muted-foreground'>-</span>,
        },
    ];

    return (
        <div className='space-y-3'>
            <div className='flex items-center justify-between'>
                <p className='text-sm text-muted-foreground'>{t('tasks.desc')}</p>
                <RefreshButton onClick={() => void refetch()} busy={isRefetching} label={t('refresh')} />
            </div>
            {error ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <OpsTable columns={columns} rows={tasks} empty={isLoading ? t('loading') : t('empty')} rowKey={(row) => row.name} />
            )}
        </div>
    );
}

