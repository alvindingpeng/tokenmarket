'use client';

import { useModelList } from '@/api/endpoints/model';
import { PageWrapper } from '@/components/common/PageWrapper';
import { ModelItem } from './ModelItem';
import { ModelToolbar } from './ModelToolbar';
import { useState, useMemo } from 'react';

export function Model() {
    const { data: models } = useModelList();
    const [searchTerm, setSearchTerm] = useState('');

    const filteredModels = useMemo(() => {
        if (!models) return [];
        if (!searchTerm.trim()) return models;

        const term = searchTerm.toLowerCase();
        return models.filter(model =>
            model.name.toLowerCase().includes(term)
        );
    }, [models, searchTerm]);

    const sortedModels = useMemo(() => {
        return [...filteredModels].sort((a, b) => a.name.localeCompare(b.name));
    }, [filteredModels]);

    return (
        <PageWrapper className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <ModelToolbar
                key="model-toolbar"
                searchTerm={searchTerm}
                onSearchChange={setSearchTerm}
            />
            {sortedModels.map((model) => (
                <ModelItem key={"model-" + model.name} model={model} />
            ))}
        </PageWrapper>
    );
}
