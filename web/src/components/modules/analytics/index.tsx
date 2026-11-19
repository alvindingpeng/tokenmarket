import { useState, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { BarChart3, Calendar, Loader2, RefreshCw, Timer, TrendingUp, TriangleAlert } from 'lucide-react';
import { Bar, BarChart, CartesianGrid, Cell, Legend, Line, LineChart, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    useUserStatsModels,
    useUserStatsOverview,
    useUserStatsTrend,
    useUserStatsLatency,
} from '@/api/user-stats';
import { formatAmount, formatTokenCount } from '@/lib/format';
import { getDateRange, PRESETS, type Preset } from './dates';
// 展示口径的纯函数单独成文件: 组件文件只导出组件才能保住 Fast Refresh,
// 而这些规则正是最该被单测钉死的部分。
import { buildTrendChartData, hasSuccessSamples, isCoverageLimited, summarizeSuccess } from './success';

// PanelQuery 区块只依赖查询的这三个字段: 加载/失败/重试彼此独立, 谁慢谁转圈, 谁错谁重试。
type PanelQuery = {
    isPending: boolean;
    isError: boolean;
    refetch: () => unknown;
};

// PanelBody 给每个区块套统一的「加载中 / 失败+重试 / 空态 / 内容」四态外壳。
// 失败必须显式报错并给重试入口: 用零值兜底会把「没取到」伪装成「真没有」,
// 用户会把假零当真实统计看。空态同理 —— 空列表是数据, 报错不是。
function PanelBody({ query, empty, emptyText, children }: { query: PanelQuery; empty?: boolean; emptyText?: string; children: ReactNode }) {
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
        // emptyText 用于语义不同的空态(例如"没有延迟样本"不等于"该区间没有调用"),
        // 复用同一句会把两种完全不同的情况说成同一件事。
        return <div className="flex h-40 items-center justify-center text-muted-foreground">{emptyText ?? t('empty')}</div>;
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

// formatLatency 毫秒转人读格式; 无样本时显示 "-" 而不是 0ms,
// 因为 0ms 会被读成"极快", 而实际是"没有数据"。
function formatLatency(ms: number): string {
    if (!Number.isFinite(ms) || ms <= 0) return '-';
    if (ms < 1000) return ms.toFixed(0) + ' ms';
    return (ms / 1000).toFixed(2) + ' s';
}

// AnalyticsPage 用户调用统计分析页面。
// 概览/趋势/模型三个区块各自持有查询与四态: 一个请求慢或挂了不拖住整页,
// 更不会把失败渲染成全零的「正常统计」。
export function AnalyticsPage() {
    const t = useTranslations('analytics');
    // 默认"今天 + 按小时": 用户打开统计页想看的是"刚才到现在发生了什么", 30 天按天是次选动作。
    const [preset, setPreset] = useState<Preset>('today');
    const [granularity, setGranularity] = useState<'hour' | 'day' | 'week' | 'month'>('hour');
    // 今天=小时, 近7/30天=按天: 周与月桶在单日区间只会产出空桶, 切范围时把粒度带到位, 省一次"图是空的"困惑。
    const effectiveGranularity = preset === 'today' ? 'hour' : granularity === 'hour' ? 'day' : granularity;

    const dateRange = getDateRange(preset);
    const overview = useUserStatsOverview(dateRange);
    const trend = useUserStatsTrend({ ...dateRange, granularity: effectiveGranularity });
    const models = useUserStatsModels(dateRange);
    const latency = useUserStatsLatency(dateRange);

    const trendData = trend.data ?? [];
    const modelRows = models.data ?? [];
    const trendChartData = buildTrendChartData(trendData);
    const trendHasSuccessSamples = hasSuccessSamples(trendData);

    // 成功率与样本量成对出现在界面里: 只有百分比没有样本量, 用户无法判断这个数字值不值得信。
    const success = overview.data ? summarizeSuccess(overview.data) : null;
    // 调用日志留存 7 天时选"最近 30 天": 覆盖起点会晚于查询起点, 必须把这段差异讲出来。
    const coverageLimited = overview.data
        ? isCoverageLimited(overview.data.request_coverage_start, dateRange.start_date)
        : false;

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
        <div data-testid="analytics-scroll" className="flex h-full min-h-0 flex-col gap-3 overflow-y-auto overflow-x-hidden overscroll-contain pb-24 pr-1 md:pb-4">
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
                    {/* 今天按小时是既定组合, 粒度选择仅在多日范围有意义, 遂在此禁用整组切换。 */}
                    <Select value={effectiveGranularity} onValueChange={(v) => setGranularity(v as typeof granularity)} disabled={preset === 'today'}>
                        <SelectTrigger className="w-28 rounded-xl"><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value="hour">{t('granularity.hour')}</SelectItem>
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
                            {/* 这一格是计费口径(只含产生用量的调用), 与右侧成功率的请求口径天然不相等。
                                不写清楚的话, 用户会拿它去除成功率样本量, 得出"少了三分之一"的错误结论。 */}
                            <div className="mt-0.5 text-xs text-muted-foreground" data-testid="requests-scope">
                                {t('card.requestsHint')}
                            </div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.successRate')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums" data-testid="success-rate">
                                {success?.value}
                            </div>
                            {/* 小字解释这个百分比是怎么来的: 分子分母与成功率同口径, 数字之间不会打架。 */}
                            {success && (
                                <div className="mt-0.5 text-xs text-muted-foreground" data-testid="success-sample">
                                    {success.attempts > 0
                                        ? t('card.successSample', {
                                            success: overview.data.request_success.toLocaleString(),
                                            failed: overview.data.request_failed.toLocaleString(),
                                        })
                                        : t('card.successNoSamples', { days: overview.data.request_retention_days })}
                                    {overview.data.request_canceled > 0 && (
                                        <span className="ml-1">
                                            {t('card.successCanceled', { canceled: overview.data.request_canceled.toLocaleString() })}
                                        </span>
                                    )}
                                </div>
                            )}
                            {/* 覆盖不全时说清楚"只覆盖到哪天、为什么": 绝不把末尾几天的样本包装成整个区间。
                                isCoverageLimited 已排掉"空串(区间内没日志)"和"区间本就完整"两种情况。 */}
                            {coverageLimited && (
                                <div className="mt-0.5 flex items-start gap-1 text-xs text-amber-600 dark:text-amber-500" data-testid="success-coverage">
                                    <TriangleAlert className="mt-px size-3 shrink-0" aria-hidden="true" />
                                    <span>
                                        {t('card.successCoverage', {
                                            start: overview.data.request_coverage_start,
                                            days: overview.data.request_retention_days,
                                        })}
                                    </span>
                                </div>
                            )}
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.cost')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{formatAmount(overview.data.total_cost)}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.tokens')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{formatTokenCount(overview.data.total_tokens)}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.cacheHitRate')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums" data-testid="cache-hit-rate">
                                {overview.data.cache_hit_rate.toFixed(1)}%
                            </div>
                        </div>
                    </div>
                )}
            </PanelBody>

            {/* 词元构成: 四项之和等于总数, 让用户看清花费在输入、输出还是缓存上。
                缓存命中的读取通常远低于按量输入价, 不拆开就完全看不见省在哪。 */}
            {overview.data && (
                <div className="shrink-0 rounded-3xl border border-border bg-card p-4" data-testid="token-breakdown">
                    <h3 className="mb-3 text-sm font-medium">{t('tokenBreakdown.title')}</h3>
                    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                        <div>
                            <div className="text-xs text-muted-foreground">{t('tokenBreakdown.input')}</div>
                            <div className="mt-1 text-lg font-semibold tabular-nums">{formatTokenCount(overview.data.input_tokens)}</div>
                        </div>
                        <div>
                            <div className="text-xs text-muted-foreground">{t('tokenBreakdown.output')}</div>
                            <div className="mt-1 text-lg font-semibold tabular-nums">{formatTokenCount(overview.data.output_tokens)}</div>
                        </div>
                        <div>
                            <div className="text-xs text-muted-foreground">{t('tokenBreakdown.cacheRead')}</div>
                            <div className="mt-1 text-lg font-semibold tabular-nums">{formatTokenCount(overview.data.cache_read_tokens)}</div>
                        </div>
                        <div>
                            <div className="text-xs text-muted-foreground">{t('tokenBreakdown.cacheWrite')}</div>
                            <div className="mt-1 text-lg font-semibold tabular-nums">{formatTokenCount(overview.data.cache_write_tokens)}</div>
                        </div>
                    </div>
                </div>
            )}

            {/* 趋势图 */}
            <div className="shrink-0 rounded-3xl border border-border bg-card p-4" data-testid="trend-chart">
                <h3 className="mb-3 flex items-center gap-2 text-sm font-medium">
                    <TrendingUp className="size-4" />
                    {t('chart.trend')}
                </h3>
                <PanelBody query={trend} empty={trendData.length === 0}>
                    <ResponsiveContainer width="100%" height={240}>
                        <LineChart data={trendChartData}>
                            <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                            <XAxis dataKey="date" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))"
                            /* 小时桶标签太长(YYYY-MM-DD HH:00), 刻度只留 HH:00; 悬浮提示仍走全量标签。 */
                            tickFormatter={(value: string) => (value.length === 16 ? value.slice(11) : value)} />
                            <YAxis yAxisId="left" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            {/* 成功率是百分比, 必须有自己的 0-100 右轴; 和费用共轴会把它压成贴底的一条直线。 */}
                            {trendHasSuccessSamples && (
                                <YAxis yAxisId="rate" orientation="right" domain={[0, 100]} tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                            )}
                            <Tooltip contentStyle={chartTooltipStyle} />
                            <Legend wrapperStyle={{ fontSize: 12 }} />
                            <Line yAxisId="left" type="monotone" dataKey="requests" stroke="#3b82f6" name={t('chart.requests')} strokeWidth={2} dot={{ r: 3 }} />
                            <Line yAxisId="right" type="monotone" dataKey="cost" stroke="#10b981" name={t('chart.cost')} strokeWidth={2} dot={{ r: 3 }} />
                            {/* 虚线 + 独立右轴: 与调用量/费用区分开, 不让人误读成同一条曲线。
                                connectNulls={false}: 没有日志样本的桶在数据里是 null, 折线在此断开,
                                而不是从上一个点坠到 0 装成"成功率归零"。 */}
                            {trendHasSuccessSamples && (
                                <Line
                                    yAxisId="rate"
                                    type="monotone"
                                    dataKey="success_rate"
                                    stroke="#f59e0b"
                                    strokeDasharray="4 4"
                                    name={t('chart.successRate')}
                                    strokeWidth={2}
                                    dot={{ r: 3 }}
                                    connectNulls={false}
                                />
                            )}
                        </LineChart>
                    </ResponsiveContainer>
                </PanelBody>
            </div>

            {/* 延迟指标: 数据源是调用日志(按留存期滚动删除), 与计费口径的时间覆盖不同。
                必须显式标出真实覆盖起点, 否则用户会把 7 天的延迟当成 30 天的结论。 */}
            <div className="shrink-0 rounded-3xl border border-border bg-card p-4" data-testid="latency-panel">
                <h3 className="mb-1 flex items-center gap-2 text-sm font-medium">
                    <Timer className="size-4" aria-hidden="true" />
                    {t('latency.title')}
                </h3>
                <p className="mb-3 text-xs text-muted-foreground">
                    {latency.data && latency.data.coverage_start !== ''
                        ? t('latency.coverage', { start: latency.data.coverage_start, days: latency.data.retention_days })
                        : t('latency.coverageHint', { days: latency.data?.retention_days ?? 7 })}
                </p>
                <PanelBody query={latency} empty={(latency.data?.sample_count ?? 0) === 0} emptyText={t('latency.empty')}>
                    {latency.data && (
                        <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                            <div>
                                <div className="text-xs text-muted-foreground">{t('latency.avgFirstToken')}</div>
                                <div className="mt-1 text-lg font-semibold tabular-nums">{formatLatency(latency.data.avg_first_token_ms)}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground">{t('latency.p95FirstToken')}</div>
                                <div className="mt-1 text-lg font-semibold tabular-nums">{formatLatency(latency.data.p95_first_token_ms)}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground">{t('latency.avgDuration')}</div>
                                <div className="mt-1 text-lg font-semibold tabular-nums">{formatLatency(latency.data.avg_duration_ms)}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground">{t('latency.upstreamTtfb')}</div>
                                <div className="mt-1 text-lg font-semibold tabular-nums">{formatLatency(latency.data.avg_upstream_ttfb_ms)}</div>
                            </div>
                        </div>
                    )}
                </PanelBody>
                <p className="mt-2 text-xs text-muted-foreground">{t('latency.hint')}</p>
            </div>

            {/* 模型分布 */}
            <PanelBody query={models} empty={modelRows.length === 0}>
                <div className="grid shrink-0 gap-2 md:grid-cols-2">
                    <div className="rounded-3xl border border-border bg-card p-4">
                        <h3 className="mb-3 text-sm font-medium">{t('chart.modelCost')}</h3>
                        <ResponsiveContainer width="100%" height={240}>
                            <PieChart>
                                <Pie data={modelData} dataKey="value" nameKey="name" cx="50%" cy="50%" outerRadius={80}
                                    /* 金额统一 2 位小数: 默认标签印原始浮点值, 会露出 0.30000000000000004 这类尾差。 */
                                    label={({ value }) => (typeof value === 'number' ? value.toFixed(2) : '')}>
                                    {modelData.map((_, index) => (
                                        <Cell key={`cell-${index}`} fill={COLORS[index % COLORS.length]} />
                                    ))}
                                </Pie>
                                <Tooltip
                                    contentStyle={chartTooltipStyle}
                                    formatter={(value) => value != null ? (value as number).toFixed(2) : ''}
                                />
                            </PieChart>
                        </ResponsiveContainer>
                    </div>

                    <div className="rounded-3xl border border-border bg-card p-4">
                        <h3 className="mb-3 text-sm font-medium">{t('chart.modelRequests')}</h3>
                        <ResponsiveContainer width="100%" height={240}>
                            <BarChart data={topModels.concat(otherModels.length > 0 ? [{ model_name: t('model.other'), requests: otherRequests, cost: 0, tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, cache_hit_rate: 0 }] : [])}>
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
