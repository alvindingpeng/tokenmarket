import type { ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { Gauge, Loader2, RefreshCw } from 'lucide-react';
import { quotaLevel, type QuotaUsage, type QuotaUsageResponse } from '@/api/quota-usage';

// QuotaCard 配额用量卡片: 用户自助与管理员排查共用同一张卡。
//
// 三条必须说清的信息, 缺一条这张卡就没有意义:
//   1) 额度是多少 —— 且必须说明来自"单独策略"还是"等级兜底"。
//      数值相同但含义不同: 改等级没变化时, 用户会以为改动没生效。
//   2) 已经用到多少 —— 否则只是个静态数字。
//   3) 什么时候重置 —— 分钟窗口滚动的数字, 不说清楚用户会反复刷新等它变。

type QueryLike = {
    isPending: boolean;
    isError: boolean;
    refetch: () => unknown;
};

// QuotaBar 单个维度的进度条。额度为 0 表示不限制, 此时不画进度(比例没有意义)只标"不限制"。
function QuotaBar({
    field,
    label,
    limit,
    used,
    remain,
    text,
}: {
    field: string;
    label: string;
    limit: number;
    used: number;
    remain: number;
    text: { unlimited: string; reset: string };
}) {
    const level = quotaLevel(limit, used);
    const ratio = level === 'unlimited' ? 0 : Math.min(used / limit, 1);
    const barClass =
        level === 'danger'
            ? 'bg-destructive'
            : level === 'warn'
              ? 'bg-amber-500'
              : 'bg-emerald-500';

    return (
        <div data-testid={`quota-${field}`}>
            <div className="flex items-baseline justify-between text-xs">
                <span className="text-muted-foreground">{label}</span>
                <span className="tabular-nums">
                    {limit > 0 ? (
                        <>
                            {used.toLocaleString()} / {limit.toLocaleString()}
                        </>
                    ) : (
                        <span className="text-muted-foreground">{text.unlimited}</span>
                    )}
                </span>
            </div>
            <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                {limit > 0 && (
                    <div
                        className={`h-full rounded-full transition-all ${barClass}`}
                        style={{ width: `${Math.round(ratio * 100)}%` }}
                    />
                )}
            </div>
            {limit > 0 && (
                <div className="mt-1 text-xs text-muted-foreground">
                    {text.reset.replace('{remain}', remain.toLocaleString())}
                </div>
            )}
        </div>
    );
}

// QuotaCardBody 已就绪的数据渲染; 拆出来便于在管理员页与自助页复用。
export function QuotaCardBody({ data }: { data: QuotaUsageResponse }) {
    const t = useTranslations('quota');
    const usage: QuotaUsage = data.usage;

    // 来源文案: 未限制时不说"等级兜底", 那会让人以为是某个等级给的无限额度。
    const sourceText =
        usage.source === 'policy'
            ? t('source.policy')
            : usage.source === 'tier'
              ? t('source.tier', { tier: t(`tier.${usage.tier}`) })
              : t('source.none');

    const resetText = usage.window_reset_at
        ? new Date(usage.window_reset_at).toLocaleTimeString()
        : '';

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span data-testid="quota-source">{sourceText}</span>
                {resetText && (
                    <>
                        <span aria-hidden="true">·</span>
                        <span>{t('windowReset', { time: resetText })}</span>
                    </>
                )}
            </div>
            <div className="grid gap-4 md:grid-cols-3">
                <QuotaBar
                    field="rpm"
                    label={t('rpm')}
                    limit={usage.rpm}
                    used={usage.used_rpm}
                    remain={data.remain_rpm}
                    text={{ unlimited: t('unlimited'), reset: t('remainReset') }}
                />
                <QuotaBar
                    field="tpm"
                    label={t('tpm')}
                    limit={usage.tpm}
                    used={usage.used_tpm}
                    remain={data.remain_tpm}
                    text={{ unlimited: t('unlimited'), reset: t('remainReset') }}
                />
                <QuotaBar
                    field="concurrent"
                    label={t('concurrent')}
                    limit={usage.concurrent}
                    used={usage.in_flight}
                    remain={data.remain_concurrent}
                    text={{ unlimited: t('unlimited'), reset: t('remainReset') }}
                />
            </div>
        </div>
    );
}

// QuotaCard 带加载/失败/空态外壳的完整卡片。
// 失败必须报错而不是显示 0: 查不到用量被渲染成"用了 0", 用户会以为自己额度充足。
export function QuotaCard({
    query,
    title,
    extra,
}: {
    query: QueryLike & { data?: QuotaUsageResponse };
    title?: ReactNode;
    extra?: ReactNode;
}) {
    const t = useTranslations('quota');

    return (
        <div className="shrink-0 rounded-3xl border border-border bg-card p-4" data-testid="quota-card">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <h3 className="flex items-center gap-2 text-sm font-medium">
                    <Gauge className="size-4" aria-hidden="true" />
                    {title ?? t('title')}
                </h3>
                {extra}
            </div>
            {query.isError ? (
                <div role="alert" className="flex h-24 flex-col items-center justify-center gap-2 text-muted-foreground">
                    <p>{t('error')}</p>
                    <button
                        type="button"
                        className="inline-flex items-center gap-1 rounded-xl border border-border px-3 py-1 text-xs"
                        onClick={() => void query.refetch()}
                    >
                        <RefreshCw className="size-3" aria-hidden="true" />
                        {t('retry')}
                    </button>
                </div>
            ) : query.isPending || !query.data ? (
                <div className="flex h-24 items-center justify-center gap-2 text-muted-foreground">
                    <Loader2 className="size-5 animate-spin" aria-hidden="true" />
                    {t('loading')}
                </div>
            ) : (
                <QuotaCardBody data={query.data} />
            )}
        </div>
    );
}
