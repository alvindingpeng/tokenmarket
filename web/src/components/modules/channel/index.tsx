import { useMemo, useState } from 'react';
import { ArrowUpAZ, ShieldCheck, LayoutGrid } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStats } from '@/api/channel';
import { useAuth } from '@/api/user';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { MorphingDialogDescription } from '@/components/ui/morphing-dialog';
import { Card } from './Card';
import { ChannelForm } from './Form';
import { ChannelReviewQueue } from './Review';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// ChannelActions 向稳定顶栏提供渠道页面的搜索、视图选项和创建入口。
export function ChannelActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.channel || '');
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.channel === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.channelFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setLayout = usePageActionsStore((state) => state.setLayout);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setChannelFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('channel', value)}
            layout={layout}
            onLayoutChange={(value) => setLayout('channel', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('channel', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.channel.all') },
                { value: 'enabled', label: t('popover.filter.channel.enabled') },
                { value: 'disabled', label: t('popover.filter.channel.disabled') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'enabled' || value === 'disabled') setFilter(value);
            }}
        >
            <div className="w-screen max-w-full md:max-w-3xl flex flex-col">
                <MorphingDialogDescription disableLayoutAnimation>
                    <ChannelForm />
                </MorphingDialogDescription>
            </div>
        </PageActions>
    );
}

// Channel 渲染渠道列表正文; 顶部 Tab 在"渠道列表"与"审核队列"(P1)之间切换。
// 审核队列自带状态筛选与审批动作, 不进虚拟滚动; 两个视图都撑满剩余高度, 切换不改外层布局。
export function Channel() {
    const tReview = useTranslations('channel.review');
    // 审核队列仅管理员可见: 渠道商只在卡片查看审核状态(与审核意见), 不进队列、无任何审核功能。
    const { role } = useAuth();
    const isAdmin = role === 'admin';
    const [view, setView] = useState<'channels' | 'review'>('channels');
    const { data: statsData } = useChannelStats();
    const searchTerm = usePageActionsStore((state) => state.searchTerms.channel || '');
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.channel === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.channelFilter);

    // 先按搜索词和启用状态过滤, 再按名称排序
    const visibleChannels = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const matched = (statsData ?? []).filter((channel) => {
            if (term && !channel.channel_name.toLowerCase().includes(term)) return false;
            if (filter === 'enabled') return channel.enabled;
            if (filter === 'disabled') return !channel.enabled;
            return true;
        });

        return matched.sort((a, b) =>
            sortOrder === 'asc'
                ? a.channel_name.localeCompare(b.channel_name)
                : b.channel_name.localeCompare(a.channel_name)
        );
    }, [statsData, searchTerm, filter, sortOrder]);

    // Tab 条: 两态分段按钮, 与统计排序切换同一交互语义(aria-pressed + 高亮态)。
    const tabs: { key: 'channels' | 'review'; label: string; icon: React.ReactNode }[] = [
        { key: 'channels', label: tReview('tabChannels'), icon: <LayoutGrid className="size-3.5" /> },
        ...(isAdmin ? [{ key: 'review' as const, label: tReview('tabReview'), icon: <ShieldCheck className="size-3.5" /> }] : []),
    ];

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center gap-1">
                {tabs.map(({ key, label, icon }) => (
                    <button
                        key={key}
                        type="button"
                        onClick={() => setView(key)}
                        aria-pressed={view === key}
                        className={`flex min-h-8 items-center gap-1.5 rounded-xl px-3 text-xs transition-colors ${
                            view === key
                                ? 'bg-muted font-medium text-foreground'
                                : 'text-muted-foreground/60 hover:text-muted-foreground'}`}
                    >
                        {icon}
                        {label}
                    </button>
                ))}
            </div>
            {view === 'review' && isAdmin ? (
                <ChannelReviewQueue />
            ) : (
                <VirtualizedGrid
                    items={visibleChannels}
                    layout={layout}
                    columns={{ default: 1, sm: 2, md: 2, lg: 4, xl: 5, '2xl': 6 }}
                    estimateItemHeight={232}
                    getItemKey={(channel) => `channel-${channel.channel_id}`}
                    renderItem={(channel) => (
                        <Card channel={channel} />
                    )}
                />
            )}
        </div>
    );
}
