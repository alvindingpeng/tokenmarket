import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { AlertTriangle, CircleCheck, CircleHelp, CircleSlash, RefreshCw, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { accountList, accountRefreshKey, type AccountListEntry } from '@/api/channel';
import { ListErrorState } from '@/components/common/ListErrorState';
import { accountStateOfAccount, remainingOf } from '@/components/modules/channel/accountState';

// 账号页: 把所有渠道下的上游账号汇总成一张可运维的表。
//
// 为什么需要这个页面: 导入之后账号散落在各渠道的凭据列表里, 而它们会自己到期、
// 自己失效、自己在后台被刷新。没有这张汇总表, 使用者只能一个渠道一个渠道点进去看 ——
// 等于逼他们把"定时验证"当成不存在。这个页面存在的全部意义就是让失效尽早可见。

const ICONS = {
    healthy: CircleCheck,
    expiring: AlertTriangle,
    manual: CircleHelp,
    invalid: CircleSlash,
} as const;

const TONES = {
    healthy: 'text-emerald-600 dark:text-emerald-500',
    expiring: 'text-amber-600 dark:text-amber-500',
    manual: 'text-muted-foreground',
    invalid: 'text-destructive',
} as const;

// formatTime 把 RFC3339 转本地时间; 空值显示"-"而不是"Invalid Date"。
function formatTime(value?: string | null): string {
    if (!value) return '-';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function Accounts() {
    const t = useTranslations('accounts');
    const queryClient = useQueryClient();
    const [search, setSearch] = useState('');
    const [refreshing, setRefreshing] = useState<number | null>(null);

    const query = useQuery({
        queryKey: ['account-list'],
        queryFn: accountList,
        // 账号状态随时间变化(还剩多久到期), 但不必秒级刷新: 定时任务是分钟级,
        // 每 60 秒重取一次已足够让"将到期→已失效"的变化被看见。
        refetchInterval: 60_000,
    });

    const rows = query.data ?? [];

    // 搜索按渠道名/凭据名/平台/邮箱匹配。账号少的时候用不上, 但渠道一多就是刚需 ——
    // 而"渠道一多"正是需要这个页面的时期。
    const filtered = useMemo(() => {
        const keyword = search.trim().toLowerCase();
        if (!keyword) return rows;
        return rows.filter((row) =>
            row.channel_name.toLowerCase().includes(keyword)
            || row.name.toLowerCase().includes(keyword)
            || row.platform.toLowerCase().includes(keyword)
            || row.email.toLowerCase().includes(keyword));
    }, [rows, search]);

    // 汇总: 顶部要能一眼看到"有几个账号有问题"。
    const counts = useMemo(() => {
        const initial = { healthy: 0, expiring: 0, manual: 0, invalid: 0 };
        for (const row of rows) initial[accountStateOfAccount(row)] += 1;
        return initial;
    }, [rows]);

    const refresh = async (row: AccountListEntry) => {
        setRefreshing(row.channel_key_id);
        try {
            await accountRefreshKey(row.channel_key_id);
            toast.success(t('refreshed'));
            // 刷新成功后状态变了, 重取列表让新到期时间立刻可见。
            await queryClient.invalidateQueries({ queryKey: ['account-list'] });
        } catch (error) {
            toast.error((error as Error).message);
        } finally {
            setRefreshing(null);
        }
    };

    if (query.isError) {
        return <ListErrorState onRetry={() => void query.refetch()} retrying={query.isFetching} />;
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {/* 顶部: 汇总 + 搜索。汇总让"有问题"这件事在打开页面时就可见, 而不是需要逐行看。 */}
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <SummaryBadge label={t('state.healthy')} count={counts.healthy} state="healthy" />
                <SummaryBadge label={t('state.expiring')} count={counts.expiring} state="expiring" />
                <SummaryBadge label={t('state.manual')} count={counts.manual} state="manual" />
                <SummaryBadge label={t('state.invalid')} count={counts.invalid} state="invalid" />
                <div className="relative ml-auto w-56">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder={t('searchPlaceholder')}
                        className="h-9 pl-8"
                    />
                </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto rounded-xl border">
                <table className="w-full text-sm">
                    <thead className="sticky top-0 z-10 border-b bg-card">
                        <tr className="text-left text-xs text-muted-foreground">
                            <th className="p-2.5 font-medium">{t('table.state')}</th>
                            <th className="p-2.5 font-medium">{t('table.account')}</th>
                            <th className="p-2.5 font-medium">{t('table.channel')}</th>
                            <th className="p-2.5 font-medium">{t('table.expires')}</th>
                            <th className="p-2.5 font-medium">{t('table.lastRefresh')}</th>
                            <th className="p-2.5 font-medium">{t('table.action')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {filtered.map((row) => {
                            const state = accountStateOfAccount(row);
                            const Icon = ICONS[state];
                            const remaining = remainingOf(row.token_expires_at ?? undefined);
                            // cookie 类没有续期能力, "刷新"按钮对它没有意义 ——
                            // 摆一个点不动的按钮比不摆更糟。
                            const canRefresh = row.kind === 'oauth';
                            return (
                                <tr key={row.channel_key_id} className="border-b last:border-b-0">
                                    <td className="p-2.5">
                                        <span className={`inline-flex items-center gap-1 text-xs ${TONES[state]}`}>
                                            <Icon className="size-3.5" />
                                            {t(`state.${state}`)}
                                            {state === 'expiring' && remaining && (
                                                <span className="text-muted-foreground">
                                                    {remainingLabel(t, remaining)}
                                                </span>
                                            )}
                                        </span>
                                    </td>
                                    <td className="p-2.5">
                                        <div className="flex flex-col gap-0.5">
                                            <span className="font-mono text-xs">{row.name}</span>
                                            <span className="flex items-center gap-1.5">
                                                <Badge variant="secondary" className="rounded px-1.5 py-0 text-[10px] uppercase">{row.platform}</Badge>
                                                {/* cookie 与 oauth 混在一张表里时必须标出来:
                                                    它们的运维方式完全不同(前者只能重导, 后者能自动续)。 */}
                                                {row.kind === 'cookie' && (
                                                    <span className="text-[10px] text-muted-foreground">{t('cookieTag')}</span>
                                                )}
                                                {row.email && <span className="truncate text-[11px] text-muted-foreground">{row.email}</span>}
                                                {!row.enabled && <span className="text-[10px] text-destructive">{t('disabled')}</span>}
                                            </span>
                                        </div>
                                    </td>
                                    <td className="p-2.5 text-xs text-muted-foreground">{row.channel_name}</td>
                                    <td className="p-2.5 text-xs">
                                        {row.token_expires_at ? (
                                            <Tooltip time={row.token_expires_at}>{formatTime(row.token_expires_at)}</Tooltip>
                                        ) : <span className="text-muted-foreground">{t('never')}</span>}
                                    </td>
                                    <td className="p-2.5 text-xs">
                                        {row.token_refresh_at
                                            ? <Tooltip time={row.token_refresh_at}>{formatTime(row.token_refresh_at)}</Tooltip>
                                            : <span className="text-muted-foreground">{t('neverRefreshed')}</span>}
                                    </td>
                                    <td className="p-2.5">
                                        <Button
                                            size="sm"
                                            variant="outline"
                                            className="h-7 rounded-lg px-2 text-xs"
                                            disabled={!canRefresh || refreshing === row.channel_key_id}
                                            onClick={() => void refresh(row)}
                                        >
                                            <RefreshCw className={`size-3 ${refreshing === row.channel_key_id ? 'animate-spin' : ''}`} />
                                            {t('refreshNow')}
                                        </Button>
                                    </td>
                                </tr>
                            );
                        })}
                        {filtered.length === 0 && (
                            <tr>
                                <td colSpan={6} className="p-8 text-center text-sm text-muted-foreground">
                                    {rows.length === 0 ? t('empty') : t('noMatch')}
                                </td>
                            </tr>
                        )}
                    </tbody>
                </table>
            </div>
        </div>
    );
}

// remainingLabel 把剩余时长换成当前语言的词。
// 数值本身由 accountState 给出, 这里只负责取词 —— 判据与展示分开, 两者才能各自测试。
function remainingLabel(t: ReturnType<typeof useTranslations>, remaining: ReturnType<typeof remainingOf>): string {
    if (!remaining) return '';
    if ('minutes' in remaining) return t('remainingMinutes', { count: remaining.minutes });
    if ('hours' in remaining) return t('remainingHours', { count: remaining.hours });
    return t('remainingDays', { count: remaining.days });
}

function SummaryBadge({ label, count, state }: { label: string; count: number; state: keyof typeof ICONS }) {
    const Icon = ICONS[state];
    return (
        <span className={`inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-xs ${count > 0 ? TONES[state] : 'text-muted-foreground'}`}>
            <Icon className="size-3.5" />
            {label}
            <span className="font-mono">{count}</span>
        </span>
    );
}

// Tooltip 显示完整时间; 表格里的时间被截断过, 悬停要能看到原值。
function Tooltip({ time, children }: { time: string; children: React.ReactNode }) {
    return <span title={time}>{children}</span>;
}
