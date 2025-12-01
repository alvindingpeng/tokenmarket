import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
} from '@/components/ui/morphing-dialog';
import { type StatsMetricsFormatted } from '@/api/endpoints/stats';
import { type Channel } from '@/api/endpoints/channel';
import { CardContent } from './CardContent';

export function Card({ channel, stats }: { channel: Channel, stats: StatsMetricsFormatted }) {
    const statusClasses = channel.enabled
        ? 'bg-accent/20 text-accent-foreground'
        : 'bg-destructive/20 text-destructive-foreground';

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <div className="relative flex h-full min-h-[12rem] flex-col justify-between gap-4 overflow-hidden rounded-2xl border border-border bg-card/80 p-5 custom-shadow">
                    <div className="pointer-events-none absolute inset-0 bg-gradient-to-br from-primary/5 via-transparent to-primary/10" />

                    <div className="relative flex items-start justify-between gap-3">
                        <h3 className="line-clamp-2 text-lg font-semibold text-card-foreground">
                            {channel.name}
                        </h3>
                        <span className={`rounded-full px-3 py-1 text-xs font-medium ${statusClasses}`}>
                            {channel.enabled ? '启用' : '禁用'}
                        </span>
                    </div>

                    <div className="relative grid grid-cols-1 gap-3">
                        <div className="flex items-center justify-between rounded-xl border border-border/70 bg-background/80 px-3 py-2">
                            <div className="flex items-center gap-3">
                                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                                    <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12h6m-6 4h6m-7 4h8a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2H8a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2z" />
                                    </svg>
                                </div>
                                <p className="text-sm text-muted-foreground">请求次数</p>
                            </div>
                            <p className="text-base font-semibold text-card-foreground">
                                {stats.request_count.formatted.value}
                                <span className="ml-1 text-xs text-muted-foreground">{stats.request_count.formatted.unit}</span>
                            </p>
                        </div>

                        <div className="flex items-center justify-between rounded-xl border border-border/70 bg-background/80 px-3 py-2">
                            <div className="flex items-center gap-3">
                                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                                    <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 8c-1.657 0-3 1.343-3 3s1.343 3 3 3 3-1.343 3-3-1.343-3-3-3zm0-6a9 9 0 1 1-9 9 9 9 0 0 1 9-9z" />
                                    </svg>
                                </div>
                                <p className="text-sm text-muted-foreground">总成本</p>
                            </div>
                            <p className="text-base font-semibold text-card-foreground">
                                {stats.total_cost.formatted.value}
                                <span className="ml-1 text-xs text-muted-foreground">{stats.total_cost.formatted.unit}</span>
                            </p>
                        </div>
                    </div>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-[600px] bg-card rounded-3xl p-8 custom-shadow max-h-[90vh] overflow-y-auto">
                    <CardContent channel={channel} stats={stats} />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
