import { useMemo, useState } from 'react';
import { AlertTriangle, Loader2, RefreshCw, ShieldAlert } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { ApiError } from '@/api/client';
import { useChannelProfit, type ChannelProfit } from '@/api/channel';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { formatAmount } from '@/lib/format';
import { defaultProfitRange, profitPresetRange, profitRangeValid } from './profitRange';

// money 展示金额: 后端保留原始精度, 这里按现有 formatAmount 口径最多 4 位小数, 不强行截到分。
function money(value: number): string {
    return '$' + formatAmount(value);
}

// margin 毛利率: 后端已按 0~100 给出, 前端只补百分号与两位小数。
function margin(value: number): string {
    return `${Number(value || 0).toFixed(2)}%`;
}

type ProfitMetric = { key: string; label: string; value: string; hint?: string; tone?: string };

// ProfitMetrics 把汇总拆成卡片; 键与文案分开, 便于三语渲染同一组指标。
function profitMetrics(data: ChannelProfit, t: (key: string) => string): ProfitMetric[] {
    const profit = data.totals.platform_revenue;
    return [
        { key: 'revenue', label: t('metric.revenue'), value: money(data.totals.user_cost), hint: t('metric.revenueHint') },
        { key: 'cost', label: t('metric.cost'), value: money(data.totals.owner_revenue), hint: t('metric.costHint') },
        {
            key: 'profit',
            label: t('metric.profit'),
            value: money(profit),
            hint: t('metric.profitHint'),
            // 亏损是经营异常, 用颜色标出来, 不让负号淹没在一排数字里。
            tone: profit < 0 ? 'text-destructive' : 'text-emerald-500',
        },
        { key: 'margin', label: t('metric.margin'), value: margin(data.totals.margin_rate), hint: t('metric.marginHint') },
        { key: 'requests', label: t('metric.requests'), value: Number(data.totals.requests || 0).toLocaleString() },
    ];
}

// ChannelProfitPanel 渠道利润分析: 管理员在渠道详情里按日期范围看收入、成本、毛利与毛利率。
// 数据只来自已结算的计费记录, 故口径与渠道统计(转发成功率/延迟)分开, 不混展示。
export function ChannelProfitPanel({ channelId }: { channelId: number }) {
    const t = useTranslations('channel.profit');
    const [range, setRange] = useState(() => defaultProfitRange());
    const valid = profitRangeValid(range.startDate, range.endDate);
    const query = useChannelProfit(channelId, range.startDate, range.endDate, valid);

    // 无权限与"加载失败"必须分开: 403 是角色问题, 给重试按钮只会让人反复点同一个必定失败的请求。
    const forbidden = query.error instanceof ApiError && query.error.status === 403;
    const metrics = useMemo(() => (query.data ? profitMetrics(query.data, t) : []), [query.data, t]);

    const applyPreset = (days: number) => setRange(profitPresetRange(days));

    return (
        <section data-share-exclude className="rounded-2xl border bg-card p-3">
            <p className="mb-2 text-[11px] text-muted-foreground">{t('fxNote')}</p>
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t('title')}</h4>
                <div className="flex flex-wrap items-center gap-1.5" data-share-exclude>
                    {[7, 30, 90].map((days) => (
                        <button
                            key={days}
                            type="button"
                            onClick={() => applyPreset(days)}
                            aria-pressed={range.endDate === profitPresetRange(days).endDate && range.startDate === profitPresetRange(days).startDate}
                            className="min-h-7 rounded-lg px-2 text-xs text-muted-foreground/70 transition-colors hover:text-foreground aria-pressed:bg-muted aria-pressed:font-medium aria-pressed:text-foreground"
                        >
                            {t('preset', { days })}
                        </button>
                    ))}
                    <Input
                        type="date"
                        className="h-8 w-36 rounded-xl"
                        aria-label={t('startDate')}
                        value={range.startDate}
                        max={range.endDate || undefined}
                        onChange={(event) => setRange((current) => ({ ...current, startDate: event.target.value }))}
                    />
                    <Input
                        type="date"
                        className="h-8 w-36 rounded-xl"
                        aria-label={t('endDate')}
                        value={range.endDate}
                        min={range.startDate || undefined}
                        onChange={(event) => setRange((current) => ({ ...current, endDate: event.target.value }))}
                    />
                </div>
            </div>

            {!valid ? (
                <div role="alert" className="flex items-center justify-center gap-2 py-6 text-xs text-destructive">
                    <AlertTriangle className="size-4" aria-hidden="true" />
                    {t('rangeInvalid')}
                </div>
            ) : forbidden ? (
                <div role="alert" className="flex items-center justify-center gap-2 py-6 text-xs text-muted-foreground">
                    <ShieldAlert className="size-4" aria-hidden="true" />
                    {t('forbidden')}
                </div>
            ) : query.isError ? (
                <div role="alert" className="flex flex-col items-center justify-center gap-2 py-6 text-muted-foreground">
                    <p className="text-xs">{t('error')}</p>
                    <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void query.refetch()}>
                        <RefreshCw className="size-4" aria-hidden="true" />
                        {t('retry')}
                    </Button>
                </div>
            ) : query.isPending ? (
                <div className="flex items-center justify-center gap-2 py-6 text-xs text-muted-foreground">
                    <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                    {t('loading')}
                </div>
            ) : query.data && query.data.daily.length === 0 && query.data.totals.requests === 0 ? (
                <div className="flex items-center justify-center py-6 text-xs text-muted-foreground">{t('empty')}</div>
            ) : query.data ? (
                <div className="space-y-2">
                    <dl className="grid grid-cols-2 gap-2 @md/stats:grid-cols-5">
                        {metrics.map((metric) => (
                            <div key={metric.key} className="rounded-xl border border-border/70 bg-background/80 p-2">
                                <dt className="truncate text-[11px] text-muted-foreground">{metric.label}</dt>
                                <dd className={`mt-1 truncate text-right text-sm font-bold tabular-nums ${metric.tone ?? 'text-card-foreground'}`}>
                                    {metric.value}
                                </dd>
                                {metric.hint && <dd className="mt-0.5 truncate text-right text-[10px] text-muted-foreground/70">{metric.hint}</dd>}
                            </div>
                        ))}
                    </dl>
                    <div className="max-h-56 overflow-y-auto rounded-xl border border-border/50">
                        <table className="w-full text-xs tabular-nums">
                            <thead className="sticky top-0 bg-muted/70 text-muted-foreground">
                                <tr>
                                    <th className="px-2 py-1.5 text-left font-medium">{t('table.date')}</th>
                                    <th className="px-2 py-1.5 text-right font-medium">{t('table.requests')}</th>
                                    <th className="px-2 py-1.5 text-right font-medium">{t('table.revenue')}</th>
                                    <th className="px-2 py-1.5 text-right font-medium">{t('table.cost')}</th>
                                    <th className="px-2 py-1.5 text-right font-medium">{t('table.profit')}</th>
                                    <th className="px-2 py-1.5 text-right font-medium">{t('table.margin')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {query.data.daily.map((day) => (
                                    <tr key={day.date} className="border-t border-border/40">
                                        <td className="px-2 py-1 text-left text-muted-foreground">{day.date}</td>
                                        <td className="px-2 py-1 text-right">{Number(day.requests || 0).toLocaleString()}</td>
                                        <td className="px-2 py-1 text-right">{money(day.user_cost)}</td>
                                        <td className="px-2 py-1 text-right">{money(day.owner_revenue)}</td>
                                        <td className={`px-2 py-1 text-right ${day.platform_revenue < 0 ? 'text-destructive' : ''}`}>{money(day.platform_revenue)}</td>
                                        <td className="px-2 py-1 text-right">{margin(day.margin_rate)}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                </div>
            ) : null}
        </section>
    );
}
