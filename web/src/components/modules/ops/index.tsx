import { useState, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { Archive, DatabaseBackup, HeartPulse, RefreshCw, Scale, ShieldAlert, Wallet } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { toast } from 'sonner';
import {
    useArchives, useBackups, useBalanceFlow, useChannelAlerts, useChannelHealth,
    useOpsOverview, useReconcileList, useRunBackup, useRunHealthCheck, useRunLogLifecycle, useRunReconcile,
    type BalanceFlow, type ChannelAlert, type ChannelHealth, type ReconcileRow,
} from '@/api/ops';

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

const TABS = [
    { id: 'reconcile', icon: Scale },
    { id: 'flow', icon: Wallet },
    { id: 'backup', icon: DatabaseBackup },
    { id: 'archive', icon: Archive },
    { id: 'health', icon: HeartPulse },
    { id: 'alerts', icon: ShieldAlert },
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
                {tab === 'flow' && <FlowSection />}
                {tab === 'backup' && <BackupSection />}
                {tab === 'archive' && <ArchiveSection />}
                {tab === 'health' && <HealthSection />}
                {tab === 'alerts' && <AlertsSection />}
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
    };
    const item = map[status] ?? { label: status, className: 'bg-muted text-muted-foreground' };
    return <Badge variant='outline' className={item.className}>{item.label}</Badge>;
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

// alertKindBadge 告警类型徽章: 宕机红/恢复绿/降级黄。
function AlertsSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, refetch, isRefetching } = useChannelAlerts(50);
    const kindBadge = (kind: string) => {
        const map: Record<string, { label: string; className: string }> = {
            down: { label: t('alerts.kindDown'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            recovered: { label: t('alerts.kindRecovered'), className: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
            degraded: { label: t('alerts.kindDegraded'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
            mismatch: { label: t('alerts.kindMismatch'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            backup_failed: { label: t('alerts.kindBackup'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            archive_failed: { label: t('alerts.kindArchive'), className: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
            billing_failed: { label: t('alerts.kindBilling'), className: 'bg-red-500/15 text-red-600 dark:text-red-400' },
            low_balance: { label: t('alerts.kindLowBalance'), className: 'bg-orange-500/15 text-orange-600 dark:text-orange-500' },
            test: { label: t('alerts.kindTest'), className: 'bg-muted text-muted-foreground' },
        };
        const item = map[kind] ?? { label: kind, className: 'bg-muted text-muted-foreground' };
        return <Badge variant='outline' className={item.className}>{item.label}</Badge>;
    };
    const columns: Col<ChannelAlert>[] = [
        { key: 'channel_code', label: t('alerts.channel') },
        { key: 'kind', label: t('alerts.kind'), render: (row) => kindBadge(row.kind) },
        { key: 'reason', label: t('alerts.reason'), render: (row) => <span className='block max-w-96 truncate' title={row.reason}>{row.reason || '-'}</span> },
        { key: 'notified', label: t('alerts.notified'), render: (row) => (row.notified ? t('alerts.yes') : t('alerts.no')) },
        { key: 'created_at', label: t('alerts.time'), render: (row) => formatTime(row.created_at) },
    ];
    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('alerts.desc')}</p>
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
