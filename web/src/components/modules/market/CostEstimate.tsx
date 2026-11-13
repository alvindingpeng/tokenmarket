import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Calculator } from 'lucide-react';
import { Input } from '@/components/ui/input';
import type { MarketModel } from '@/api/market';
import { cn } from '@/lib/utils';
import { estimateCost, cheapestUsableListing, isMediaModel, type TokenVolume } from './compare';

// CostEstimate 在展开的卡片里按"一次典型调用"的用量估算花费。
//
// 为什么值得有: 广场列的是"每百万 token 的单价", 而用户真正想知道的是"我这个请求要花多少"。
// 单价到账单之间隔着一次心算, 而心算恰好是最容易错的一步(尤其输入输出价差一档以上时)。
// 这里直接把那步算出来, 但只作为估算展示 —— 真实计费以 relay 的结算为准, 文案里必须写清。
//
// 不做的事: 不猜用户的实际用量。默认值是一个"聊天一轮"的保守量, 全部可改。

/** DEFAULTS 是三个预设档位的用量, 覆盖最常见的三种请求形态。 */
const PRESETS: Array<{ key: 'chat' | 'doc' | 'agent'; volume: TokenVolume }> = [
    { key: 'chat', volume: { input: 2000, output: 500 } },
    { key: 'doc', volume: { input: 50000, output: 2000 } },
    { key: 'agent', volume: { input: 200000, output: 8000 } },
];

export function CostEstimate({ model }: { model: MarketModel }) {
    const t = useTranslations('market');
    const [preset, setPreset] = useState<'chat' | 'doc' | 'agent'>('chat');
    const [custom, setCustom] = useState<TokenVolume | null>(null);

    const listing = useMemo(() => cheapestUsableListing(model), [model]);
    const media = model.channels.every((item) => isMediaModel(item));

    const volume = custom ?? PRESETS.find((item) => item.key === preset)!.volume;
    const cost = listing ? estimateCost(listing, volume) : undefined;

    // 媒体模型不能按 token 估: 说清楚原因, 而不是给一个 0 或空白。
    if (media) {
        return (
            <div className="flex items-center gap-2 rounded-xl bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                <Calculator className="size-3.5" aria-hidden="true" />
                {t('cost.mediaNotSupported')}
            </div>
        );
    }
    if (!listing) {
        return (
            <div className="flex items-center gap-2 rounded-xl bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                <Calculator className="size-3.5" aria-hidden="true" />
                {t('cost.noPricedChannel')}
            </div>
        );
    }

    return (
        <div className="flex flex-col gap-2 rounded-xl bg-muted/40 px-3 py-2">
            <div className="flex flex-wrap items-center gap-2 text-xs">
                <Calculator className="size-3.5 text-muted-foreground" aria-hidden="true" />
                <span className="font-medium text-card-foreground">{t('cost.title')}</span>
                <div className="flex items-center gap-1">
                    {PRESETS.map((item) => (
                        <button
                            key={item.key}
                            type="button"
                            onClick={() => {
                                setPreset(item.key);
                                setCustom(null);
                            }}
                            className={cn(
                                'rounded-md px-1.5 py-0.5 text-[11px] transition-colors',
                                preset === item.key && custom === null
                                    ? 'bg-primary/15 font-medium text-primary'
                                    : 'text-muted-foreground hover:text-foreground'
                            )}
                        >
                            {t('cost.preset.' + item.key)}
                        </button>
                    ))}
                </div>
                <span className="ml-auto font-mono text-sm font-semibold tabular-nums text-card-foreground">
                    {cost !== undefined && cost < 0.0001 ? '<$0.0001' : '$' + Number(cost ?? 0).toFixed(6)}
                </span>
            </div>
            <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
                <span>{t('cost.volume', { input: volume.input, output: volume.output })}</span>
                {/* 允许改用量: 预设只是起点, 用户的实际请求往往既不是 2000 也不是 50000。 */}
                <div className="ml-auto flex items-center gap-1">
                    <span>{t('cost.inputLabel')}</span>
                    <Input
                        type="number"
                        min={0}
                        value={volume.input}
                        onChange={(event) => {
                            const input = Math.max(0, Number(event.target.value) || 0);
                            setCustom({ ...volume, input });
                        }}
                        className="h-6 w-20 px-1.5 py-0 text-[11px]"
                    />
                    <span>{t('cost.outputLabel')}</span>
                    <Input
                        type="number"
                        min={0}
                        value={volume.output}
                        onChange={(event) => {
                            const output = Math.max(0, Number(event.target.value) || 0);
                            setCustom({ ...volume, output });
                        }}
                        className="h-6 w-20 px-1.5 py-0 text-[11px]"
                    />
                </div>
            </div>
            {/* 估算口径必须写明: 这行字是"这不是账单"的唯一依据, 删掉它这个数字就变成了承诺。 */}
            <span className="text-[10px] text-muted-foreground">{t('cost.disclaimer')}</span>
        </div>
    );
}
