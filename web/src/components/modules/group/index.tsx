'use client';

import { PageWrapper } from '@/components/common/PageWrapper';
import { CreateGroupButton } from './Create';
import { GroupCard } from './Item';
import { useGroupList } from '@/api/endpoints/group';
import { useTranslations } from 'next-intl';
import { AnimatePresence, motion } from 'motion/react';

export function Group() {
    const { data: groups = [] } = useGroupList();
    const t = useTranslations('group');

    return (
        <PageWrapper>
            <div className="flex items-center justify-between mb-4">
                <CreateGroupButton />
            </div>

            <div
                className="overflow-x-auto"
                style={{
                    '--card-width': '20rem',
                    '--card-height': '32rem',
                    '--shadow-space': '3rem',
                    margin: '0 calc(var(--shadow-space) * -1) calc(var(--shadow-space) * -1)',
                    minHeight: 'var(--card-height)',
                } as React.CSSProperties}
            >
                <div
                    className="grid grid-flow-col gap-4"
                    style={{
                        gridAutoColumns: 'var(--card-width)',
                        gridAutoRows: 'var(--card-height)',
                        padding: `0 var(--shadow-space) var(--shadow-space)`,
                    }}
                >
                    <AnimatePresence mode="popLayout">
                        {groups.length === 0 ? (
                            <motion.div
                                key="empty"
                                initial={{ opacity: 0 }}
                                animate={{ opacity: 1 }}
                                exit={{ opacity: 0 }}
                                className="flex items-center justify-center text-muted-foreground col-span-full"
                                style={{ height: 'var(--card-height)' }}
                            >
                                {t('empty')}
                            </motion.div>
                        ) : (
                            groups.sort((a, b) => a.id! - b.id!).map((group) => (
                                <motion.div
                                    key={group.id}
                                    layout
                                    initial={{ opacity: 0, scale: 0.8 }}
                                    animate={{ opacity: 1, scale: 1 }}
                                    exit={{ opacity: 0, scale: 0.8 }}
                                    transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                                >
                                    <GroupCard group={group} />
                                </motion.div>
                            ))
                        )}
                    </AnimatePresence>
                </div>
            </div>
        </PageWrapper>
    );
}
