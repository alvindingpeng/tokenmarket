import { useChannelList } from '@/api/channel';
import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { TrendingUp } from 'lucide-react';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';
import { useHomeViewStore, type RankScope, type RankSortMode } from '@/components/modules/home/store';
import type { StatsMetricsFormatted } from '@/api/stats';

interface RankData {
    id: string;
    name: string;
    channelName?: string;
    formatted: StatsMetricsFormatted;
}

export function Rank() {
    const { data: channelData } = useChannelList();
    const t = useTranslations('home.rank');
    const rankSortMode = useHomeViewStore((state) => state.rankSortMode);
    const setRankSortMode = useHomeViewStore((state) => state.setRankSortMode);
    const rankScope = useHomeViewStore((state) => state.rankScope);
    const setRankScope = useHomeViewStore((state) => state.setRankScope);

    const rankData = useMemo<RankData[]>(() => {
        if (!channelData) return [];
        if (rankScope === 'channel') {
            return channelData.map((channel) => ({
                id: `channel-${channel.raw.id}`,
                name: channel.raw.name,
                formatted: channel.formatted,
            }));
        }
        return channelData.flatMap((channel) => channel.raw.models.map((model) => ({
            id: `model-${model.id}`,
            name: model.name,
            channelName: channel.raw.name,
            formatted: {
                input_token: formatCount(model.input_token),
                output_token: formatCount(model.output_token),
                total_token: formatCount(model.input_token + model.output_token),
                input_cost: formatMoney(model.input_cost),
                output_cost: formatMoney(model.output_cost),
                total_cost: formatMoney(model.input_cost + model.output_cost),
                wait_time: formatTime(model.wait_time),
                request_success: formatCount(model.request_success),
                request_failed: formatCount(model.request_failed),
                request_count: formatCount(model.request_success + model.request_failed),
            },
        })));
    }, [channelData, rankScope]);

    const rankedByCost = useMemo<RankData[]>(() => {
        return [...rankData].sort((a, b) => b.formatted.total_cost.raw - a.formatted.total_cost.raw);
    }, [rankData]);

    const rankedByCount = useMemo<RankData[]>(() => {
        return [...rankData].sort((a, b) => b.formatted.request_count.raw - a.formatted.request_count.raw);
    }, [rankData]);

    const rankedByTokens = useMemo<RankData[]>(() => {
        return [...rankData].sort((a, b) => b.formatted.total_token.raw - a.formatted.total_token.raw);
    }, [rankData]);

    const renderList = (items: RankData[], mode: RankSortMode) => {
        if (items.length === 0) {
            return (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            );
        }
        return (
            <div className="space-y-3 max-h-[300px] overflow-y-auto">
                {items.map((item, index) => {
                    const rank = index + 1;

                    return (
                        <div
                            key={item.id}
                            className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3"
                        >
                            <div className="flex items-center justify-center font-bold text-lg">
                                {rank}
                            </div>

                            <div className="min-w-0">
                                <p className="font-medium text-sm truncate">{item.name}</p>
                                {item.channelName && (
                                    <p className="mt-1 truncate text-xs text-muted-foreground">{item.channelName}</p>
                                )}
                                {mode === 'count' && (() => {
                                    const successCount = item.formatted.request_success.raw;
                                    const failedCount = item.formatted.request_failed.raw;
                                    const totalCount = successCount + failedCount;
                                    const successRate = totalCount > 0 ? (successCount / totalCount) * 100 : 0;

                                    return (
                                        <div className="flex items-center gap-1 text-xs text-muted-foreground mt-1">
                                            <span>{t('successRate')}:</span>
                                            <span>{successRate.toFixed(1)}%</span>
                                        </div>
                                    );
                                })()}
                            </div>

                            <div className="flex items-center gap-1 text-right">
                                {mode === 'count' ? (
                                    <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
                                        <span className="text-accent">
                                            {item.formatted.request_success.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted.request_success.formatted.unit}
                                            </span>
                                        </span>
                                        <span className="text-muted-foreground/40 font-light">/</span>
                                        <span className="text-destructive">
                                            {item.formatted.request_failed.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted.request_failed.formatted.unit}
                                            </span>
                                        </span>
                                    </div>
                                ) : mode === 'tokens' ? (
                                    <span className="font-semibold text-base">
                                        {item.formatted.total_token.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {item.formatted.total_token.formatted.unit}
                                        </span>
                                    </span>
                                ) : (
                                    <span className="font-semibold text-base">
                                        {item.formatted.total_cost.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {item.formatted.total_cost.formatted.unit}
                                        </span>
                                    </span>
                                )}
                            </div>
                        </div>
                    );
                })}
            </div>
        );
    };

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <Tabs value={rankSortMode} onValueChange={(value) => setRankSortMode(value as RankSortMode)}>
                <div className="flex items-center justify-between">
                    <Tabs value={rankScope} onValueChange={(value) => setRankScope(value as RankScope)}>
                        <TabsList variant="text" className="p-0">
                            <TabsTrigger value="channel" className="pr-0">{t('channel')}</TabsTrigger>
                            <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                            <TabsTrigger value="model" className="pl-0">{t('model')}</TabsTrigger>
                        </TabsList>
                    </Tabs>
                    <TabsList variant="text" className="p-0">
                        <TabsTrigger value="cost" className="pr-0">{t('sortByCost')}</TabsTrigger>
                        <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                        <TabsTrigger value="count" className="px-0">{t('sortByCount')}</TabsTrigger>
                        <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                        <TabsTrigger value="tokens" className="pl-0">{t('sortByTokens')}</TabsTrigger>
                    </TabsList>
                </div>
                <TabsContent value="cost">
                    {renderList(rankedByCost, 'cost')}
                </TabsContent>
                <TabsContent value="count">
                    {renderList(rankedByCount, 'count')}
                </TabsContent>
                <TabsContent value="tokens">
                    {renderList(rankedByTokens, 'tokens')}
                </TabsContent>
            </Tabs>
        </div>
    );
}
