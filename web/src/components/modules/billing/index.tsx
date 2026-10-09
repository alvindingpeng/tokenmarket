import { useMemo, useState } from 'react';
import type { LucideIcon } from 'lucide-react';
import { ArrowDownToLine, ArrowUpFromLine, ChevronLeft, ChevronRight, CreditCard, Database, Download, Loader2, PiggyBank, Receipt, Search, Snowflake } from 'lucide-react';
import { useTranslations } from 'use-intl';
import dayjs from 'dayjs';
import { billingExportUrl, useAccountProfile, useBalanceLedger, useBillingDaily, useBillingRecords, useRevenueDaily, type BillingDaily, type BillingFilter } from '@/api/billing';
import { formatPlainMoney } from '@/lib/money';
import { useAuth } from '@/api/user';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';

// 计费金额可能不足 0.001, 固定 6 位小数再截尾零（与 share 模块同一口径, 实现见 lib/money）。
const formatMoney = formatPlainMoney;

// 词元为整数, 只显示千位分隔符。
function formatCount(value: number): string {
    return value.toLocaleString('en-US');
}

// BillingDaily.date 形如 20260107, 表格内只显示月/日。
function dayLabel(date: string): string {
    return date.length === 6 ? `${date.slice(4, 6)}/${date.slice(6, 8)}` : date;
}

// TokenCell 是四类词元的紧凑展示, 行内自动换行而不是横向溢出。
function TokenCell({ values }: { values: { label: string; value: number }[] }) {
    const t = useTranslations('billing');
    return (
        <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 font-mono text-xs tabular-nums">
            {values.map(({ label, value }) => (
                <span key={label} className="text-muted-foreground">
                    {label} <span className="text-foreground">{formatCount(value)}</span>
                </span>
            ))}
        </span>
    );
}

// DailyTokenCell 是日聚合行的词元列; 文案在组件内翻译, 避免把 useTranslations 的函数类型当参数传来传去。
function DailyTokenCell({ row }: { row: BillingDaily }) {
    const t = useTranslations('billing');
    return (
        <TokenCell values={[
            { label: t('tokens.input'), value: row.input_token },
            { label: t('tokens.output'), value: row.output_token },
            { label: t('tokens.cacheRead'), value: row.cache_read_token },
            { label: t('tokens.cacheWrite'), value: row.cache_write_token },
        ]} />
    );
}

// Skeleton 是区块加载中的占位。
function Skeleton() {
    return <div className="flex h-8 items-center justify-center text-muted-foreground"><Loader2 className="size-4 animate-spin" /></div>;
}

// AccountCard 渲染账户概览中的一张金额卡片; hint 用于标注口径（如冻结额为预扣中）。
function AccountCard({ label, icon: Icon, value, hint, iconBg }: {
    label: string;
    icon: LucideIcon;
    value: string;
    hint?: string;
    iconBg: string;
}) {
    return (
        <section className="rounded-3xl bg-card text-card-foreground border-border border p-5">
            <div className="flex items-center gap-3">
                <div className={cn('flex size-10 shrink-0 items-center justify-center rounded-xl text-primary', iconBg)}>
                    <Icon className="size-5" />
                </div>
                <div className="min-w-0">
                    <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <span className="truncate">{label}</span>
                        {hint && (
                            <span className="shrink-0 rounded-full border border-border bg-background/80 px-1.5 py-px text-[10px] leading-none text-muted-foreground">
                                {hint}
                            </span>
                        )}
                    </div>
                    <div className={cn('mt-0.5 truncate text-xl font-bold tabular-nums', value === '' && 'opacity-40')}>
                        {value || '--'}
                    </div>
                </div>
            </div>
        </section>
    );
}

// Overview 渲染账户概览卡片; 收入与充值两项仅渠道商/管理员可见。
function Overview({ owner }: { owner: boolean }) {
    const t = useTranslations('billing');
    const { data: profile, isLoading, isError } = useAccountProfile();

    // 前三张卡人人可见, 后两张按 owner 追加; key 只用于 React 列表去重。
    const cards = useMemo(() => {
        const items = [
            { key: 'balance', label: t('account.balance'), value: profile?.balance ?? 0, icon: PiggyBank, iconBg: 'bg-emerald-500/10' },
            { key: 'frozen', label: t('account.frozen'), hint: t('account.frozenHint'), value: profile?.frozen ?? 0, icon: Snowflake, iconBg: 'bg-sky-500/10' },
            { key: 'totalSpent', label: t('account.totalSpent'), value: profile?.total_spent ?? 0, icon: ArrowDownToLine, iconBg: 'bg-red-500/10' },
        ];

        if (owner) {
            items.push({ key: 'totalRevenue', label: t('account.totalRevenue'), value: profile?.total_revenue ?? 0, icon: ArrowUpFromLine, iconBg: 'bg-green-500/10' });
            items.push({ key: 'totalRecharged', label: t('account.totalRecharged'), value: profile?.total_recharged ?? 0, icon: CreditCard, iconBg: 'bg-amber-500/10' });
        }

        return items;
    }, [owner, profile, t]);

    return (
        <section>
            <h2 className="mb-3 flex items-center gap-2 px-1 text-base font-semibold text-muted-foreground">
                <CreditCard className="size-4" />
                {t('account.title')}
            </h2>
            {isError && (
                <p className="mb-3 rounded-2xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm text-destructive">
                    {t('loadFailed')}
                </p>
            )}
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
                {cards.map((card) => (
                    <AccountCard
                        key={card.key}
                        label={card.label}
                        icon={card.icon}
                        iconBg={card.iconBg}
                        hint={card.hint}
                        value={isLoading ? '' : formatMoney(card.value)}
                    />
                ))}
            </div>
        </section>
    );
}

// PAGE_SIZE 明细与流水的每页条数, 与后端 limit 上限保持一致。
const PAGE_SIZE = 20;

// Pager 通用分页控件: 上一页/下一页 + 页码与总数。
function Pager({ page, total, onChange, label }: { page: number; total: number; onChange: (page: number) => void; label: (page: number, pages: number, total: number) => string }) {
    const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
    return (
        <div className="flex items-center justify-end gap-2 pt-2 text-xs text-muted-foreground">
            <span>{label(page, pages, total)}</span>
            <Button variant="outline" size="sm" className="size-7 rounded-lg p-0" disabled={page <= 1} onClick={() => onChange(page - 1)}>
                <ChevronLeft className="size-4" />
            </Button>
            <Button variant="outline" size="sm" className="size-7 rounded-lg p-0" disabled={page >= pages} onClick={() => onChange(page + 1)}>
                <ChevronRight className="size-4" />
            </Button>
        </div>
    );
}

// SpendingTable 渲染计费明细表: 模型/渠道/状态/时间筛选 + 分页 + CSV 导出; 收入列只在渠道商/管理员视图展示。
function SpendingTable({ owner }: { owner: boolean }) {
    const t = useTranslations('billing');
    const [page, setPage] = useState(1);
    const [draftModel, setDraftModel] = useState('');
    const [draftChannel, setDraftChannel] = useState('');
    const [status, setStatus] = useState('');
    const [from, setFrom] = useState('');
    const [to, setTo] = useState('');
    const [filter, setFilter] = useState<BillingFilter>({});
    const { data, isLoading, isError } = useBillingRecords(filter, PAGE_SIZE, (page - 1) * PAGE_SIZE);
    const records = data?.items ?? [];
    const total = data?.total ?? 0;

    // 筛选条件一次提交: 页码回到第一页, 避免停在已不存在的页。
    const applyFilter = () => {
        setPage(1);
        setFilter({
            model: draftModel.trim() || undefined,
            channel: draftChannel.trim() || undefined,
            status: status || undefined,
            from: from ? new Date(from).toISOString() : undefined,
            to: to ? new Date(to + 'T23:59:59').toISOString() : undefined,
        });
    };

    // 列宽随是否展示"收入"列变化, 用同一份模板字符串拼接。
    const gridTemplate = useMemo(() => {
        const base = '150px minmax(180px,1.7fr) minmax(100px,1fr) minmax(210px,1.5fr) 110px';
        return owner ? `${base} 110px 90px` : base + ' 90px';
    }, [owner]);

    return (
        <section className="rounded-3xl bg-card text-card-foreground border-border border pt-3 pb-3 px-4">
            <header className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-base font-semibold">
                    <CreditCard className="size-4 text-muted-foreground" />
                    {t('details.title')}
                </h2>
                <div className="flex flex-wrap items-center gap-2">
                    <Input className="h-8 w-28 rounded-xl" placeholder={t('filters.model')} value={draftModel}
                        onChange={(event) => setDraftModel(event.target.value)}
                        onKeyDown={(event) => { if (event.key === 'Enter') applyFilter(); }} />
                    <Input className="h-8 w-28 rounded-xl" placeholder={t('filters.channel')} value={draftChannel}
                        onChange={(event) => setDraftChannel(event.target.value)}
                        onKeyDown={(event) => { if (event.key === 'Enter') applyFilter(); }} />
                    <Select value={status} onValueChange={(value) => { setStatus(value); }}>
                        <SelectTrigger className="h-8 w-28 rounded-xl"><SelectValue placeholder={t('filters.statusAll')} /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value="">{t('filters.statusAll')}</SelectItem>
                            <SelectItem value="settled">{t('status.settled')}</SelectItem>
                            <SelectItem value="refunded">{t('status.refunded')}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Input type="date" className="h-8 w-36 rounded-xl" value={from} onChange={(event) => setFrom(event.target.value)} />
                    <Input type="date" className="h-8 w-36 rounded-xl" value={to} onChange={(event) => setTo(event.target.value)} />
                    <Button variant="outline" size="sm" className="h-8 rounded-xl" onClick={applyFilter}>
                        <Search className="size-4" />
                        {t('filters.apply')}
                    </Button>
                    <Button variant="outline" size="sm" className="h-8 rounded-xl" onClick={() => window.open(billingExportUrl(filter), '_blank')}>
                        <Download className="size-4" />
                        {t('filters.export')}
                    </Button>
                </div>
            </header>

            {isLoading ? <Skeleton /> : isError ? (
                <p className="py-10 text-center text-sm text-destructive">{t('loadFailed')}</p>
            ) : records.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted-foreground">{t('details.empty')}</p>
            ) : (
                <div className="overflow-x-auto">
                    <div className="min-w-[980px]">
                        <div className="grid items-center gap-3 border-b border-border/60 py-2 text-xs font-medium text-muted-foreground" style={{ gridTemplateColumns: gridTemplate }}>
                            <span>{t('details.column.time')}</span>
                            <span>{t('details.column.model')}</span>
                            <span>{t('details.column.channel')}</span>
                            <span>{t('details.column.tokens')}</span>
                            <span className="text-right">{t('details.column.spend')}</span>
                            {owner && <span className="text-right">{t('details.column.revenue')}</span>}
                            <span className="text-right">{t('details.column.unitPrice')}</span>
                            <span className="text-right">{t('details.column.status')}</span>
                        </div>

                        {records.map((record) => (
                            <div key={record.id} className="grid items-center gap-3 border-b border-border/40 py-2.5 text-sm hover:bg-muted/40" style={{ gridTemplateColumns: gridTemplate }}>
                                <span className="whitespace-nowrap tabular-nums text-muted-foreground">
                                    {dayjs(record.created_at).format('YYYY-MM-DD HH:mm')}
                                </span>

                                <span className="min-w-0">
                                    <span className="block truncate font-medium">{record.group_model}</span>
                                    <span className="block truncate text-xs text-muted-foreground">{record.model_name}</span>
                                </span>

                                <span className="min-w-0">
                                    {record.share_code ? (
                                        <code className="block truncate rounded-lg bg-muted/60 px-1.5 py-0.5 font-mono text-xs">{record.share_code}</code>
                                    ) : (
                                        <span className="text-xs text-muted-foreground">—</span>
                                    )}
                                </span>

                                <TokenCell values={[
                                    { label: t('tokens.input'), value: record.input_token },
                                    { label: t('tokens.output'), value: record.output_token },
                                    { label: t('tokens.cacheRead'), value: record.cache_read_token },
                                    { label: t('tokens.cacheWrite'), value: record.cache_write_token },
                                    ...(record.media_units?.images ? [{ label: t('tokens.images'), value: record.media_units.images }] : []),
                                ]} />

                                <span className="text-right font-medium tabular-nums text-red-500">{formatMoney(record.user_cost)}</span>
                                {owner && (
                                    <span className="text-right font-medium tabular-nums text-green-500">{formatMoney(record.owner_revenue)}</span>
                                )}
                                <span className="text-right font-mono text-[10px] tabular-nums text-muted-foreground">
                                    <span className="block">{t('tokens.input')} {record.user_price.input}</span>
                                    <span className="block">{t('tokens.output')} {record.user_price.output}</span>
                                </span>
                                <span className="flex justify-end">
                                    <Badge variant="outline" className={cn('px-1.5 py-0 text-[10px] font-medium', record.status === 'settled' ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400')}>
                                        {t('status.' + record.status)}
                                    </Badge>
                                </span>
                            </div>
                        ))}
                    </div>
                </div>
            )}

            <Pager page={page} total={total} onChange={setPage}
                label={(current, pages, count) => t('pagination.summary', { current, pages, count })} />
        </section>
    );
}

// SpendingTrend 渲染近 30 天消费趋势表: 日期, 请求数, tokens, 消费。
function SpendingTrend() {
    const t = useTranslations('billing');
    const { data: daily, isLoading, isError } = useBillingDaily();
    const rows = daily ?? [];
    // 合计行与各列同口径累加, 便于一眼核对 30 天总量。
    const totals = useMemo(() => rows.reduce((sum, row) => ({
        requests: sum.requests + row.request_count,
        tokens: sum.tokens + row.input_token + row.output_token + row.cache_read_token + row.cache_write_token,
        spend: sum.spend + row.user_cost,
    }), { requests: 0, tokens: 0, spend: 0 }), [rows]);

    return (
        <article className="rounded-3xl bg-card text-card-foreground border-border border pt-3 pb-3 px-4">
            <header className="mb-2 flex items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-base font-semibold">
                    <Database className="size-4 text-muted-foreground" />
                    {t('trend.title')}
                </h2>
                <span className="text-xs text-muted-foreground">{t('trend.range')}</span>
            </header>

            {isLoading ? <Skeleton /> : isError ? (
                <p className="py-10 text-center text-sm text-destructive">{t('loadFailed')}</p>
            ) : rows.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted-foreground">{t('trend.empty')}</p>
            ) : (
                <div className="overflow-x-auto">
                    <div className="min-w-[640px]">
                        <div className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px] gap-3 border-b border-border/60 py-2 text-xs font-medium text-muted-foreground">
                            <span>{t('trend.column.date')}</span>
                            <span className="text-right">{t('trend.column.requests')}</span>
                            <span>{t('trend.column.tokens')}</span>
                            <span className="text-right">{t('trend.column.spend')}</span>
                        </div>

                        <div className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px] gap-3 border-b border-border/60 bg-muted/30 py-2 text-sm font-medium">
                            <span>{t('trend.summary')}</span>
                            <span className="text-right tabular-nums">{formatCount(totals.requests)}</span>
                            <span className="text-xs text-muted-foreground">{formatCount(totals.tokens)}</span>
                            <span className="text-right tabular-nums">{formatMoney(totals.spend)}</span>
                        </div>

                        {rows.map((row) => (
                            <div key={row.date} className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px] gap-3 py-2 text-sm hover:bg-muted/40">
                                <span className="tabular-nums text-muted-foreground">{dayLabel(row.date)}</span>
                                <span className="text-right tabular-nums">{formatCount(row.request_count)}</span>
                                <DailyTokenCell row={row} />
                                <span className="text-right tabular-nums">{formatMoney(row.user_cost)}</span>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </article>
    );
}

// RevenueTrend 渲染近 30 天收入趋势表: 日期, 请求数, tokens, 供货收入, 平台佣金;
// 独立成组件以便按 owner 条件挂载。
function RevenueTrend() {
    const t = useTranslations('billing');
    const { data: revenue, isLoading, isError } = useRevenueDaily();
    const rows = revenue ?? [];
    const totals = useMemo(() => rows.reduce((sum, row) => ({
        requests: sum.requests + row.request_count,
        tokens: sum.tokens + row.input_token + row.output_token + row.cache_read_token + row.cache_write_token,
        revenue: sum.revenue + row.owner_revenue,
        commission: sum.commission + row.platform_revenue,
    }), { requests: 0, tokens: 0, revenue: 0, commission: 0 }), [rows]);

    return (
        <article className="rounded-3xl bg-card text-card-foreground border-border border pt-3 pb-3 px-4">
            <header className="mb-2 flex items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-base font-semibold">
                    <ArrowUpFromLine className="size-4 text-muted-foreground" />
                    {t('revenue.title')}
                </h2>
                <span className="text-xs text-muted-foreground">{t('revenue.hint')}</span>
            </header>

            {isLoading ? <Skeleton /> : isError ? (
                <p className="py-10 text-center text-sm text-destructive">{t('loadFailed')}</p>
            ) : rows.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted-foreground">{t('revenue.empty')}</p>
            ) : (
                <div className="overflow-x-auto">
                    <div className="min-w-[680px]">
                        <div className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px_110px] gap-3 border-b border-border/60 py-2 text-xs font-medium text-muted-foreground">
                            <span>{t('trend.column.date')}</span>
                            <span className="text-right">{t('trend.column.requests')}</span>
                            <span>{t('trend.column.tokens')}</span>
                            <span className="text-right">{t('revenue.column.ownerRevenue')}</span>
                            <span className="text-right">{t('revenue.column.commission')}</span>
                        </div>

                        <div className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px_110px] gap-3 border-b border-border/60 bg-muted/30 py-2 text-sm font-medium">
                            <span>{t('revenue.summary')}</span>
                            <span className="text-right tabular-nums">{formatCount(totals.requests)}</span>
                            <span className="text-xs text-muted-foreground">{formatCount(totals.tokens)}</span>
                            <span className="text-right tabular-nums">{formatMoney(totals.revenue)}</span>
                            <span className="text-right tabular-nums">{formatMoney(totals.commission)}</span>
                        </div>

                        {rows.map((row) => (
                            <div key={row.date} className="grid grid-cols-[90px_1fr_minmax(150px,1.2fr)_110px_110px] gap-3 py-2 text-sm hover:bg-muted/40">
                                <span className="tabular-nums text-muted-foreground">{dayLabel(row.date)}</span>
                                <span className="text-right tabular-nums">{formatCount(row.request_count)}</span>
                                <DailyTokenCell row={row} />
                                <span className="text-right tabular-nums text-green-500">{formatMoney(row.owner_revenue)}</span>
                                <span className="text-right tabular-nums text-muted-foreground">{formatMoney(row.platform_revenue)}</span>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </article>
    );
}

// LEDGER_KINDS 余额流水的类型选项, 与后端 model.LedgerKind* 一致。
const LEDGER_KINDS = ['reserve', 'settle', 'release', 'adjust'] as const;

// formatDelta 有符号金额: 正数带 +, 负数保留 -, 零显示 0。
function formatDelta(value: number): string {
    const formatted = formatMoney(Math.abs(value));
    if (value > 0) return '+' + formatted;
    if (value < 0) return '-' + formatted;
    return '0';
}

// BalanceLedgerTable 渲染余额流水: 每一笔冻结/解冻/扣费/调账逐条留痕, 是余额变动的唯一解释口径。
function BalanceLedgerTable() {
    const t = useTranslations('billing');
    const [page, setPage] = useState(1);
    const [kind, setKind] = useState('');
    const { data, isLoading, isError } = useBalanceLedger(kind, PAGE_SIZE, (page - 1) * PAGE_SIZE);
    const rows = data?.items ?? [];
    const total = data?.total ?? 0;
    const gridTemplate = '150px 96px 110px 110px 110px 110px minmax(120px,1fr)';

    return (
        <section className="rounded-3xl bg-card text-card-foreground border-border border pt-3 pb-3 px-4">
            <header className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-base font-semibold">
                    <Receipt className="size-4 text-muted-foreground" />
                    {t('ledger.title')}
                </h2>
                <div className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground">{t('ledger.hint')}</span>
                    <Select value={kind} onValueChange={(value) => { setKind(value); setPage(1); }}>
                        <SelectTrigger className="h-8 w-28 rounded-xl"><SelectValue placeholder={t('ledger.kindAll')} /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value="">{t('ledger.kindAll')}</SelectItem>
                            {LEDGER_KINDS.map((item) => (
                                <SelectItem key={item} value={item}>{t('ledger.kind.' + item)}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
            </header>

            {isLoading ? <Skeleton /> : isError ? (
                <p className="py-10 text-center text-sm text-destructive">{t('loadFailed')}</p>
            ) : rows.length === 0 ? (
                <p className="py-10 text-center text-sm text-muted-foreground">{t('ledger.empty')}</p>
            ) : (
                <div className="overflow-x-auto">
                    <div className="min-w-[900px]">
                        <div className="grid items-center gap-3 border-b border-border/60 py-2 text-xs font-medium text-muted-foreground" style={{ gridTemplateColumns: gridTemplate }}>
                            <span>{t('ledger.column.time')}</span>
                            <span>{t('ledger.column.kind')}</span>
                            <span className="text-right">{t('ledger.column.frozen')}</span>
                            <span className="text-right">{t('ledger.column.amount')}</span>
                            <span className="text-right">{t('ledger.column.cost')}</span>
                            <span className="text-right">{t('ledger.column.balance')}</span>
                            <span>{t('ledger.column.note')}</span>
                        </div>

                        {rows.map((row) => (
                            <div key={row.id} className="grid items-center gap-3 border-b border-border/40 py-2.5 text-sm hover:bg-muted/40" style={{ gridTemplateColumns: gridTemplate }}>
                                <span className="whitespace-nowrap tabular-nums text-muted-foreground">{dayjs(row.created_at).format('YYYY-MM-DD HH:mm')}</span>
                                <span>
                                    <Badge variant="outline" className={cn('px-1.5 py-0 text-[10px] font-medium',
                                        row.kind === 'settle' && 'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400',
                                        row.kind === 'reserve' && 'border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-400',
                                        row.kind === 'release' && 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
                                        row.kind === 'adjust' && 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400')}>
                                        {t('ledger.kind.' + row.kind)}
                                    </Badge>
                                </span>
                                <span className="text-right tabular-nums text-muted-foreground">{row.frozen ? formatDelta(row.frozen) : '—'}</span>
                                <span className={cn('text-right font-medium tabular-nums', row.amount > 0 ? 'text-emerald-600 dark:text-emerald-400' : row.amount < 0 ? 'text-red-500' : 'text-muted-foreground')}>{formatDelta(row.amount)}</span>
                                <span className="text-right tabular-nums text-muted-foreground">{row.cost ? formatMoney(row.cost) : '—'}</span>
                                <span className="text-right tabular-nums">{row.balance_after ? formatMoney(row.balance_after) : '—'}</span>
                                <span className="min-w-0 truncate text-xs text-muted-foreground">
                                    {row.model_name ? row.model_name + ' · ' : ''}
                                    {row.actor_name ? row.actor_name + ' · ' : ''}
                                    {row.note || (row.request_id ? '#' + row.request_id : '—')}
                                </span>
                            </div>
                        ))}
                    </div>
                </div>
            )}

            <Pager page={page} total={total} onChange={setPage}
                label={(current, pages, count) => t('pagination.summary', { current, pages, count })} />
        </section>
    );
}

// Trends 并列消费趋势与收入趋势; 收入趋势仅渠道商/管理员挂载。
function Trends({ owner }: { owner: boolean }) {
    return (
        <section className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <SpendingTrend />
            {owner && <RevenueTrend />}
        </section>
    );
}

// Billing 渲染计费页面: 账户概览、消费明细、消费趋势, 收入相关区块按角色开放。
export default function Billing() {
    // 角色取自登录态（API Key 登录时为 null）; 不是渠道商或管理员就按普通用户裁剪收入区块。
    const { role } = useAuth();
    const owner = role === 'reseller' || role === 'admin';

    return (
        <div className="flex h-full min-h-0 flex-col">
            <div className="mx-auto flex w-full max-w-6xl min-h-0 flex-1 flex-col gap-6 overflow-y-auto overscroll-contain px-4 py-6 pb-24">
                <Overview owner={owner} />
                <SpendingTable owner={owner} />
                <BalanceLedgerTable />
                <Trends owner={owner} />
            </div>
        </div>
    );
}