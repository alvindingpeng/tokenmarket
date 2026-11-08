import type { ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import type { MarketChannelListing } from '@/api/market';
import { cn } from '@/lib/utils';
import { marketStatusOf, type MarketStatusKey } from './labels';

// 状态徽章与成绩/价格展示的共用零件。抽出来的理由: 卡片与展开行都要用同一套徽章与数字口径,
// 抄两份必然出现"卡片显示可用、展开行显示熔断中"。

const STATUS_STYLES: Record<MarketStatusKey, string> = {
    '': 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
    fx_missing: 'bg-rose-500/15 text-rose-600 dark:text-rose-400',
    disabled: 'bg-muted text-muted-foreground',
    key_disabled: 'bg-muted text-muted-foreground',
    breaker: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
    unpriced: 'bg-muted text-muted-foreground',
};

/** StatusBadge 渲染一条挂牌的状态徽章; 状态文案取自 market.status.*。 */
export function StatusBadge({ listing }: { listing: MarketChannelListing }) {
    const t = useTranslations('market');
    const status = marketStatusOf(listing);
    return (
        <span className={cn('shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-medium', STATUS_STYLES[status])}>
            {t('status.' + (status === '' ? 'available' : status))}
        </span>
    );
}

/**
 * MetricValue 渲染一项成绩: 无样本时显示 — 而不是 0 或 100%。
 *
 * 这条规则是整个广场最容易出错的展示: 把"没数据"渲染成 100% 成功会让用户挑中一个
 * 从没跑过的模型, 把 0 渲染出来又会让它看起来像"跑挂了"。两者都是把"未知"当成结论。
 */
export function MetricValue({ value, samples, format }: { value?: number; samples: number; format: (v: number) => string }) {
    if (samples <= 0 || value === undefined) return <span className="text-muted-foreground">—</span>;
    return <span className="tabular-nums">{format(value)}</span>;
}

/** PriceValue 价格位: 未定价显示"—"并挂"未定价"标记, 而不是显示 0 元。 */
export function PriceValue({ children, unpriced }: { children: ReactNode; unpriced?: boolean }) {
    const t = useTranslations('market');
    if (unpriced) {
        return (
            <span className="inline-flex items-center gap-1 text-muted-foreground">
                —
                <span className="rounded bg-muted px-1 py-0.5 text-[10px]">{t('status.unpriced')}</span>
            </span>
        );
    }
    return <span className="tabular-nums">{children}</span>;
}
