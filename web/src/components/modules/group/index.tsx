import { useMemo } from 'react';
import { ArrowUpAZ, Filter } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { GroupCard } from './Card';
import { ModelRuleEditor } from '@/components/modules/model/RuleEditor';
import { CreateDialogContent } from './Create';
import { useRuntimeClock } from './MemberStatus';
import { useGroupList } from '@/api/group';
import { useModelAutoAddRules } from '@/api/model-rule';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// GroupActions 向稳定顶栏提供分组页面的搜索、视图选项和创建入口。
export function GroupActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setGroupFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('group', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('group', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.group.all') },
                { value: 'with-members', label: t('popover.filter.group.withMembers') },
                { value: 'empty', label: t('popover.filter.group.empty') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'with-members' || value === 'empty') setFilter(value);
            }}
        >
            <ModelRuleEditor
                nameEntry
                rule={undefined}
                trigger={
                    <button
                        type="button"
                        title={t('autoAddRule')}
                        className="h-9 px-3 flex items-center gap-1.5 rounded-lg text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    >
                        <Filter className="size-4" />
                        <span className="hidden md:inline">{t('autoAddRule')}</span>
                    </button>
                }
            />
            <CreateDialogContent />
        </PageActions>
    );
}

// Group 渲染分组列表正文。
export function Group() {
    const t = useTranslations('group');
    const { data: groups } = useGroupList(true, true);
    // 规则表在页面层取一次下发给每张卡片: 卡片数量多, 各自 useQuery 会白白多出几十个订阅。
    const { data: autoAddRules } = useModelAutoAddRules();
    const runtimeNow = useRuntimeClock(groups);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);

    const sortedGroups = useMemo(() => {
        if (!groups) return [];
        return [...groups].sort((a, b) =>
            sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)
        );
    }, [groups, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const byName = !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));

        if (filter === 'with-members') return byName.filter((g) => (g.items?.length || 0) > 0);
        if (filter === 'empty') return byName.filter((g) => (g.items?.length || 0) === 0);

        return byName;
    }, [sortedGroups, searchTerm, filter]);

    return (
        // 外层必须自己占满高度并把剩余空间让给列表: 页面容器是 absolute inset-0 + overflow-hidden,
        // 这里若只是普通 flex 列, 说明行会把容器撑高, VirtualizedGrid 的 h-full 随之按撑高的高度解析,
        // 结果它自己的滚动区没有可滚内容(h == scrollHeight), 页面被整体裁掉 —— 一页滑不动。
        <div className="flex h-full min-h-0 flex-col gap-3">
            {/* 系统设置里那组条件只是全局默认; 想给单个模型另设一份, 入口在这里和顶栏漏斗。 */}
            <p className="shrink-0 rounded-xl border border-border/50 bg-muted/30 px-3 py-2 text-xs leading-relaxed text-muted-foreground">
                {t('ruleHint')}
            </p>

            <div className="min-h-0 flex-1">
            <VirtualizedGrid
            items={visibleGroups}
            // 一行 2 卡: 分组卡片里带成员矩阵与运行态, 三列会把每张卡压到 400px 上下,
            // 成员行被迫折行到看不清归属。两列给足宽度后信息才排得开。
            columns={{ default: 1, md: 2, lg: 2 }}
            estimateItemHeight={520}
            getItemKey={(group) => group.id}
            renderItem={(group) => {
                let deadline = group.runtime.affinity_until;
                for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
                    deadline = Math.max(deadline, cooldownUntil);
                }
                return (
                    <GroupCard
                        group={group}
                        now={deadline > runtimeNow ? runtimeNow : deadline}
                        rules={autoAddRules}
                    />
                );
            }}
            />
            </div>
        </div>
    );
}
