import {
    Activity,
    MessageSquare,
    Clock,
    ArrowDownToLine,
    ChartColumnBig,
    Bot,
    ArrowUpFromLine,
    Rewind,
    DollarSign,
    FastForward
} from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useStatsTotal } from '@/api/endpoints/stats';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';

export function Total() {
    const { data } = useStatsTotal();
    const t = useTranslations('total');

    const requestStats = formatCount(data?.request_count);
    const timeStats = formatTime(data?.wait_time);
    const inputTokenStats = formatCount(data?.input_token);
    const outputTokenStats = formatCount(data?.output_token);
    const inputCostStats = formatMoney(data?.input_cost);
    const outputCostStats = formatMoney(data?.output_cost);
    const totalTokenStats = formatCount((data?.input_token || 0) + (data?.output_token || 0));
    const totalCostStats = formatMoney((data?.input_cost || 0) + (data?.output_cost || 0));

    const cards = [
        {
            title: t('requestStats'),
            headerIcon: Activity,
            items: [
                {
                    label: t('requestCount'),
                    value: requestStats.value,
                    icon: MessageSquare,
                    color: 'text-primary',
                    bgColor: 'bg-primary/10',
                    unit: requestStats.unit
                },
                {
                    label: t('timeConsumed'),
                    value: timeStats.value,
                    icon: Clock,
                    color: 'text-accent',
                    bgColor: 'bg-accent/10',
                    unit: timeStats.unit
                }
            ]
        },
        {
            title: t('totalStats'),
            headerIcon: ChartColumnBig,
            items: [
                {
                    label: t('totalToken'),
                    value: totalTokenStats.value,
                    icon: Bot,
                    color: 'text-chart-1',
                    bgColor: 'bg-chart-1/10',
                    unit: totalTokenStats.unit
                },
                {
                    label: t('totalCost'),
                    value: totalCostStats.value,
                    icon: DollarSign,
                    color: 'text-chart-2',
                    bgColor: 'bg-chart-2/10',
                    unit: totalCostStats.unit
                }
            ]
        },
        {
            title: t('inputStats'),
            headerIcon: ArrowDownToLine,
            items: [
                {
                    label: t('inputTokens'),
                    value: inputTokenStats.value,
                    icon: Rewind,
                    color: 'text-chart-3',
                    bgColor: 'bg-chart-3/10',
                    unit: inputTokenStats.unit
                },
                {
                    label: t('inputCost'),
                    value: inputCostStats.value,
                    icon: DollarSign,
                    color: 'text-chart-3',
                    bgColor: 'bg-chart-3/10',
                    unit: inputCostStats.unit
                }
            ]
        },
        {
            title: t('outputStats'),
            headerIcon: ArrowUpFromLine,
            items: [
                {
                    label: t('outputTokens'),
                    value: outputTokenStats.value,
                    icon: FastForward,
                    color: 'text-chart-4',
                    bgColor: 'bg-chart-4/10',
                    unit: outputTokenStats.unit
                },
                {
                    label: t('outputCost'),
                    value: outputCostStats.value,
                    icon: DollarSign,
                    color: 'text-chart-4',
                    bgColor: 'bg-chart-4/10',
                    unit: outputCostStats.unit
                }
            ]
        }
    ];

    return (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
            {cards.map((card, index) => (
                <section key={index} className="rounded-3xl bg-card border-card-border border p-5 text-card-foreground custom-shadow flex flex-row items-center gap-4">
                    <div className="flex flex-col items-center justify-center gap-3 border-r border-border/50 pr-4 py-1 self-stretch">
                        <card.headerIcon className="w-4 h-4" />
                        <h3 className="font-medium text-sm [writing-mode:vertical-lr]">{card.title}</h3>
                    </div>

                    <div className="flex flex-col gap-4 flex-1 min-w-0">
                        {card.items.map((item, idx) => (
                            <div key={idx} className="flex items-center gap-3">
                                <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${item.bgColor} ${item.color}`}>
                                    <item.icon className="w-5 h-5" />
                                </div>
                                <div className="flex flex-col min-w-0">
                                    <span className="text-xs text-muted-foreground">{item.label}</span>
                                    <div className="flex items-baseline gap-1">
                                        <span className="text-xl">
                                            {item.value ?? '-'}
                                        </span>
                                        {item.unit && item.value && (
                                            <span className="text-sm text-muted-foreground">{item.unit}</span>
                                        )}
                                    </div>
                                </div>
                            </div>
                        ))}
                    </div>
                </section>
            ))}
        </div>
    );
}
