import { useState, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { BarChart3, Calendar, Loader2, RefreshCw, TrendingUp } from 'lucide-react';
import { Bar, BarChart, CartesianGrid, Cell, Legend, Line, LineChart, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useUserStatsModels, useUserStatsOverview, useUserStatsTrend } from '@/api/user-stats';
import { formatAmount } from '@/lib/format';
import { getDateRange, PRESETS, type Preset } from './dates';

// PanelQuery 区块只依赖查询的这三个字段: 加载/失败/重试彼此独立, 谁慢谁转圈, 谁错谁重试。
type PanelQuery = {
    isPending: boolean;
    isError: boolean;
    refetch: () => unknown;
};

// PanelBody 给每个区块套统一的「加载中 / 失败+重试 / 空态 / 内容」四态外壳。
// 失败必须显式报错并给重试入口: 用零值兜底会把「没取到」伪装成「真没有」,
// 用户会把假零当真实统计看。空态同理 —— 空列表是数据, 报错不是。
function PanelBody({ query, empty, children }: { query: PanelQuery; empty?: boolean; children: ReactNode }) {
    const t = useTranslations('analytics');
    if (query.isError) {
        return (
            <div role="alert" className="flex h-40 flex-col items-center justify-center gap-2 text-muted-foreground">
                <p>{t('error')}</p>
                <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void query.refetch()}>
                    <RefreshCw className="size-4" aria-hidden="true" />
                    {t('retry')}
                </Button>
            </div>
        );
    }
    if (query.isPending) {
        return (
            <div className="flex h-40 items-center justify-center gap-2 text-muted-foreground">
                <Loader2 className="size-5 animate-spin" aria-hidden="true" />
                {t('loading')}
            </div>
        );
    }
    if (empty) {
        return <div className="flex h-40 items-center justify-center text-muted-foreground">{t('empty')}</div>;
    }
    return <>{children}</>;
}

const COLORS = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4', '#84cc16', '#f97316', '#6366f1', '#64748b'];

// chartTooltipStyle 图表浮层跟随卡片配色。
const chartTooltipStyle = {
    backgroundColor: 'hsl(var(--card))',
    border: '1px solid hsl(var(--border))',
    borderRadius: '8px',
};

// AnalyticsPage 用户调用统计分析页面。
// 概览/趋势/模型三个区块各自持有查询与四态: 一个请求慢或挂了不拖住整页,
// 更不会把失败渲染成全零的「正常统计」。
export function AnalyticsPage() {
    const t = useTranslations('analytics');
    const [preset, setPreset] = useState<Preset>('month');
    const [granularity, setGranularity] = useState<'day' | 'week' | 'month'>('day');

    const dateRange = getDateRange(preset);
    const overview = useUserStatsOverview(dateRange);
    const trend = useUserStatsTrend({ ...dateRange, granularity });
    const models = useUserStatsModels(dateRange);

    const trendData = trend.data ?? [];
    const modelRows = models.data ?? [];

    // 模型分布前 10 + 其他聚合
    const topModels = modelRows.slice(0, 10);
    const otherModels = modelRows.slice(10);
    const otherCost = otherModels.reduce((sum, m) => sum + m.cost, 0);
    const otherRequests = otherModels.reduce((sum, m) => sum + m.requests, 0);
    const modelData = topModels.map((m) => ({ name: m.model_name, value: m.cost }));
    if (otherCost > 0) {
        modelData.push({ name: t('model.other'), value: otherCost });
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold">
                    <BarChart3 className="size-5" />
                    {t('title')}
                </h2>
                <div className="flex flex-wrap items-center gap-2">
                    <Select value={preset} onValueChange={(v) => setPreset(v as Preset)}>
                        <SelectTrigger className="w-32 rounded-xl">
                            <Calendar className="mr-1 size-4" />
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {PRESETS.map((p) => (
                                <SelectItem key={p} value={p}>{t(`range.${p}`)}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Select value={granularity} onValueChange={(v) => setGranularity(v as typeof granularity)}>
                        <SelectTrigger className="w-28 rounded-xl"><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value="day">{t('granularity.day')}</SelectItem>
                            <SelectItem value="week">{t('granularity.week')}</SelectItem>
                            <SelectItem value="month">{t('granularity.month')}</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
            </div>

            {/* 概览卡片: 数值是查询给的真值 —— 零就是零; 加载/失败由 PanelBody 显式区分 */}
            <PanelBody query={overview}>
                {overview.data && (
                    <div className="grid shrink-0 grid-cols-2 gap-2 md:grid-cols-4">
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.requests')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{overview.data.total_requests.toLocaleString()}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.successRate')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{overview.data.success_rate.toFixed(1)}%</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.cost')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{formatAmount(overview.data.total_cost)}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.tokens')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{(overview.data.total_tokens / 1000).toFixed(1)}K</div>
                        </div>
                    </div>
                )}
            </PanelBody>

            {/* 趋势图 */}
            <div className="shrink-0 rounded-3xl border border-border bg-card p-4">
                <h3 className="mb-3 flex items-center gap-2 text-sm font-medium">
                    <TrendingUp className="size-4" />
                    {t('chart.trend')}
                </h3>
                <PanelBody query={trend} empty={trendData.length === 0}>
                    <ResponsiveContainer width="100%" height={240}>
                        <LineChart data={trendData}>
                            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                            <XAxis dataKey="date" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            <YAxis yAxisId="left" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            <Tooltip contentStyle={chartTooltipStyle} />
                            <Legend wrapperStyle={{ fontSize: 12 }} />
                            <Line yAxisId="left" type="monotone" dataKey="requests" stroke="#3b82f6" name={t('chart.requests')} strokeWidth={2} dot={{ r: 3 }} />
                            <Line yAxisId="right" type="monotone" dataKey="cost" stroke="#10b981" name={t('chart.cost')} strokeWidth={2} dot={{ r: 3 }} />
                        </LineChart>
                    </ResponsiveContainer>
                </PanelBody>
            </div>

            {/* 模型分布 */}
            <PanelBody query={models} empty={modelRows.length === 0}>
                <div className="grid shrink-0 gap-2 md:grid-cols-2">
                    <div className="rounded-3xl border border-border bg-card p-4">
                        <h3 className="mb-3 text-sm font-medium">{t('chart.modelCost')}</h3>
                        <ResponsiveContainer width="100%" height={240}>
                            <PieChart>
                                <Pie data={modelData} dataKey="value" nameKey="name" cx="50%" cy="50%" outerRadius={80} label>
                                    {modelData.map((_, index) => (
                                        <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} />
                                    ))}
                                </Pie>
                                <Tooltip
                                    contentStyle={chartTooltipStyle}
                                    formatter={(value) => value != null ? formatAmount(value as number) : ''}
                                />
                            </PieChart>
                        </ResponsiveContainer>
                    </div>

                    <div className="rounded-3xl border border-border bg-card p-4">
                        <h3 className="mb-3 text-sm font-medium">{t('chart.modelRequests')}</h3>
                        <ResponsiveContainer width="100%" height={240}>
                            <BarChart data={topModels.concat(otherModels.length > 0 ? [{ model_name: t('model.other'), requests: otherRequests, cost: 0, tokens: 0 }] : [])}>
                                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                                <XAxis dataKey="model_name" tick={{ fontSize: 10 }} angle={-45} textAnchor="end" height={80} stroke="hsl(var(--muted-foreground))" />
                                <YAxis tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                                <Tooltip contentStyle={chartTooltipStyle} />
                                <Bar dataKey="requests" fill="#3b82f6" />
                            </BarChart>
                        </ResponsiveContainer>
                    </div>
                </div>
            </PanelBody>
        </div>
    );
}
