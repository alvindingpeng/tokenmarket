'use client';

import { useChannelList } from '@/api/endpoints/channel';
import { PageWrapper } from '@/components/common/PageWrapper';
import { CreateCard } from './Create';
import { Card } from './Card';

export function Channel() {
    const { data: channelsData } = useChannelList();

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            <CreateCard key="channel-create" />
            {channelsData?.sort((a, b) => a.raw.id - b.raw.id).map((channel) => (
                <Card key={"channel-" + channel.raw.id} channel={channel.raw} stats={channel.formatted} />
            ))}
        </PageWrapper>
    );
}
