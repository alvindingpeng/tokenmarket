import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { ArrowRight, Check, Loader2, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useAddGroupItems, useGroupList, type Group } from '@/api/group';
import type { MarketChannelListing, MarketModel } from '@/api/market';
import { useAppStore } from '@/stores/app';
import { cn } from '@/lib/utils';

/**
 * AddToGroup 让用户把该模型下的可用挂牌加进自己已有的模型配置(分组)。
 *
 * 写路径用专门为"追加"设计的 POST /api/v1/group/add-items, 而不是 /update 的 items 整体替换。
 * 原因是并发安全: 整体替换要求调用方先持有准确的当前集合, 而广场的卡片数据来自轮询缓存 ——
 * 用户在弹层里停留期间, 另一个标签页或另一台设备可能刚改过同一个分组。按旧快照合并再提交,
 * 对方刚加的成员会被整段盖掉, 且没有任何报错。追加语义把差集计算放到后端事务里, 从根上消除这个窗口。
 *
 * 只做"加成员": 不新建分组、不改模式、不改 Relay 配置 —— 那是分组页的活。
 */
export function AddToGroup({ model }: { model: MarketModel }) {
    const t = useTranslations('market');
    // SSE 打开: 加入后分组页与这里的"已加入"角标都能自动跟上, 不必手动刷新。
    const { data: groups } = useGroupList(true, true);
    // 只有可用渠道能加: 不可用的挂牌加进去也选不到路, 徒增一条灰成员。
    const addable = model.channels.filter((listing) => listing.available);
    const ownedGroups = groups ?? [];

    // 一条可用渠道都没有时不给入口: 按钮点了也加不进任何东西, 那是死路。
    if (addable.length === 0) return null;

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">
                {/* 入口按钮不再"全加入后禁用": 追加语义下"全加过"仍可打开面板复核(重复提交是幂等的),
                    禁用只会让用户以为出故障。按钮文案统一, 已加入状态由卡片上的角标表达。 */}
                <Button size="sm" variant="outline" className="rounded-xl">
                    <Plus className="size-3.5" />
                    {t('action.addToGroup')}
                </Button>
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                {/* 面板整体挂在 Container 下, 每次打开都是一次新挂载: 默认勾选在挂载时算一次即可,
                    不必用 effect 追踪 dialog 开合(那会多触发一轮渲染)。 */}
                <Panel model={model} addable={addable} ownedGroups={ownedGroups} />
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

/** AddPreviewRow 是一个分组在本次提交中的预览: 会新增哪些、跳过多少(已存在)。 */
export interface AddPreviewRow {
    group: Group;
    toAdd: number[];
    skipped: number;
}

/**
 * addPreview 计算"本次提交会在每个分组各新增几条、跳过几条"。
 *
 * 抽成导出的纯函数而不留在组件里, 是为了能被直接测试: 面板挂在弹层里, 弹层关闭时不渲染,
 * 所以 SSR 断言不到预览行 —— 但那恰恰是用户点提交前唯一的确认依据, 必须有测试盯着。
 *
 * 注意这里算出的差集只用于展示。真正落库时后端会在事务内重新读当前成员再算一次,
 * 所以即便这里的预览基于旧快照, 也不会写错数据(这正是换成只追加接口的原因)。
 * 数量按去重后的授权计: 同一授权在请求里重复出现时后端只加一次, 预览也必须这样算, 否则
 * 界面会承诺"加 3 条"而实际只加 1 条。
 */
export function addPreview(groups: Group[], selectedGroupIds: number[], selectedGrantIds: number[]): AddPreviewRow[] {
    const uniqueGrants = Array.from(new Set(selectedGrantIds));
    return selectedGroupIds
        .map((groupId) => groups.find((group) => group.id === groupId))
        .filter((group): group is Group => Boolean(group))
        .map((group) => {
            const existing = new Set(group.items.map((item) => item.channel_grant_id));
            const toAdd = uniqueGrants.filter((grantId) => !existing.has(grantId));
            return { group, toAdd, skipped: uniqueGrants.length - toAdd.length };
        });
}

/** groupHasListing 判断某分组里是否已有这条挂牌(按授权主键)。 */
function groupHasListing(group: Group, listing: MarketChannelListing): boolean {
    return group.items.some((item) => item.channel_grant_id === listing.channel_grant_id);
}

/** Panel 是加入面板: 选分组(可多选)、选渠道(可多选)、提交前预览、提交后逐条展示结果。 */
function Panel({ model, addable, ownedGroups }: { model: MarketModel; addable: MarketChannelListing[]; ownedGroups: Group[] }) {
    const t = useTranslations('market');
    const addItems = useAddGroupItems();
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    // 加入成功后自行关掉弹层: 保留弹层会停在"已在其中"的状态上, 用户以为没提交成功。
    const { setIsOpen } = useMorphingDialog();

    // 默认**不勾选任何分组**, 只勾渠道。
    //
    // 这里刻意不预勾分组: 生产上有 20 多个模型配置, 预勾"还没加过的全部"会让一次点击把同一个模型
    // 塞进十几二十个配置, 而用户往往只想加到其中一两个。误操作的代价远大于省下的那几次点击 ——
    // 尤其是这些配置已经在跑线上流量, 加成员会立刻改变选路结果。
    // 渠道默认勾上"还没在任何配置里出现过"的那些, 因为它们必然要加; 已有的默认不勾。
    const [selectedGroupIds, setSelectedGroupIds] = useState<number[]>([]);
    const [selectedGrantIds, setSelectedGrantIds] = useState<number[]>(() => {
        const pending = addable.filter((listing) =>
            ownedGroups.every((group) => !groupHasListing(group, listing))
        );
        return (pending.length > 0 ? pending : addable).map((listing) => listing.channel_grant_id);
    });

    // 预览按"分组 × 渠道"逐格算出新增/已存在, 让用户在点提交前就能看清会加多少条。
    // 这一步只用于展示, 真正的差集由后端在事务内重算 —— 这里算错了也不会写错数据。
    const preview = useMemo(
        () => addPreview(ownedGroups, selectedGroupIds, selectedGrantIds),
        [selectedGroupIds, selectedGrantIds, ownedGroups]
    );

    const totalAdd = preview.reduce((sum, row) => sum + row.toAdd.length, 0);

    // sortedGroups 按"这个模型在该配置里还缺几条渠道"降序: 缺得多的排前面。
    // 只是展示顺序, 不影响提交内容; 让用户不必在二十多行里逐个比对哪条还没配过。
    const sortedGroups = useMemo(
        () => [...ownedGroups].sort((a, b) => missingCount(b) - missingCount(a)),
        [ownedGroups]
    );
    function missingCount(group: Group): number {
        return addable.filter((listing) => !groupHasListing(group, listing)).length;
    }
    // missingGroups 是"还没配齐这个模型"的配置, 供"一键选上"使用 —— 与列表排序同源。
    const missingGroups = sortedGroups.filter((group) => missingCount(group) > 0);

    const toggleGroup = (groupId: number) => {
        setSelectedGroupIds((current) =>
            current.includes(groupId) ? current.filter((id) => id !== groupId) : [...current, groupId]
        );
    };
    const toggleGrant = (grantId: number) => {
        setSelectedGrantIds((current) =>
            current.includes(grantId) ? current.filter((id) => id !== grantId) : [...current, grantId]
        );
    };

    const submit = () => {
        if (selectedGroupIds.length === 0 || selectedGrantIds.length === 0) return;
        addItems.mutate(
            { group_ids: selectedGroupIds, channel_grant_ids: selectedGrantIds },
            {
                onSuccess: (results) => {
                    const added = results.reduce((sum, result) => sum + result.added.length, 0);
                    const skipped = results.reduce((sum, result) => sum + result.skipped.length, 0);
                    if (added === 0) {
                        // 全部已存在: 这不是失败, 如实说"都已经在里面了", 免得用户以为没生效。
                        toast.success(t('toast.alreadyPresent'), { description: t('toast.alreadyPresentHint') });
                    } else if (skipped > 0) {
                        toast.success(t('toast.addedPartial', { count: added, skipped }), {
                            description: t('toast.addedPartialHint'),
                        });
                    } else {
                        toast.success(t('toast.added', { count: added, groups: results.length }));
                    }
                    setIsOpen(false);
                },
                // 业务失败(404/409)带 message; 网络失败也要说清, 否则用户只会重复点。
                onError: (error) => toast.error(t('toast.addFailed'), { description: error.message }),
            }
        );
    };

    return (
        <MorphingDialogContent className="w-[min(92vw,36rem)] rounded-3xl p-6">
            <MorphingDialogTitle className="text-lg font-semibold">{t('dialog.title')}</MorphingDialogTitle>
            <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                {ownedGroups.length === 0 ? t('dialog.noGroup') : t('dialog.description', { name: model.name })}
            </MorphingDialogDescription>

            {/* 没有任何模型配置时, 面板不该是死路: 直接给出创建入口, 让用户一步走到分组页。 */}
            {ownedGroups.length === 0 ? (
                <Button
                    className="mt-5 w-full rounded-xl"
                    onClick={() => {
                        setIsOpen(false);
                        setCurrentPage('group');
                    }}
                >
                    {t('dialog.createGroup')}
                    <ArrowRight className="size-4" />
                </Button>
            ) : (
                <div className="mt-4 flex max-h-[60vh] flex-col gap-4 overflow-y-auto pr-1">
                    <section className="flex flex-col gap-2">
                        <div className="flex items-center justify-between gap-2">
                            <span className="text-xs font-medium text-muted-foreground">{t('dialog.pickGroup')}</span>
                            <div className="flex items-center gap-2">
                                {/* 默认不勾选, 故必须给"一键选上还没配过的"这条路:
                                    二十多个配置逐个点太累, 但默认全选又会一次误加进全部配置。
                                    两个极端都不对, 折中是默认不选 + 显式快捷选择。 */}
                                {missingGroups.length > 0 && (
                                    <button
                                        type="button"
                                        onClick={() => setSelectedGroupIds(missingGroups.map((group) => group.id))}
                                        className="text-[11px] text-primary transition-colors hover:text-primary/80"
                                    >
                                        {t('dialog.selectMissing', { count: missingGroups.length })}
                                    </button>
                                )}
                                {selectedGroupIds.length > 0 && (
                                    <button
                                        type="button"
                                        onClick={() => setSelectedGroupIds([])}
                                        className="text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                                    >
                                        {t('dialog.clearGroups')}
                                    </button>
                                )}
                                <span className="text-[11px] text-muted-foreground">
                                    {t('dialog.selectedGroups', { count: selectedGroupIds.length })}
                                </span>
                            </div>
                        </div>
                        <div className="flex flex-col gap-1">
                            {/* 按"可加条数"降序: 还没配过这个模型的配置排前面, 用户不必在二十多行里找。 */}
                            {sortedGroups.map((group) => {
                                const joined = addable.every((listing) => groupHasListing(group, listing));
                                const checked = selectedGroupIds.includes(group.id);
                                return (
                                    <label
                                        key={group.id}
                                        className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-muted"
                                    >
                                        <Checkbox
                                            checked={checked}
                                            onCheckedChange={() => toggleGroup(group.id)}
                                        />
                                        <span className="truncate font-medium">{group.name}</span>
                                        <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
                                            {t('dialog.groupMeta', { count: group.items.length, mode: t('dialog.mode.' + group.mode) })}
                                        </span>
                                        {joined && (
                                            <span className="shrink-0 rounded bg-background px-1 py-0.5 text-[10px] text-muted-foreground">
                                                {t('dialog.alreadyIn')}
                                            </span>
                                        )}
                                    </label>
                                );
                            })}
                        </div>
                    </section>

                    <section className="flex flex-col gap-2">
                        <div className="flex items-center justify-between">
                            <span className="text-xs font-medium text-muted-foreground">{t('dialog.pickChannel')}</span>
                            <span className="text-[11px] text-muted-foreground">
                                {t('dialog.selectedChannels', { count: selectedGrantIds.length })}
                            </span>
                        </div>
                        <div className="flex flex-col gap-1">
                            {addable.map((listing) => {
                                const checked = selectedGrantIds.includes(listing.channel_grant_id);
                                return (
                                    <label
                                        key={listing.channel_grant_id}
                                        className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-muted"
                                    >
                                        <Checkbox
                                            checked={checked}
                                            onCheckedChange={() => toggleGrant(listing.channel_grant_id)}
                                        />
                                        <span className="font-mono">{listing.channel_code}</span>
                                        {/* 已在该分组的渠道逐行标注, 让用户知道为什么"选了却不会新增"。 */}
                                        {selectedGroupIds.some((groupId) => {
                                            const group = ownedGroups.find((item) => item.id === groupId);
                                            return group ? groupHasListing(group, listing) : false;
                                        }) && (
                                            <span className="ml-auto text-[11px] text-muted-foreground">{t('dialog.alreadyIn')}</span>
                                        )}
                                    </label>
                                );
                            })}
                        </div>
                    </section>

                    {/* 提交预览: 只说"会新增几条、跳过几条", 不承诺顺序 —— 具体位置由后端追加到末尾。 */}
                    <section className="rounded-xl bg-muted/40 px-3 py-2 text-xs">
                        {selectedGroupIds.length === 0 || selectedGrantIds.length === 0 ? (
                            <span className="text-muted-foreground">{t('dialog.previewEmpty')}</span>
                        ) : (
                            <div className="flex flex-col gap-1">
                                {preview.map((row) => (
                                    <div key={row.group.id} className="flex items-center gap-2">
                                        <span className="truncate font-medium">{row.group.name}</span>
                                        <span className="ml-auto shrink-0 text-muted-foreground">
                                            {row.toAdd.length > 0
                                                ? t('dialog.previewRow', { count: row.toAdd.length, skipped: row.skipped })
                                                : t('dialog.previewAllPresent')}
                                        </span>
                                        {row.toAdd.length > 0 && <Check className="size-3.5 shrink-0 text-primary" />}
                                    </div>
                                ))}
                            </div>
                        )}
                    </section>
                </div>
            )}

            <div className="mt-6 flex items-center justify-end gap-2">
                <MorphingDialogClose className="static rounded-xl px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
                    {t('dialog.cancel')}
                </MorphingDialogClose>
                {ownedGroups.length > 0 && (
                    <Button
                        size="sm"
                        className="rounded-xl"
                        disabled={totalAdd === 0 || addItems.isPending}
                        onClick={submit}
                    >
                        {addItems.isPending && <Loader2 className="size-3.5 animate-spin" />}
                        {totalAdd > 0 ? t('dialog.confirmCount', { count: totalAdd }) : t('dialog.confirm')}
                    </Button>
                )}
            </div>
        </MorphingDialogContent>
    );
}
