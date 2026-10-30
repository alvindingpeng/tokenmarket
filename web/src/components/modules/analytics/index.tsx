import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { BarChart3, Calendar, Loader2, TrendingUp } from 'lucide-react';
import { Bar, BarChart, CartesianGrid, Cell, Legend, Line, LineChart, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useUserStatsModels, useUserStatsOverview, useUserStatsTrend, type UserStatsQuery } from '@/api/user-stats';
import { formatAmount } from '@/lib/format';

// 预设时间范围
const PRESETS = ['today', 'week', 'month', 'custom'] as const;
type Preset = typeof PRESETS[number];

// 计算日期范围
function getDateRange(preset: Preset): Pick<UserStatsQuery, 'start_date' | 'end_date'> {
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    
    switch (preset) {
        case 'today':
            return {
                start_date: formatDate(today),
                end_date: formatDate(today),
            };
        case 'week': {
            const weekAgo = new Date(today);
            weekAgo.setDate(weekAgo.getDate() - 6);
            return {
                start_date: formatDate(weekAgo),
                end_date: formatDate(today),
            };
        }
        case 'month': {
            const monthAgo = new Date(today);
            monthAgo.setDate(monthAgo.getDate() - 29);
            return {
                start_date: formatDate(monthAgo),
                end_date: formatDate(today),
            };
        }
        default:
            return {};
    }
}

function formatDate(date: Date): string {
    return date.toISOString().slice(0, 10);
}

// AnalyticsPage 用户调用统计分析页面。
export function AnalyticsPage() {
    const t = useTranslations('analytics');
    const [preset, setPreset] = useState<Preset>('month');
    const [granularity, setGranularity] = useState<'day' | 'week' | 'month'>('day');
    
    const dateRange = getDateRange(preset);
    const overview = useUserStatsOverview(dateRange);
    const trend = useUserStatsTrend({ ...dateRange, granularity });
    const models = useUserStatsModels(dateRange);
    
    const loading = overview.isLoading || trend.isLoading || models.isLoading;
    const data = overview.data ?? { total_requests: 0, success_rate: 0, total_cost: 0, total_tokens: 0 };
    
    // 模型分布前 10 + 其他聚合
    const topModels = (models.data ?? []).slice(0, 10);
    const otherModels = (models.data ?? []).slice(10);
    const otherCost = otherModels.reduce((sum, m) => sum + m.cost, 0);
    const otherRequests = otherModels.reduce((sum, m) => sum + m.requests, 0);
    const modelData = topModels.map(m => ({ name: m.model_name, value: m.cost }));
    if (otherCost > 0) {
        modelData.push({ name: t('model.other'), value: otherCost });
    }
    
    const COLORS = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4', '#84cc16', '#f97316', '#6366f1', '#64748b'];
    
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
            
            {loading ? (
                <div className="flex h-full items-center justify-center gap-2 text-muted-foreground">
                    <Loader2 className="size-5 animate-spin" />
                    {t('loading')}
                </div>
            ) : (
                <>
                    {/* 概览卡片 */}
                    <div className="grid shrink-0 grid-cols-2 gap-2 md:grid-cols-4">
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.requests')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{data.total_requests.toLocaleString()}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.successRate')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{data.success_rate.toFixed(1)}%</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.cost')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{formatAmount(data.total_cost)}</div>
                        </div>
                        <div className="rounded-3xl border border-border bg-card px-4 py-3">
                            <div className="text-xs text-muted-foreground">{t('card.tokens')}</div>
                            <div className="mt-1 text-2xl font-semibold tabular-nums">{(data.total_tokens / 1000).toFixed(1)}K</div>
                        </div>
                    </div>
                    
                    {/* 趋势图 */}
                    <div className="shrink-0 rounded-3xl border border-border bg-card p-4">
                        <h3 className="mb-3 flex items-center gap-2 text-sm font-medium">
                            <TrendingUp className="size-4" />
                            {t('chart.trend')}
                        </h3>
                        <ResponsiveContainer width="100%" height={240}>
                            <LineChart data={trend.data ?? []}>
                                <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
                                <XAxis dataKey="date" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                                <YAxis yAxisId="left" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                                <YAxis yAxisId="right" orientation="right" tick={{ fontSize: 12 }} stroke="hsl(var(--muted-foreground))" />
                                <Tooltip
                                    contentStyle={{
                                        backgroundColor: 'hsl(var(--card))',
                                        border: '1px solid hsl(var(--border))',
                                        borderRadius: '8px',
                                    }}
                                />
                                <Legend wrapperStyle={{ fontSize: 12 }} />
                                <Line yAxisId="left" type="monotone" dataKey="requests" stroke="#3b82f6" name={t('chart.requests')} strokeWidth={2} dot={{ r: 3 }} />
                                <Line yAxisId="right" type="monotone" dataKey="cost" stroke="#10b981" name={t('chart.cost')} strokeWidth={2} dot={{ r: 3 }} />
                            </LineChart>
                        </ResponsiveContainer>
                    </div>
                    
                    {/* 模型分布 */}
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
                                        contentStyle={{
                                            backgroundColor: 'hsl(var(--card))',
                                            border: '1px solid hsl(var(--border))',
                                            borderRadius: '8px',
                                        }}
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
                                    <Tooltip
                                        contentStyle={{
                                            backgroundColor: 'hsl(var(--card))',
                                            border: '1px solid hsl(var(--border))',
                                            borderRadius: '8px',
                                        }}
                                    />
                                    <Bar dataKey="requests" fill="#3b82f6" />
                                </BarChart>
                            </ResponsiveContainer>
                        </div>
                    </div>
                </>
            )}
        </div>
    );
}
