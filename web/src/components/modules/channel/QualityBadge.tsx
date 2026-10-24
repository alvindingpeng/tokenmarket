import { useTranslations } from 'use-intl';
import type { ChannelQualitySnapshot, ChannelTrustLevel } from '@/api/channel';

// trustMeta 是信任级别的展示配色与语义提示。
// 配色遵循「绿=已转正、蓝=观察中、黄=延长观察、红=已收回」的语义梯度，
// 与审核徽章（draft/pending/approved/rejected/stale）用同一套语义色，避免两套色系打架。
// 文案走 i18n（channel.quality.trust.*），这里只保留与语言无关的样式。
const trustTone: Record<ChannelTrustLevel, string> = {
    trusted: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 ring-emerald-500/20',
    observing: 'bg-sky-500/10 text-sky-600 dark:text-sky-400 ring-sky-500/20',
    probation: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 ring-amber-500/20',
    revoked: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 ring-rose-500/20',
};

// gradeTone 是质量等级的展示配色。
// 空等级（样本不足）单独给一档中性色，避免显示成 "D" 让人误判成最差。
const gradeTone: Record<string, string> = {
    A: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 ring-emerald-500/20',
    B: 'bg-sky-500/10 text-sky-600 dark:text-sky-400 ring-sky-500/20',
    C: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 ring-amber-500/20',
    D: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 ring-rose-500/20',
    '': 'bg-neutral-500/10 text-neutral-500 dark:text-neutral-400 ring-neutral-500/20',
};

// TrustBadge 渲染信任级别徽章；级别未知时按 observing 展示（后端新增级别也不至于裸奔）。
export function TrustBadge({ level }: { level?: ChannelTrustLevel }) {
    const t = useTranslations('channel.quality');
    const key = level ?? 'observing';
    const tone = trustTone[key] ?? trustTone.observing;
    // trust.<key> 与 trustHint.<key> 都按同一组 key 存在；未知级别回落到 observing 的文案。
    const label = t.has('trust.' + key) ? t('trust.' + key) : t('trust.observing');
    const hint = t.has('trustHint.' + key) ? t('trustHint.' + key) : t('trustHint.observing');
    return (
        <span className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[10px] font-medium ring-1 ring-inset ${tone}`} title={hint}>
            {label}
        </span>
    );
}

// GradeBadge 渲染质量等级；空等级显示 "--" 并提示样本不足。
export function GradeBadge({ grade }: { grade?: string }) {
    const t = useTranslations('channel.quality');
    const key = grade ?? '';
    const tone = gradeTone[key] ?? gradeTone[''];
    const title = key === '' ? t('gradeUnknown') : t('gradeHint', { grade: key });
    return (
        <span className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1 ring-inset ${tone}`} title={title}>
            {key === '' ? '--' : key}
        </span>
    );
}

// ObservationProgress 渲染观察期进度条。
// 两个维度同时展示：时间（还剩几天）与样本（已积累多少请求）。
// 只有两个都达标才会转正，只看时间会让人误以为「等够天数就行」。
export function ObservationProgress({
    level,
    until,
    samples,
    minSamples,
    successRate,
    floor,
    compact = false,
}: {
    level?: ChannelTrustLevel;
    until?: string | null;
    samples: number;
    minSamples: number;
    successRate: number;
    floor: number;
    compact?: boolean;
}) {
    const t = useTranslations('channel.quality');
    // 已转正（或级别未知）不显示进度条：转正后进度条毫无信息量。
    if (!level || level === 'trusted') {
        return null;
    }
    const daysLeft = daysUntil(until);
    const samplePct = minSamples > 0 ? Math.min(100, (samples / minSamples) * 100) : 100;
    const ratePct = Math.min(100, (successRate / Math.max(floor, 0.0001)) * 100);
    const met = samples >= minSamples && successRate >= floor;

    return (
        <div className="space-y-1.5">
            {!compact && (
                <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
                    <span className="truncate">
                        {samples > 0 ? t('sampleProgress', { samples, min: minSamples }) : t('sampleNone')}
                    </span>
                    <span className="shrink-0">{daysLeft > 0 ? t('daysLeft', { days: daysLeft }) : t('expired')}</span>
                </div>
            )}
            {/* 样本进度条：主条，决定「数据够不够」。 */}
            <div className="h-1.5 overflow-hidden rounded-full bg-neutral-200 dark:bg-neutral-700">
                <div
                    className={`h-full rounded-full transition-all ${met ? 'bg-emerald-500' : 'bg-sky-500'}`}
                    style={{ width: `${samplePct}%` }}
                />
            </div>
            {!compact && (
                <div className="flex items-center justify-between gap-2 text-[10px] text-muted-foreground/80">
                    <span className="truncate">
                        {t('successVsFloor', { rate: (successRate * 100).toFixed(1), floor: (floor * 100).toFixed(1) })}
                    </span>
                    <span className="shrink-0">
                        {met ? t('graduated') : t('belowFloor', { gap: ((floor - successRate) * 100).toFixed(1) })}
                    </span>
                </div>
            )}
            {/* 成功率进度条：副条，用琥珀色区分「数据够了但质量还差」。 */}
            <div className="h-1 overflow-hidden rounded-full bg-neutral-200 dark:bg-neutral-700">
                <div
                    className={`h-full rounded-full transition-all ${ratePct >= 100 ? 'bg-emerald-500' : 'bg-amber-500'}`}
                    style={{ width: `${ratePct}%` }}
                />
            </div>
        </div>
    );
}

// daysUntil 返回距离目标时间的天数（向上取整）；目标缺失或已过期返回 0。
function daysUntil(target?: string | null): number {
    if (!target) {
        return 0;
    }
    const diff = new Date(target).getTime() - Date.now();
    if (Number.isNaN(diff) || diff <= 0) {
        return 0;
    }
    return Math.ceil(diff / 86_400_000);
}

// QualityTrend 用极简条形渲染近 N 天等级，避免引入图表库。
// 每根条的高度固定、颜色按等级映射，鼠标悬停给出当日精确指标。
export function QualityTrend({ trend, days = 30 }: { trend?: ChannelQualitySnapshot[]; days?: number }) {
    const t = useTranslations('channel.quality');
    if (!trend || trend.length === 0) {
        return <p className="text-[11px] text-muted-foreground">{t('emptyTrend')}</p>;
    }
    const byDate = new Map(trend.map((row) => [row.date, row]));
    // 缺失的日期也要占位：跳过会让趋势条在视觉上"压缩"，把断档伪装成连续。
    const cells = buildDateRange(days).map((date) => ({ date, row: byDate.get(date) }));

    return (
        <div className="flex items-end gap-[2px]">
            {cells.map(({ date, row }) => (
                <div
                    key={date}
                    title={
                        row
                            ? t('dayTooltip', {
                                  date: formatDate(date),
                                  grade: row.grade || '--',
                                  rate: (row.success_rate * 100).toFixed(1),
                                  latency: row.avg_wait_ms,
                                  uptime: row.uptime_pct.toFixed(1),
                              })
                            : t('noData', { date: formatDate(date) })
                    }
                    className={`h-6 flex-1 rounded-sm ${row ? gradeBarClass(row.grade) : 'bg-neutral-200/60 dark:bg-neutral-700/40'}`}
                />
            ))}
        </div>
    );
}

// gradeBarClass 把等级映射到条形颜色；空等级用中性灰（无数据 ≠ 最差）。
function gradeBarClass(grade: string): string {
    switch (grade) {
        case 'A':
            return 'bg-emerald-500';
        case 'B':
            return 'bg-sky-500';
        case 'C':
            return 'bg-amber-500';
        case 'D':
            return 'bg-rose-500';
        default:
            return 'bg-neutral-400 dark:bg-neutral-600';
    }
}

// buildDateRange 生成从 days-1 天前到今天的日期序列（20060102 格式）。
function buildDateRange(days: number): string[] {
    const out: string[] = [];
    const now = new Date();
    for (let i = days - 1; i >= 0; i--) {
        const d = new Date(now);
        d.setDate(d.getDate() - i);
        out.push(formatCompact(d));
    }
    return out;
}

// formatCompact 把 Date 格式化成 20060102。
function formatCompact(d: Date): string {
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}${m}${day}`;
}

// formatDate 把 20060102 格式化成 2006-01-02。
function formatDate(compact: string): string {
    return `${compact.slice(0, 4)}-${compact.slice(4, 6)}-${compact.slice(6, 8)}`;
}
