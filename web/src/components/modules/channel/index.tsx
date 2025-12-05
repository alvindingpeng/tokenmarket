'use client';

import { useMemo } from 'react';
import { useChannelList } from '@/api/endpoints/channel';
import { PageWrapper } from '@/components/common/PageWrapper';
import { Card } from './Card';
import { useSearchStore } from '@/components/modules/toolbar';

export function Channel() {
    const { data: channelsData } = useChannelList();
    const searchTerm = useSearchStore((s) => s.getSearchTerm('channel'));

    const filteredChannels = useMemo(() => {
        if (!channelsData) return [];
        const sorted = [...channelsData].sort((a, b) => a.raw.id - b.raw.id);
        if (!searchTerm.trim()) return sorted;
        const term = searchTerm.toLowerCase();
        return sorted.filter((c) => c.raw.name.toLowerCase().includes(term));
    }, [channelsData, searchTerm]);

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            {filteredChannels.map((channel) => (
                <Card key={"channel-" + channel.raw.id} channel={channel.raw} stats={channel.formatted} />
            ))}
        </PageWrapper>
    );
}
