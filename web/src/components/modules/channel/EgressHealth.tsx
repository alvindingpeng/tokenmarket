import { Activity, CheckCircle2, CircleOff, Clock3, PauseCircle } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelEgressHealth, type ChannelEgressHealth } from '@/api/channel';

function formatDuration(ms: number) {
    if (ms < 1000) return `${Math.max(0, Math.ceil(ms))}ms`;
    return `${(ms / 1000).toFixed(ms >= 10_000 ? 0 : 1)}s`;
}

function EgressHealthRow({ egress }: { egress: ChannelEgressHealth }) {
    const t = useTranslations('channel.stats');
    const kind = egress.kind === 'socks5' ? 'SOCKS5' : egress.kind === 'http' ? 'HTTP' : t('egressDirect');
    const status = !egress.enabled ? t('egressDisabled') : egress.cooling ? t('egressCooling') : egress.has_data ? t('egressHealthy') : t('egressCold');
    const StatusIcon = !egress.enabled ? PauseCircle : egress.cooling ? CircleOff : egress.has_data ? CheckCircle2 : Activity;
    const tone = !egress.enabled ? 'text-muted-foreground' : egress.cooling ? 'text-amber-500' : egress.has_data ? 'text-emerald-500' : 'text-sky-500';

    return (
        <div className="rounded-xl border border-border/50 px-3 py-2">
            <div className="flex items-center justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                    <StatusIcon className={`h-3.5 w-3.5 shrink-0 ${tone}`} aria-hidden="true" />
                    <span className="truncate text-xs font-medium">{egress.name}</span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">{kind}{egress.region ? ` · ${egress.region}` : ''}</span>
                </div>
                <span className={`shrink-0 text-[10px] ${tone}`}>{status}</span>
            </div>
            <div className="mt-1.5 grid grid-cols-3 gap-2 text-[11px] tabular-nums text-muted-foreground">
                <span>{t('egressSuccess')}: {egress.has_data ? `${(egress.success_rate * 100).toFixed(1)}%` : '—'}</span>
                <span>{t('egressLatency')}: {egress.has_data ? `${Math.round(egress.avg_wait_ms)}ms` : '—'}</span>
                <span>{t('egressSamples')}: {egress.samples}</span>
            </div>
            {egress.cooling && egress.cooldown_remaining_ms > 0 && (
                <div className="mt-1 flex items-center gap-1 text-[10px] text-amber-500">
                    <Clock3 className="h-3 w-3" aria-hidden="true" />
                    {t('egressCooldownRemaining', { duration: formatDuration(egress.cooldown_remaining_ms) })}
                </div>
            )}
        </div>
    );
}

export function EgressHealth({ channelId }: { channelId: number }) {
    const t = useTranslations('channel.stats');
    const { data, isLoading, isError } = useChannelEgressHealth(channelId);

    return (
        <section data-share-exclude className="shrink-0 rounded-2xl border bg-card p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
                <h4 className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    <Activity className="h-3.5 w-3.5" aria-hidden="true" />{t('egressHealth')}
                </h4>
                {data && <span className="text-[10px] text-muted-foreground">{data.egresses.length}</span>}
            </div>
            {isLoading ? <div className="py-3 text-center text-xs text-muted-foreground">{t('egressLoading')}</div> :
                isError ? <div className="py-3 text-center text-xs text-destructive">{t('egressLoadFailed')}</div> :
                !data?.egresses.length ? <div className="py-3 text-center text-xs text-muted-foreground">{t('egressEmpty')}</div> :
                <div className="grid gap-2">{data.egresses.map((egress) => <EgressHealthRow key={egress.id} egress={egress} />)}</div>}
        </section>
    );
}