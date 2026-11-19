import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { RefreshCw } from 'lucide-react';
import { formatAmount, formatDateTime, formatTokenCount } from '@/lib/format';
import { useMetricsSnapshot, type MetricsSnapshot } from '@/api/ops';

// MetricsSection /metrics 抓取口径的只读观测面板: 展示累计请求(按终态)、token、成本、
// 用户与渠道计数, 30 秒自动刷新。
//
// 数据源是 GET /api/v1/ops/metrics/snapshot, 它复用 op.CollectMetrics() ——
// 与 Prometheus 抓走的数字同源。这里刻意不做任何"二次统计"(比如自己算成功率):
// 面板一旦自己算数, 就会和抓取端悄悄分叉, 排查时两边对不上。
// 面板只读, 没有重置/轮换入口; 令牌相关操作在设置页(会写库的才需要 step-up)。

// STATUS_ORDER 终态展示顺序: 与 internal/relay/state.go 的终态定义一致。
// success/failed/canceled 之外的键(理论上不存在)按字母序附在后面, 免得将来新增状态在这里静默消失。
const STATUS_ORDER = ['success', 'failed', 'canceled'];

type StatusRow = { status: string; count: number };

// statusRows 把快照里的 requests_by_status 摊平成可排序的行。
// 不补 0: 快照里没有这个状态 = 从来没有请求落进该终态, 表格里也不该凭空多一行。
function statusRows(counts: Record<string, number> | undefined): StatusRow[] {
    if (!counts) return [];
    const known = STATUS_ORDER.filter((status) => status in counts)
        .map((status) => ({ status, count: counts[status] }));
    const others = Object.keys(counts)
        .filter((status) => !STATUS_ORDER.includes(status))
        .sort()
        .map((status) => ({ status, count: counts[status] }));
    return [...known, ...others];
}

// formatCost 与运维概览同一个金额口径: 保留到 6 位小数再裁掉尾零。
// 不并进 @/lib/format 是因为概览卡片在本模块内已有一份, 这里保持一致即可。
function formatCost(value: number | undefined): string {
    const fixed = Number(value ?? 0).toFixed(6);
    return fixed.includes('.') ? fixed.replace(/0+$/, '').replace(/\.$/, '') : fixed;
}

// lastRefresh 最后刷新时间: 优先用本地拿到数据的时刻(dataUpdatedAt), 后端时间只作兜底。
// 展示"我什么时候看到的这份数据"比展示"服务端什么时候组装的"更贴合"最后刷新"的语义;
// 两者时钟不一致时(容器与浏览器都对不上)以后端时间为准会更误导。
function lastRefresh(dataUpdatedAt: number, snapshot: MetricsSnapshot | undefined, fallback: string): string {
    if (dataUpdatedAt > 0) return new Date(dataUpdatedAt).toLocaleString();
    return formatDateTime(snapshot?.generated_at, fallback);
}

export function MetricsSection() {
    const t = useTranslations('ops');
    const { data, isLoading, isError, isRefetching, refetch, dataUpdatedAt } = useMetricsSnapshot();

    const statusLabels: Record<string, string> = {
        success: t('metrics.success'),
        failed: t('metrics.failed'),
        canceled: t('metrics.canceled'),
    };
    const rows = statusRows(data?.requests_by_status);

    // cards 每一项都直接对应快照里的一个字段; 未加载完显示占位符而不是 0,
    // 假零会被当成"库里一条请求都没有"读。
    const cards = [
        { key: 'success', label: t('metrics.success'), value: data ? String(data.requests_by_status?.success ?? 0) : '-' },
        { key: 'failed', label: t('metrics.failed'), value: data ? String(data.requests_by_status?.failed ?? 0) : '-' },
        { key: 'canceled', label: t('metrics.canceled'), value: data ? String(data.requests_by_status?.canceled ?? 0) : '-' },
        { key: 'inputTokens', label: t('metrics.inputTokens'), value: data ? formatTokenCount(data.input_tokens) : '-' },
        { key: 'outputTokens', label: t('metrics.outputTokens'), value: data ? formatTokenCount(data.output_tokens) : '-' },
        { key: 'cost', label: t('metrics.cost'), value: data ? formatCost(data.cost) : '-' },
        { key: 'users', label: t('metrics.users'), value: data ? String(data.users) : '-' },
        { key: 'balance', label: t('metrics.balance'), value: data ? formatAmount(data.balance) : '-' },
        { key: 'channels', label: t('metrics.channels'), value: data ? `${data.channels_enabled}/${data.channels}` : '-' },
        { key: 'alerts', label: t('metrics.alerts'), value: data ? String(data.alerts_24h) : '-' },
    ];

    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
                <p className='text-sm text-muted-foreground'>{t('metrics.desc')}</p>
                <div className='flex items-center gap-3'>
                    <span className='text-xs text-muted-foreground tabular-nums'>
                        {t('metrics.lastRefresh')}: {data ? lastRefresh(dataUpdatedAt, data, '-') : '-'}
                    </span>
                    <Button variant='outline' size='sm' onClick={() => void refetch()} disabled={isRefetching} className='rounded-xl'>
                        <RefreshCw className={'size-4' + (isRefetching ? ' animate-spin' : '')} />
                        {t('refresh')}
                    </Button>
                </div>
            </div>
            {isError ? (
                <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{t('loadFailed')}</div>
            ) : (
                <>
                    <div className='grid shrink-0 grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5'>
                        {cards.map((card) => (
                            <div key={card.key} className='rounded-2xl border border-border bg-card px-4 py-3'>
                                <div className='text-xs text-muted-foreground'>{card.label}</div>
                                <div className='truncate text-lg font-semibold tabular-nums text-card-foreground'>{card.value}</div>
                            </div>
                        ))}
                    </div>
                    <div className='space-y-2'>
                        <div className='flex items-center gap-2'>
                            <Badge variant='outline' className='bg-sky-500/15 text-sky-600 dark:text-sky-400'>/metrics</Badge>
                            <span className='text-sm font-medium text-card-foreground'>{t('metrics.statusTitle')}</span>
                        </div>
                        {rows.length === 0 ? (
                            <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>
                                {isLoading ? t('loading') : t('metrics.statusEmpty')}
                            </div>
                        ) : (
                            <div className='overflow-x-auto rounded-3xl border border-border bg-card'>
                                <table className='w-full min-w-[320px] text-sm'>
                                    <thead>
                                        <tr className='border-b border-border text-left text-xs text-muted-foreground'>
                                            <th className='px-4 py-3 font-medium'>{t('metrics.status')}</th>
                                            <th className='px-4 py-3 font-medium'>{t('metrics.count')}</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {rows.map((row) => (
                                            <tr key={row.status} className='border-b border-border/60 last:border-0 hover:bg-muted/40'>
                                                <td className='px-4 py-2.5 align-middle font-mono text-xs'>{statusLabels[row.status] ?? row.status}</td>
                                                <td className='px-4 py-2.5 align-middle tabular-nums'>{row.count}</td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                </>
            )}
        </div>
    );
}
