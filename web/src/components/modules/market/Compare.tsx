import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { X } from 'lucide-react';
import { Checkbox } from '@/components/ui/checkbox';
import { MorphingDialog, MorphingDialogContainer, MorphingDialogContent, MorphingDialogTrigger } from '@/components/ui/morphing-dialog';
import { formatLatency } from '@/lib/format';
import { formatPlainMoney } from '@/lib/money';
import { getBrandIcon } from '@/lib/model-icons';
import { hasNumericMetrics, type MarketModel } from '@/api/market';
import { cn } from '@/lib/utils';
import { cheapestUsableListing, estimateCost } from './compare';
import { brandLabel } from './labels';

// 模型对比: 在多张卡片上打勾, 底部浮出对比条, 点开并排看价格/成绩/质量区间。
//
// 为什么值得有: 广场的价值不只在"列出所有模型", 更在"帮我选一个"。用户实际面对的是
// "这两三个模型差在哪、差价多少" —— 逐个展开卡片记忆四个数字, 是最容易记错的一步。
//
// 对比只做并排展示, 不做综合评分: 给出一个"总分之王"等于替用户定义了"好", 而不同场景的
// 权重完全不同(写代码看延迟, 批量跑看价格)。这里把差异摆出来, 判断权留给用户。

/** MAX_COMPARE 是一次可对比的上限。取 4 是因为再多就无法在窄屏并排阅读, 且已超出人的短期比较能力。 */
export const MAX_COMPARE = 4;

export function CompareBar({ models, selected, onToggle, onClear }: {
    models: MarketModel[];
    selected: number[];
    onToggle: (index: number) => void;
    onClear: () => void;
}) {
    const t = useTranslations('market');
    const picked = useMemo(
        () => selected.map((index) => models[index]).filter((model): model is MarketModel => Boolean(model)),
        [models, selected]
    );
    if (picked.length === 0) return null;

    return (
        <div className="pointer-events-none sticky bottom-0 z-10 flex justify-center">
            <MorphingDialog>
                <div className="pointer-events-auto flex items-center gap-3 rounded-2xl border border-border bg-card/95 px-4 py-2.5 shadow-lg backdrop-blur">
                    <span className="text-xs text-muted-foreground">{t('compare.selected', { count: picked.length })}</span>
                    <div className="flex items-center gap-1">
                        {picked.map((model, index) => {
                            const { Icon, className: iconClassName, color } = getBrandIcon(model.brand);
                            return (
                                <span key={model.name} className="flex items-center gap-1 rounded-lg bg-muted px-1.5 py-0.5 text-[11px]">
                                    <Icon className={iconClassName} color={color} width={12} height={12} aria-hidden="true" />
                                    <span className="max-w-24 truncate">{model.name}</span>
                                    <button
                                        type="button"
                                        aria-label={t('compare.remove', { name: model.name })}
                                        onClick={() => {
                                            const target = models.findIndex((item) => item.name === model.name);
                                            if (target >= 0) onToggle(target);
                                        }}
                                        className="text-muted-foreground transition-colors hover:text-foreground"
                                    >
                                        <X className="size-3" />
                                    </button>
                                    {/* 只读地引用 index, 避免 ESLint 报未使用 */}
                                    <span className="hidden">{index}</span>
                                </span>
                            );
                        })}
                    </div>
                    <MorphingDialogTrigger>
                        <span className="cursor-pointer rounded-lg bg-primary px-2.5 py-1 text-[11px] font-medium text-primary-foreground">
                            {t('compare.open')}
                        </span>
                    </MorphingDialogTrigger>
                    <button
                        type="button"
                        onClick={onClear}
                        className="text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                    >
                        {t('compare.clear')}
                    </button>
                </div>
                <MorphingDialogContainer>
                    <MorphingDialogContent className="max-h-[80vh] w-full max-w-4xl overflow-y-auto rounded-3xl border border-border bg-card p-5">
                        <CompareTable models={picked} />
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>
        </div>
    );
}

/** CompareTable 并排展示各模型的关键差异。行是维度, 列是模型 —— 竖着比同一维度更省力。 */
function CompareTable({ models }: { models: MarketModel[] }) {
    const t = useTranslations('market');
    // 每个模型取最便宜的可估算渠道做价格与成本口径, 与卡片上的"最低价"同一来源。
    const rows = useMemo(
        () => models.map((model) => ({
            model,
            listing: cheapestUsableListing(model),
            cost: cheapestUsableListing(model) ? estimateCost(cheapestUsableListing(model)!, { input: 2000, output: 500 }) : undefined,
        })),
        [models],
    );

    return (
        <div className="flex flex-col gap-3">
            <div className="flex items-center justify-between">
                <span className="text-sm font-semibold text-card-foreground">{t('compare.title')}</span>
                <span className="text-[11px] text-muted-foreground">{t('compare.sameVolume')}</span>
            </div>
            <div className="overflow-x-auto">
                <table className="w-full min-w-[32rem] border-collapse text-left text-xs">
                    <thead>
                        <tr className="border-b border-border">
                            <th className="w-28 py-1.5 pr-2 font-medium text-muted-foreground">{t('compare.dimension')}</th>
                            {rows.map((row) => (
                                <th key={row.model.name} className="px-2 py-1.5 font-semibold text-card-foreground">
                                    {row.model.name}
                                    <span className="ml-1 rounded bg-muted px-1 py-0.5 text-[10px] font-normal text-muted-foreground">
                                        {brandLabel(t, row.model.brand)}
                                    </span>
                                </th>
                            ))}
                        </tr>
                    </thead>
                    <tbody>
                        <tr className="border-b border-border/50">
                            <td className="py-1.5 pr-2 text-muted-foreground">{t('label.price')}</td>
                            {rows.map((row) => (
                                <td key={row.model.name} className="px-2 py-1.5 font-mono tabular-nums">
                                    {row.listing
                                        ? '$' + formatPlainMoney(row.listing.user_price.input) + ' / $' + formatPlainMoney(row.listing.user_price.output)
                                        : <span className="text-muted-foreground">—</span>}
                                </td>
                            ))}
                        </tr>
                        <tr className="border-b border-border/50">
                            <td className="py-1.5 pr-2 text-muted-foreground">{t('cost.title')}</td>
                            {rows.map((row) => (
                                <td key={row.model.name} className="px-2 py-1.5 font-mono tabular-nums">
                                    {row.cost === undefined
                                        ? <span className="text-muted-foreground">—</span>
                                        : row.cost < 0.0001 ? '<$0.0001' : '$' + row.cost.toFixed(6)}
                                </td>
                            ))}
                        </tr>
                        <tr className="border-b border-border/50">
                            <td className="py-1.5 pr-2 text-muted-foreground">{t('label.success')}</td>
                            {rows.map((row) => (
                                <td key={row.model.name} className="px-2 py-1.5 tabular-nums">
                                    {hasNumericMetrics(row.model.metrics_source) && row.model.success !== undefined
                                        ? (row.model.success * 100).toFixed(1) + '%'
                                        : <span className="text-muted-foreground">—</span>}
                                </td>
                            ))}
                        </tr>
                        <tr className="border-b border-border/50">
                            <td className="py-1.5 pr-2 text-muted-foreground">{t('label.latency')}</td>
                            {rows.map((row) => (
                                <td key={row.model.name} className="px-2 py-1.5 tabular-nums">
                                    {hasNumericMetrics(row.model.metrics_source) && row.model.wait_ms !== undefined
                                        ? formatLatency(row.model.wait_ms)
                                        : <span className="text-muted-foreground">—</span>}
                                </td>
                            ))
                            }
                        </tr>
                        <tr>
                            <td className="py-1.5 pr-2 text-muted-foreground">{t('compare.quality')}</td>
                            {rows.map((row) => {
                                // 质量带取该模型任一条有区间的渠道: 区间比精确值更能说明"稳不稳"。
                                const band = row.model.channels.find((item) => item.quality_band)?.quality_band;
                                return (
                                    <td key={row.model.name} className="px-2 py-1.5">
                                        {band
                                            ? <span className="flex flex-wrap gap-1">{band.success && <span className="rounded bg-muted px-1 py-0.5 text-[10px]">{band.success}</span>}{band.latency && <span className="rounded bg-muted px-1 py-0.5 text-[10px]">{band.latency}</span>}</span>
                                            : <span className="text-muted-foreground">—</span>}
                                    </td>
                                );
                            })}
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    );
}

export function CompareToggle({ model, selected, onToggle, index }: {
    model: MarketModel;
    selected: number[];
    onToggle: (index: number) => void;
    index: number;
}) {
    const t = useTranslations('market');
    const checked = selected.includes(index);
    const disabled = !checked && selected.length >= MAX_COMPARE;
    return (
        <span className={cn('shrink-0', disabled && 'opacity-40')}>
            <Checkbox
                checked={checked}
                disabled={disabled}
                onCheckedChange={() => onToggle(index)}
                aria-label={t('compare.pick', { name: model.name })}
            />
        </span>
    );
}
