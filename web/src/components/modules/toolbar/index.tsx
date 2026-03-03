'use client';

import { useState } from 'react';
import { ArrowDownUp, LayoutGrid, List, Plus, Search, SlidersHorizontal, X } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
} from '@/components/ui/morphing-dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useNavStore, type NavItem } from '@/components/modules/navbar';
import { CreateDialogContent as ChannelCreateContent } from '@/components/modules/channel/Create';
import { CreateDialogContent as GroupCreateContent } from '@/components/modules/group/Create';
import { CreateDialogContent as ModelCreateContent } from '@/components/modules/model/Create';
import { useSearchStore } from './search-store';
import {
    useToolbarViewOptionsStore,
    TOOLBAR_PAGES,
    type ToolbarPage,
    type ChannelFilter,
    type GroupFilter,
    type ModelFilter,
} from './view-options-store';

const CHANNEL_FILTER_OPTIONS: Array<{ value: ChannelFilter; label: string }> = [
    { value: 'all', label: 'All channels' },
    { value: 'enabled', label: 'Enabled only' },
    { value: 'disabled', label: 'Disabled only' },
];
const GROUP_FILTER_OPTIONS: Array<{ value: GroupFilter; label: string }> = [
    { value: 'all', label: 'All groups' },
    { value: 'with-members', label: 'With members' },
    { value: 'empty', label: 'Empty groups' },
];
const MODEL_FILTER_OPTIONS: Array<{ value: ModelFilter; label: string }> = [
    { value: 'all', label: 'All models' },
    { value: 'priced', label: 'Priced only' },
    { value: 'free', label: 'Free only' },
];

function isToolbarPage(item: NavItem): item is ToolbarPage {
    return (TOOLBAR_PAGES as readonly NavItem[]).includes(item);
}

function CreateDialogContent({ activeItem }: { activeItem: ToolbarPage }) {
    switch (activeItem) {
        case 'channel':
            return <ChannelCreateContent />;
        case 'group':
            return <GroupCreateContent />;
        case 'model':
            return <ModelCreateContent />;
    }
}

export function Toolbar() {
    const { activeItem } = useNavStore();
    const toolbarItem = isToolbarPage(activeItem) ? activeItem : null;
    const searchTerm = useSearchStore((s) => (toolbarItem ? s.searchTerms[toolbarItem] || '' : ''));
    const setSearchTerm = useSearchStore((s) => s.setSearchTerm);
    const layout = useToolbarViewOptionsStore((s) => (toolbarItem ? s.getLayout(toolbarItem) : 'grid'));
    const sortOrder = useToolbarViewOptionsStore((s) => (toolbarItem ? s.getSortOrder(toolbarItem) : 'asc'));
    const setLayout = useToolbarViewOptionsStore((s) => s.setLayout);
    const setSortOrder = useToolbarViewOptionsStore((s) => s.setSortOrder);
    const channelFilter = useToolbarViewOptionsStore((s) => s.channelFilter);
    const groupFilter = useToolbarViewOptionsStore((s) => s.groupFilter);
    const modelFilter = useToolbarViewOptionsStore((s) => s.modelFilter);
    const setChannelFilter = useToolbarViewOptionsStore((s) => s.setChannelFilter);
    const setGroupFilter = useToolbarViewOptionsStore((s) => s.setGroupFilter);
    const setModelFilter = useToolbarViewOptionsStore((s) => s.setModelFilter);
    const [expandedSearchItem, setExpandedSearchItem] = useState<ToolbarPage | null>(null);
    const searchExpanded = expandedSearchItem === toolbarItem;

    if (!toolbarItem) return null;

    const filterOptions = toolbarItem === 'channel'
        ? CHANNEL_FILTER_OPTIONS
        : toolbarItem === 'group'
            ? GROUP_FILTER_OPTIONS
            : MODEL_FILTER_OPTIONS;

    const activeFilter = toolbarItem === 'channel'
        ? channelFilter
        : toolbarItem === 'group'
            ? groupFilter
            : modelFilter;

    const handleFilterChange = (value: string) => {
        switch (toolbarItem) {
            case 'channel':
                setChannelFilter(value as ChannelFilter);
                break;
            case 'group':
                setGroupFilter(value as GroupFilter);
                break;
            case 'model':
                setModelFilter(value as ModelFilter);
                break;
        }
    };

    return (
        <AnimatePresence mode="wait">
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
                            onClick={() => setExpandedSearchItem(toolbarItem)}
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
                                onChange={(e) => setSearchTerm(toolbarItem, e.target.value)}
                                autoFocus
                                className="w-20 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                            />
                            <button
                                onClick={() => {
                                    setSearchTerm(toolbarItem, '');
                                    setExpandedSearchItem(null);
                                }}
                                className="p-0.5 rounded shrink-0 text-muted-foreground hover:text-foreground transition-colors"
                            >
                                <X className="size-3.5" />
                            </button>
                        </motion.div>
                    )}
                </div>

                <Popover>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            aria-label="View options"
                            className={buttonVariants({
                                variant: 'ghost',
                                size: 'icon',
                                className: 'rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground',
                            })}
                        >
                            <SlidersHorizontal className="size-4 transition-colors duration-300" />
                        </button>
                    </PopoverTrigger>
                    <PopoverContent
                        align="center"
                        side="bottom"
                        sideOffset={8}
                        className="w-64 rounded-2xl border border-border/60 bg-card p-3 shadow-xl"
                    >
                        <div className="grid gap-3">
                            <div className="grid gap-2">
                                <p className="text-xs font-medium text-muted-foreground">Layout</p>
                                <div className="grid grid-cols-2 gap-2">
                                    <button
                                        type="button"
                                        onClick={() => setLayout(toolbarItem, 'grid')}
                                        className={cn(
                                            'h-8 rounded-lg border text-xs font-medium inline-flex items-center justify-center gap-1.5 transition-colors',
                                            layout === 'grid'
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        <LayoutGrid className="size-3.5" />
                                        Grid
                                    </button>
                                    <button
                                        type="button"
                                        onClick={() => setLayout(toolbarItem, 'list')}
                                        className={cn(
                                            'h-8 rounded-lg border text-xs font-medium inline-flex items-center justify-center gap-1.5 transition-colors',
                                            layout === 'list'
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        <List className="size-3.5" />
                                        List
                                    </button>
                                </div>
                            </div>

                            <div className="grid gap-2">
                                <p className="text-xs font-medium text-muted-foreground">Sort</p>
                                <div className="grid grid-cols-2 gap-2">
                                    <button
                                        type="button"
                                        onClick={() => setSortOrder(toolbarItem, 'asc')}
                                        className={cn(
                                            'h-8 rounded-lg border text-xs font-medium inline-flex items-center justify-center gap-1.5 transition-colors',
                                            sortOrder === 'asc'
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        <ArrowDownUp className="size-3.5" />
                                        Asc
                                    </button>
                                    <button
                                        type="button"
                                        onClick={() => setSortOrder(toolbarItem, 'desc')}
                                        className={cn(
                                            'h-8 rounded-lg border text-xs font-medium inline-flex items-center justify-center gap-1.5 transition-colors',
                                            sortOrder === 'desc'
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        <ArrowDownUp className="size-3.5 rotate-180" />
                                        Desc
                                    </button>
                                </div>
                            </div>

                            <div className="grid gap-2">
                                <p className="text-xs font-medium text-muted-foreground">Filter</p>
                                <div className="grid gap-2">
                                    {filterOptions.map((option) => (
                                        <button
                                            key={option.value}
                                            type="button"
                                            onClick={() => handleFilterChange(option.value)}
                                            className={cn(
                                                'h-8 rounded-lg border px-2 text-xs font-medium text-left transition-colors',
                                                activeFilter === option.value
                                                    ? 'border-primary/30 bg-primary text-primary-foreground'
                                                    : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                            )}
                                        >
                                            {option.label}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        </div>
                    </PopoverContent>
                </Popover>

                {/* 创建按钮 */}
                <MorphingDialog>
                    <MorphingDialogTrigger className={buttonVariants({ variant: "ghost", size: "icon", className: "rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground" })}>
                        <Plus className="size-4 transition-colors duration-300" />
                    </MorphingDialogTrigger>

                    <MorphingDialogContainer>
                        <MorphingDialogContent className="w-fit max-w-full bg-card text-card-foreground px-6 py-4 rounded-3xl custom-shadow max-h-[calc(100vh-2rem)] flex flex-col overflow-hidden">
                            <CreateDialogContent activeItem={toolbarItem} />
                        </MorphingDialogContent>
                    </MorphingDialogContainer>
                </MorphingDialog>
            </motion.div>
        </AnimatePresence>
    );
}

export { useSearchStore } from './search-store';
export { useToolbarViewOptionsStore } from './view-options-store';
