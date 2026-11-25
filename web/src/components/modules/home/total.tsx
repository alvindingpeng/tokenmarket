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
import { useTranslations } from 'use-intl';
import { useStatsHome } from '@/api/stats';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';

// Total 展示累计的请求, 总量, 输入和输出四组指标卡片。
export function Total() {
    // 首页所有区块共用同一条聚合查询, React Query 按 queryKey 去重, 首屏只发一次请求。
    const { data } = useStatsHome();
    const stats = data?.total;
    const t = useTranslations('home.total');
    // 口径说明单独挂在 home.stats.* 下: 它描述的是数字的来源, 不是这一格的标题。
    const tStats = useTranslations('home.stats');

    const cards = [
        {
            title: t('requestStats'),
            headerIcon: Activity,
            items: [
                // 这一格是请求口径: 成功与失败都各算一次, 失败请求也计入。
                // 同页的词元/费用只来自产生了用量的调用(失败的调用没有用量), 两者天然不等;
                // 不写清楚的话, 用户会拿"请求数"去除"词元数"或与趋势图对账, 得出"少算了一大截"的错误结论。
                { label: t('requestCount'), metric: stats?.request_count, icon: MessageSquare, bgColor: 'bg-primary/10', hint: tStats('requestCountHint') },
                { label: t('timeConsumed'), metric: stats?.wait_time, icon: Clock, bgColor: 'bg-accent/10', hint: undefined },
            ],
        },
        {
            title: t('totalStats'),
            headerIcon: ChartColumnBig,
            items: [
                { label: t('totalToken'), metric: stats?.total_token, icon: Bot, bgColor: 'bg-chart-1/10', hint: undefined },
                { label: t('totalCost'), metric: stats?.total_cost, icon: DollarSign, bgColor: 'bg-chart-2/10', hint: undefined },
            ],
        },
        {
            title: t('inputStats'),
            headerIcon: ArrowDownToLine,
            items: [
                { label: t('inputTokens'), metric: stats?.input_token, icon: Rewind, bgColor: 'bg-chart-3/10', hint: undefined },
                { label: t('inputCost'), metric: stats?.input_cost, icon: DollarSign, bgColor: 'bg-chart-3/10', hint: undefined },
            ],
        },
        {
            title: t('outputStats'),
            headerIcon: ArrowUpFromLine,
            items: [
                { label: t('outputTokens'), metric: stats?.output_token, icon: FastForward, bgColor: 'bg-chart-4/10', hint: undefined },
                { label: t('outputCost'), metric: stats?.output_cost, icon: DollarSign, bgColor: 'bg-chart-4/10', hint: undefined },
            ],
        },
    ];

    return (
        <div className="grid grid-cols-1 @xl/home:grid-cols-2 @5xl/home:grid-cols-4 gap-4">
            {cards.map((card) => (
                <section
                    key={card.title}
                    className="home-summary-card min-w-0 rounded-3xl bg-card border-border border p-4 sm:p-5 text-card-foreground flex flex-row items-center gap-4"
                >
                    <div className="flex flex-col items-center justify-center gap-3 border-r border-border/50 pr-4 py-1 self-stretch">
                        <card.headerIcon className="w-4 h-4" />
                        <h3 className="font-medium text-sm [writing-mode:vertical-lr]">{card.title}</h3>
                    </div>

                    <div className="flex flex-col gap-4 flex-1 min-w-0">
                        {card.items.map((item) => (
                            <div key={item.label} className="flex items-center gap-3">
                                <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${item.bgColor}`}>
                                    <item.icon className="w-5 h-5" />
                                </div>
                                <div className="flex flex-col min-w-0">
                                    <span className="text-xs text-muted-foreground">{item.label}</span>
                                    {/* 口径说明紧跟标题、字号更小: 让人先看数字, 需要时再确认这一格代表什么。 */}
                                    {item.hint && (
                                        <span className="text-[10px] leading-tight text-muted-foreground/70" data-testid="request-count-scope">
                                            {item.hint}
                                        </span>
                                    )}
                                    <div className="flex flex-wrap items-baseline gap-x-1">
                                        <span className="text-xl">
                                            <AnimatedNumber value={item.metric?.formatted.value} />
                                        </span>
                                        {/* 计数在千位以下 unit 为空串, 空 span 仍是 flex 项, 会多出 gap-1 的间距。 */}
                                        {item.metric?.formatted.unit && (
                                            <span className="text-sm text-muted-foreground">{item.metric.formatted.unit}</span>
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
