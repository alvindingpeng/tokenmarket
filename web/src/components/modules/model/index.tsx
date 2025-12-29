'use client';

import { useEffect, useMemo } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { useModelList } from '@/api/endpoints/model';
import { ModelItem } from './Item';
import { usePaginationStore, useSearchStore } from '@/components/modules/toolbar';
import { EASING } from '@/lib/animations/fluid-transitions';
import { useGridPageSize } from '@/hooks/use-grid-page-size';

/** Model item height: h-28 = 112px */
const MODEL_ITEM_HEIGHT = 112;

export function Model() {
    const { data: models } = useModelList();
    const pageKey = 'model' as const;
    const pageSize = useGridPageSize({
        itemHeight: MODEL_ITEM_HEIGHT,
        gap: 16,
        columns: { default: 1, md: 2, xl: 3 },
    });
    const searchTerm = useSearchStore((s) => s.getSearchTerm(pageKey));
    const page = usePaginationStore((s) => s.getPage(pageKey));
    const setPage = usePaginationStore((s) => s.setPage);
    const setTotalItems = usePaginationStore((s) => s.setTotalItems);
    const setPageSize = usePaginationStore((s) => s.setPageSize);
    const direction = usePaginationStore((s) => s.getDirection(pageKey));

    const filteredModels = useMemo(() => {
        if (!models) return [];
        const sortedModels = [...models].sort((a, b) => a.name.localeCompare(b.name));
        if (!searchTerm.trim()) return sortedModels;
        const term = searchTerm.toLowerCase();
        return sortedModels.filter((m) => m.name.toLowerCase().includes(term));
    }, [models, searchTerm]);

    // Sync to store for Toolbar to display pagination info
    useEffect(() => {
        setTotalItems(pageKey, filteredModels.length);
        setPageSize(pageKey, pageSize);
    }, [filteredModels.length, pageSize, pageKey, setTotalItems, setPageSize]);

    // Reset to page 1 when search term changes
    useEffect(() => {
        setPage(pageKey, 1);
    }, [searchTerm, pageKey, setPage]);

    const pagedModels = useMemo(() => {
        const start = (page - 1) * pageSize;
        return filteredModels.slice(start, start + pageSize);
    }, [filteredModels, page, pageSize]);

    return (
        <AnimatePresence mode="popLayout" initial={false} custom={direction}>
            <motion.div
                key={`model-page-${page}`}
                custom={direction}
                variants={{
                    enter: (d: number) => ({ x: d >= 0 ? 24 : -24, opacity: 0 }),
                    center: { x: 0, opacity: 1 },
                    exit: (d: number) => ({ x: d >= 0 ? -24 : 24, opacity: 0 }),
                }}
                initial="enter"
                animate="center"
                exit="exit"
                transition={{ duration: 0.25, ease: EASING.easeOutExpo }}
            >
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                    <AnimatePresence mode="popLayout">
                        {pagedModels.map((model, index) => (
                            <motion.div
                                key={"model-" + model.name}
                                initial={{ opacity: 0, y: 20 }}
                                animate={{ opacity: 1, y: 0 }}
                                exit={{
                                    opacity: 0,
                                    scale: 0.95,
                                    transition: { duration: 0.2 }
                                }}
                                transition={{
                                    duration: 0.45,
                                    ease: EASING.easeOutExpo,
                                    delay: index === 0 ? 0 : Math.min(0.08 * Math.log2(index + 1), 0.4),
                                }}
                                layout={!searchTerm.trim()}
                            >
                                <ModelItem model={model} />
                            </motion.div>
                        ))}
                    </AnimatePresence>
                </div>
            </motion.div>
        </AnimatePresence>
    );
}
