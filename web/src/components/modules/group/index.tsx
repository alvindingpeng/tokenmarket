'use client';

import { useMemo } from 'react';
import { PageWrapper } from '@/components/common/PageWrapper';
import { GroupCard } from './Item';
import { useGroupList } from '@/api/endpoints/group';
import { useSearchStore } from '@/components/modules/toolbar';

export function Group() {
    const { data: groups } = useGroupList();
    const searchTerm = useSearchStore((s) => s.getSearchTerm('group'));

    const filteredGroups = useMemo(() => {
        if (!groups) return [];
        const sorted = [...groups].sort((a, b) => a.id! - b.id!);
        if (!searchTerm.trim()) return sorted;
        const term = searchTerm.toLowerCase();
        return sorted.filter((g) => g.name.toLowerCase().includes(term));
    }, [groups, searchTerm]);

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {filteredGroups.map((group) => (
                <GroupCard key={group.id} group={group} />
            ))}
        </PageWrapper>
    );
}
