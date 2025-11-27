'use client';

import { useStatsDaily } from '@/api/endpoints/stats';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { useMemo, useState } from 'react';
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { useTranslations } from 'next-intl';
import { formatCount, formatMoney } from '@/lib/utils';
import dayjs from 'dayjs';

const PERIODS = ['7', '30'] as const;

export function StatsChart() {
    const { data: statsDaily } = useStatsDaily();
    const [period, setPeriod] = useState<typeof PERIODS[number]>('7');
    const t = useTranslations('home.chart');

    const chartData = useMemo(() => {
        if (!statsDaily) return [];
        const days = parseInt(period);
        return statsDaily.raw.slice(-days).map((stat) => {
            return {
                date: dayjs(stat.date).format('MM/DD'),
                total_cost: stat.input_cost + stat.output_cost,
            };
        });
    }, [statsDaily, period]);

    const totals = useMemo(() => {
        if (!statsDaily) return { requests: 0, cost: 0 };
        const days = parseInt(period);
        const recentStats = statsDaily.raw.slice(-days);
        return {
            requests: recentStats.reduce((acc, stat) => acc + stat.request_success + stat.request_failed, 0),
            cost: recentStats.reduce((acc, stat) => acc + stat.input_cost + stat.output_cost, 0),
        };
    }, [statsDaily, period]);

    const chartConfig = {
        total_cost: { label: t('totalCost') },
    };

    const getPeriodLabel = (p: typeof period) => {
        const labels = {
            '7': t('period.last7Days'),
            '30': t('period.last30Days'),
        };
        return labels[p];
    };

    const handlePeriodClick = () => {
        const currentIndex = PERIODS.indexOf(period);
        const nextIndex = (currentIndex + 1) % PERIODS.length;
        setPeriod(PERIODS[nextIndex]);
    };

    return (
        <div className="rounded-3xl bg-card border-card-border border pt-2 pb-0 text-card-foreground custom-shadow">
            <div className="flex justify-between items-start px-4 pb-2">
                <div className="flex gap-2 text-sm">
                    <div>
                        <div className="text-xs text-muted-foreground">{t('totalRequests')}</div>
                        <div className="text-xl font-semibold">
                            {formatCount(totals.requests).value}
                            <span className="text-base ml-0.5">{formatCount(totals.requests).unit}</span>
                        </div>
                    </div>
                    <div className="w-px bg-border self-stretch"></div>
                    <div>
                        <div className="text-xs text-muted-foreground">{t('totalCost')}</div>
                        <div className="text-xl font-semibold">
                            {formatMoney(totals.cost).value}
                            <span className="text-base ml-0.5">{formatMoney(totals.cost).unit}</span>
                        </div>
                    </div>
                </div>
                <div
                    className="flex gap-2 text-sm cursor-pointer hover:opacity-80 transition-opacity"
                    onClick={handlePeriodClick}
                >
                    <div>
                        <div className="text-xs text-muted-foreground">{t('timePeriod')}</div>
                        <div className="text-xl font-semibold">{getPeriodLabel(period)}</div>
                    </div>
                </div>
            </div>
            <ChartContainer config={chartConfig} className="h-40 w-full" >
                <AreaChart accessibilityLayer data={chartData}>
                    <defs>
                        <linearGradient id="fillTotalCost" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="5%" stopColor="var(--chart-1)" stopOpacity={1.0} />
                            <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0.1} />
                        </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" vertical={false} />
                    <XAxis dataKey="date" tickLine={false} axisLine={false} />
                    <YAxis
                        tickLine={false}
                        axisLine={false}
                        tickFormatter={(value) => {
                            const formatted = formatMoney(value);
                            return `${formatted.value}${formatted.unit}`;
                        }}
                    />
                    <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="line" />} />
                    <Area type="monotone" dataKey="total_cost" stroke="var(--chart-1)" fill="url(#fillTotalCost)" />
                </AreaChart>
            </ChartContainer>
        </div>
    );
}
