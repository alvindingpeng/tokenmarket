import { useEffect, useState, type ReactNode } from 'react';
import { CircleCheck } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { useGroupMetrics, type Group, type GroupMemberMetric, type GroupScoreBasis } from '@/api/group';

// MemberStatusProps 描述成员的冷却和亲和状态。
interface MemberStatusProps {
    group: Group; // group 提供当前路由和成员冷却时间戳。
    itemId?: number; // itemId 是待展示状态的成员 ID。
    now: number; // now 是所属列表共享的当前 Unix 毫秒时间。
    active?: boolean; // active 表示该成员当前正在使用。
    activeClassName?: string; // activeClassName 调整原有选中图标在不同列表中的间距。
}

// useRuntimeClock 为一个或多个分组提供共享倒计时当前时间。
export function useRuntimeClock(source?: Group | Group[]) {
    const [now, setNow] = useState(() => Date.now());
    const groups = source === undefined ? [] : Array.isArray(source) ? source : [source];
    let enabled = false;
    let lastDeadline = 0;
    for (const group of groups) {
        if (group.mode === 'manual') continue;
        enabled = true;
        lastDeadline = Math.max(lastDeadline, group.runtime.affinity_until);
        for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
            lastDeadline = Math.max(lastDeadline, cooldownUntil);
        }
    }

    useEffect(() => {
        if (!enabled) return;

        let timer = 0;
        const tick = () => {
            const next = Date.now();
            setNow(next);
            if (next >= lastDeadline) window.clearInterval(timer);
        };
        // 依赖变化后先异步校正一次，避免上一轮倒计时结束后残留的旧时间参与判断。
        const immediate = window.setTimeout(tick, 0);
        if (Date.now() < lastDeadline) timer = window.setInterval(tick, 1000);

        return () => {
            window.clearTimeout(immediate);
            window.clearInterval(timer);
        };
    }, [enabled, lastDeadline]);

    return now;
}

// formatLatency 把毫秒形式的滑动平均耗时缩写为展示文本。
const formatLatency = (ms: number) => (ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`);

// scoreTooltip 组装综合评分的悬浮说明: 各维度得分 + 本次评分实际采用的权重与可靠性下限。
// 权重由后端按「分组自定义 > 系统设置 > 内置兜底」解析后给出, 与选路取值一致, 前端不做二次推断。
function scoreTooltip(metric: GroupMemberMetric, basis: GroupScoreBasis | undefined, t: ReturnType<typeof useTranslations>) {
    const lines = [
        t('scoreTitle', { score: Math.round(metric.score) }),
        t('scoreDims', {
            price: Math.round(metric.price_score),
            latency: Math.round(metric.latency_score),
            success: Math.round(metric.success_score),
        }),
    ];
    if (basis) {
        lines.push(t('scoreBasis', {
            price: basis.price_weight,
            latency: basis.latency_weight,
            success: basis.success_weight,
            floor: Math.round(basis.reliability_floor * 100),
        }));
    }
    if (metric.cold) lines.push(t('scoreCold'));
    else if (!metric.healthy) lines.push(t('scoreUnhealthy'));
    return lines.join('\n');
}

// MemberStatus 展示成员的冷却、亲和倒计时、实时选路指标或当前使用圆点。
// 冷却与亲和对所有动态选路模式生效, 实时指标来自 Relay 的滑动平均采样。
export function MemberStatus({ group, itemId, now, active = false, activeClassName }: MemberStatusProps) {
    const t = useTranslations('group.card');
    const { data: metrics } = useGroupMetrics(group?.id);
    const metric = metrics?.items.find((m) => m.item_id === itemId);

    const metricBadge = metric && metric.samples > 0 && itemId !== undefined && (
        <span
            title={t('metricTitle', { samples: metric.samples })}
            className="shrink-0 rounded-md bg-muted/60 px-1.5 py-0 text-[10px] font-medium tabular-nums text-muted-foreground"
        >
            ≈{formatLatency(metric.wait_ms)} · {Math.round(metric.success * 100)}%
        </span>
    );

    let status: ReactNode = active ? (
        <span aria-hidden="true" className={cn('inline-flex shrink-0 text-primary', activeClassName)}>
            <CircleCheck className="size-4" />
        </span>
    ) : null;

    if (group.mode !== 'manual' && itemId !== undefined) {
        const cooldownUntil = group.runtime.cooldowns[itemId] ?? 0;
        const affinityUntil = group.runtime.current_item_id === itemId
            ? group.runtime.affinity_until
            : 0;
        if (now < cooldownUntil || now < affinityUntil) {
            const cooling = now < cooldownUntil;
            const deadline = cooling ? cooldownUntil : affinityUntil;
            const label = t(cooling ? 'cooling' : 'affinity', { seconds: Math.ceil((deadline - now) / 1000) });

            status = (
                <Badge
                    variant="outline"
                    className={cn(
                        'shrink-0 px-1.5 py-0 text-[10px] font-medium',
                        cooling
                            ? 'border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400'
                            : 'border-cyan-500/30 bg-cyan-500/10 text-cyan-600 dark:text-cyan-400'
                    )}
                >
                    {label}
                </Badge>
            );
        }
    }

    // 综合评分对全部成员都给出(冷成员按中性值参与), 故不受 samples 限制。
    const scoreBadge = metric && (
        <span
            title={scoreTooltip(metric, metrics?.basis, t)}
            className={cn(
                'shrink-0 rounded-md px-1.5 py-0 text-[10px] font-medium tabular-nums',
                metric.healthy
                    ? 'bg-primary/10 text-primary'
                    : 'bg-destructive/10 text-destructive'
            )}
        >
            {t('scoreBadge', { score: Math.round(metric.score) })}
        </span>
    );

    return (
        <span className="inline-flex shrink-0 items-center gap-1">
            {scoreBadge}
            {metricBadge}
            {status}
        </span>
    );
}
