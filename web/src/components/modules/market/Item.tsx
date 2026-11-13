import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getBrandIcon } from '@/lib/model-icons';
import { formatLatency } from '@/lib/format';
import { cn } from '@/lib/utils';
import { availableCount, cheapestListing, hasNumericMetrics, type MarketModel } from '@/api/market';
import { AddToGroup } from './AddToGroup';
import { CostEstimate } from './CostEstimate';
import { CompareToggle } from './Compare';
import { MetricValue, PriceValue, StatusBadge } from './shared';
import { brandLabel, marketStatusOf, metricsNote, priceText, qualityBandTexts } from './labels';

// BAND_STYLES 是质量三条区间各自的配色, 顺序对应 成功率 / 延迟 / 可用率。
const BAND_STYLES = [
    'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
    'bg-sky-500/10 text-sky-600 dark:text-sky-400',
    'bg-violet-500/10 text-violet-600 dark:text-violet-400',
];

// MarketItem 渲染一条模型行: 折叠后的同名模型, 展开可看每条渠道编码与各自的价格/状态。
export function MarketItem({ model, layout, compareIndex, compareSelected, onToggleCompare }: {
    model: MarketModel;
    layout: 'grid' | 'list';
    // 对比勾选: 只有广场正文会传这三项。渠道/分组页复用本组件时不传, 就不会出现无处可去的勾选框。
    compareIndex?: number;
    compareSelected?: number[];
    onToggleCompare?: (index: number) => void;
}) {
    const t = useTranslations('market');
    const [expanded, setExpanded] = useState(false);
    const { Icon, className: iconClassName, color: brandColor } = useMemo(() => getBrandIcon(model.brand), [model.brand]);

    const cheapest = cheapestListing(model);
    const available = availableCount(model);
    const joined = model.in_groups.length > 0;
    // 折叠态是否需要状态徽章: 全部可用且已定价时不显示, 免得每张卡片都挂同一个绿标。
    const needsBadge = model.channels.some(
        (listing) => !listing.available || listing.unpriced || marketStatusOf(listing) === 'fx_missing' || marketStatusOf(listing) === 'breaker'
    );

    return (
        <article className="group rounded-3xl border border-border bg-card p-4">
            <div className="flex items-center gap-3">
                <Icon aria-hidden="true" className={iconClassName} color={brandColor} width={40} height={40} />
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                    <div className="flex min-w-0 items-center gap-1.5">
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <span className="max-w-full truncate text-base font-semibold leading-tight text-card-foreground">
                                    {model.name}
                                </span>
                            </TooltipTrigger>
                            <TooltipContent side="top" sideOffset={10}>{model.name}</TooltipContent>
                        </Tooltip>
                        <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                            {brandLabel(t, model.brand)}
                        </span>
                        {joined && (
                            <span className="shrink-0 rounded-md bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                                {t('badge.inGroups')}
                            </span>
                        )}
                        {needsBadge && (
                            <span className="ml-auto shrink-0">
                                <StatusBadge listing={model.channels.find((listing) => !listing.available) ?? model.channels[0]} />
                            </span>
                        )}
                    </div>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                        <span className="inline-flex items-center gap-1">
                            {t('label.price')}{' '}
                            {cheapest
                                ? <PriceValue>{priceText(cheapest, t)}</PriceValue>
                                : <span>{t('label.noAvailable')}</span>}
                        </span>
                        <span>{t('label.channels', { count: model.channels.length, available })}</span>
                        <span className="inline-flex items-center gap-1">
                            {t('label.success')}{' '}
                            <MetricValue
                                value={hasNumericMetrics(model.metrics_source) ? model.success : undefined}
                                samples={hasNumericMetrics(model.metrics_source) ? model.samples : 0}
                                format={(v) => (v * 100).toFixed(1) + '%'}
                            />
                        </span>
                        <span className="inline-flex items-center gap-1">
                            {t('label.latency')}{' '}
                            <MetricValue
                                value={hasNumericMetrics(model.metrics_source) ? model.wait_ms : undefined}
                                samples={hasNumericMetrics(model.metrics_source) ? model.samples : 0}
                                format={(v) => formatLatency(v)}
                            />
                        </span>
                    </div>
                </div>
                {compareIndex !== undefined && onToggleCompare && compareSelected && (
                    <CompareToggle
                        model={model}
                        index={compareIndex}
                        selected={compareSelected}
                        onToggle={onToggleCompare}
                    />
                )}
                <button
                    type="button"
                    onClick={() => setExpanded((value) => !value)}
                    aria-expanded={expanded}
                    aria-label={t('label.details')}
                    className="shrink-0 rounded-lg p-1.5 text-muted-foreground transition-colors hover:text-foreground"
                >
                    {expanded ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
                </button>
            </div>

            {expanded && (
                <div className={cn('mt-3 flex flex-col gap-3', layout === 'list' && 'border-t border-border pt-3')}>
                    <ChannelTable model={model} />
                    <CostEstimate model={model} />
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        {/* 把口径说清楚: 这些数字来自进程内滑动平均, 服务重启后会重新累计。 */}
                        <span className="text-[11px] text-muted-foreground">
                            {metricsNote(t, model.metrics_source, model.samples)}
                        </span>
                        <AddToGroup model={model} />
                    </div>
                </div>
            )}
        </article>
    );
}

// ChannelTable 列出该模型下各渠道的挂牌: 编码、价格、成绩、状态。
// 定序由后端定稿(可用优先 → 价格升 → 编码升), 前端不再重排。
function ChannelTable({ model }: { model: MarketModel }) {
    const t = useTranslations('market');
    return (
        <div className="flex flex-col gap-1.5">
            {model.channels.map((listing) => (
                <div
                    key={listing.channel_grant_id}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl bg-muted/40 px-3 py-2 text-xs"
                >
                    <span className="font-mono font-medium text-card-foreground">{listing.channel_code}</span>
                    <span className="text-muted-foreground">
                        {t('label.success')}{' '}
                        <MetricValue
                            value={hasNumericMetrics(listing.metrics_source) ? listing.success : undefined}
                            samples={hasNumericMetrics(listing.metrics_source) ? listing.samples : 0}
                            format={(v) => (v * 100).toFixed(1) + '%'}
                        />
                    </span>
                    <span className="text-muted-foreground">
                        {t('label.latency')}{' '}
                        <MetricValue
                            value={hasNumericMetrics(listing.metrics_source) ? listing.wait_ms : undefined}
                            samples={hasNumericMetrics(listing.metrics_source) ? listing.samples : 0}
                            format={(v) => formatLatency(v)}
                        />
                    </span>
                    {/* 质量区间: 近 7 天的成功率/延迟/可用率落成区间文案。
                        只在样本达标时后端才会给这一段, 所以这里无需再判一次门槛 ——
                        前端重复判阈值必然与服务端分叉。不给精确值是与分享页同一条脱敏防线。 */}
                    {qualityBandTexts(listing.quality_band).map((text, index) => (
                        <span
                            key={text}
                            className={cn(
                                'rounded px-1.5 py-0.5 text-[10px]',
                                BAND_STYLES[index % BAND_STYLES.length]
                            )}
                        >
                            {text}
                        </span>
                    ))}
                    {listing.price_currency && listing.price_currency !== 'USD' && (
                        <span className="text-[10px] text-muted-foreground">
                            {t('label.convertedFrom', { currency: listing.price_currency })}
                        </span>
                    )}
                    <span className="ml-auto flex items-center gap-2">
                        <PriceValue unpriced={listing.unpriced || marketStatusOf(listing) === 'unpriced'}>
                            {priceText(listing, t)}
                        </PriceValue>
                        <StatusBadge listing={listing} />
                    </span>
                </div>
            ))}
        </div>
    );
}
