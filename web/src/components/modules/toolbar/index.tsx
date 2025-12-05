'use client';

import { useState, useEffect } from 'react';
import { Plus, Search, X } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
} from '@/components/ui/morphing-dialog';
import { buttonVariants } from '@/components/ui/button';
import { useNavStore, type NavItem } from '@/components/modules/navbar';
import { CreateDialogContent as ChannelCreateContent } from '@/components/modules/channel/Create';
import { CreateDialogContent as GroupCreateContent } from '@/components/modules/group/Create';
import { CreateDialogContent as ModelCreateContent } from '@/components/modules/model/Create';
import { useSearchStore } from './search-store';

const TOOLBAR_PAGES: NavItem[] = ['channel', 'group', 'model'];

function CreateDialogContent({ activeItem }: { activeItem: NavItem }) {
    switch (activeItem) {
        case 'channel':
            return <ChannelCreateContent />;
        case 'group':
            return <GroupCreateContent />;
        case 'model':
            return <ModelCreateContent />;
        default:
            return null;
    }
}

export function Toolbar() {
    const { activeItem } = useNavStore();
    const searchTerm = useSearchStore((s) => s.searchTerms[activeItem] || '');
    const setSearchTerm = useSearchStore((s) => s.setSearchTerm);
    const [searchExpanded, setSearchExpanded] = useState(false);

    useEffect(() => {
        queueMicrotask(() => setSearchExpanded(false));
    }, [activeItem]);

    const showToolbar = TOOLBAR_PAGES.includes(activeItem);

    return (
        <AnimatePresence mode="wait">
            {showToolbar && (
                <motion.div
                    key="toolbar"
                    initial={{ opacity: 0, scale: 0.9 }}
                    animate={{ opacity: 1, scale: 1 }}
                    exit={{ opacity: 0, scale: 0.9 }}
                    transition={{ duration: 0.2 }}
                    className="flex items-center gap-2"
                >
                    {/* 搜索按钮/展开框 */}
                    <div className="relative h-9 w-9">
                        {!searchExpanded ? (
                            <motion.button
                                layoutId="search-box"
                                onClick={() => setSearchExpanded(true)}
                                className={buttonVariants({ variant: "ghost", size: "icon", className: "absolute inset-0 rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground" })}
                            >
                                <motion.span layout="position"><Search className="size-4 transition-colors duration-300" /></motion.span>
                            </motion.button>
                        ) : (
                            <motion.div
                                layoutId="search-box"
                                className="absolute right-0 top-0 flex items-center gap-2 h-9 px-3 rounded-xl border"
                                transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                            >
                                <motion.span layout="position"><Search className="size-4 text-muted-foreground shrink-0" /></motion.span>
                                <input
                                    type="text"
                                    value={searchTerm}
                                    onChange={(e) => setSearchTerm(activeItem, e.target.value)}
                                    autoFocus
                                    className="w-20 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                                />
                                <button
                                    onClick={() => {
                                        setSearchTerm(activeItem, '');
                                        setSearchExpanded(false);
                                    }}
                                    className="p-0.5 rounded shrink-0 text-muted-foreground hover:text-foreground transition-colors"
                                >
                                    <X className="size-3.5" />
                                </button>
                            </motion.div>
                        )}
                    </div>

                    {/* 创建按钮 */}
                    <MorphingDialog>
                        <MorphingDialogTrigger className={buttonVariants({ variant: "ghost", size: "icon", className: "rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground" })}>
                            <Plus className="size-4 transition-colors duration-300" />
                        </MorphingDialogTrigger>

                        <MorphingDialogContainer>
                            <MorphingDialogContent className="w-full max-w-xl bg-card text-card-foreground px-6 py-4 rounded-3xl custom-shadow max-h-[90vh] overflow-y-auto">
                                <CreateDialogContent activeItem={activeItem} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>
                </motion.div>
            )}
        </AnimatePresence>
    );
}

export { useSearchStore } from './search-store';
