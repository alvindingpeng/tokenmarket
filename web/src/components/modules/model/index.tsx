'use client';

import { useMemo } from 'react';
import { useModelList } from '@/api/endpoints/model';
import { PageWrapper } from '@/components/common/PageWrapper';
import { ModelItem } from './Item';
import { useSearchStore } from '@/components/modules/toolbar';

export function Model() {
    const { data: models } = useModelList();
    const searchTerm = useSearchStore((s) => s.getSearchTerm('model'));

    const filteredModels = useMemo(() => {
        if (!models) return [];
        const sorted = [...models].sort((a, b) => a.name.localeCompare(b.name));
        if (!searchTerm.trim()) return sorted;
        const term = searchTerm.toLowerCase();
        return sorted.filter((m) => m.name.toLowerCase().includes(term));
    }, [models, searchTerm]);

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-3 gap-4">
            {filteredModels.map((model) => (
                <ModelItem key={"model-" + model.name} model={model} />
            ))}
        </PageWrapper>
    );
}
