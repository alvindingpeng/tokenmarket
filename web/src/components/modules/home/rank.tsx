'use client';

import { useChannelList } from '@/api/endpoints/channel';
import { useMemo } from 'react';
import { useTranslations } from 'next-intl';
import { TrendingUp } from 'lucide-react';
import { type Channel } from '@/api/endpoints/channel';
import { formatMoney } from '@/lib/utils';

export function Rank() {
    const { data: channelData } = useChannelList();
    const t = useTranslations('home.rank');

    const rankedChannels = useMemo<Channel[]>(() => {
        if (!channelData?.raw) return [];
        return channelData.raw.sort((a, b) => (b.stats.input_cost + b.stats.output_cost) - (a.stats.input_cost + a.stats.output_cost))
    }, [channelData]);

    const getMedalEmoji = (rank: number): string => {
        switch (rank) {
            case 1: return '🥇';
            case 2: return '🥈';
            case 3: return '🥉';
            default: return '';
        }
    };

    return (
        <div className="rounded-3xl bg-card border-card-border border text-card-foreground custom-shadow p-5 ">
            {rankedChannels.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            ) : (
                <div className="space-y-3 max-h-[300px] overflow-y-auto">
                    {rankedChannels.map((channel, index) => {
                        const rank = index + 1;
                        const medal = getMedalEmoji(rank);
                        const totalCost = channel.stats.input_cost + channel.stats.output_cost;
                        const formattedCost = formatMoney(totalCost);
                        return (
                            <div
                                key={channel.id}
                                className="flex items-center gap-3 p-3 rounded-2xl hover:bg-accent/5 transition-colors"
                            >
                                <div className="w-8 h-8 rounded-lg flex items-center justify-center font-bold text-lg shrink-0">
                                    {medal || rank}
                                </div>

                                <div className="flex-1 min-w-0">
                                    <p className="font-medium text-sm truncate">{channel.name}</p>
                                </div>

                                <div className="flex items-baseline gap-1 text-right shrink-0">
                                    <span className="font-semibold text-base">
                                        {formattedCost.value}
                                    </span>
                                    <span className="text-xs text-muted-foreground">
                                        {formattedCost.unit}
                                    </span>
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}