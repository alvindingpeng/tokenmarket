'use client';

import { useAPIKeyList } from '@/api/endpoints/apikey';
import { PageWrapper } from '@/components/common/PageWrapper';
import { CreateCard } from './Create';
import { KeyItem } from './KeyItem';

export function Key() {
    const { data: apiKeys } = useAPIKeyList();

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
            <CreateCard key="apikey-create" />
            {apiKeys?.sort((a, b) => a.id - b.id).map((apiKey) => (
                <KeyItem key={"apikey-" + apiKey.id} apiKey={apiKey} />
            ))}
        </PageWrapper>
    );
}
