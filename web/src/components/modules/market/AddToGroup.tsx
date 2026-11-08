import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import { useGroupList, useUpdateGroup, type Group } from '@/api/group';
import type { MarketChannelListing, MarketModel } from '@/api/market';
import { cn } from '@/lib/utils';

/**
 * AddToGroup 让用户把该模型下的可用挂牌加进自己已有的模型配置(分组)。
 *
 * 只做"加成员": 不新建分组、不改模式、不改 Relay 配置 —— 那是分组页的活。
 * 提交走 useUpdateGroup 的 items 整体替换, 与分组页同一条写路径:
 * 成员的优先级、当前成员与灰度成员的一致性只在 `syncGroupItems` 那个事务里成立,
 * 绕过它会留下"当前成员指向已删除成员"之类的悬空引用。
 */
export function AddToGroup({ model }: { model: MarketModel }) {
    const t = useTranslations('market');
    // SSE 打开: 加入后分组页与这里的"已加入"角标都能自动跟上, 不必手动刷新。
    const { data: groups } = useGroupList(true, true);
    // 只有可用渠道能加: 不可用的挂牌加进去也选不到路, 徒增一条灰成员。
    const addable = model.channels.filter((listing) => listing.available);
    const ownedGroups = groups ?? [];
    const allJoined = ownedGroups.every((group) => addable.every((listing) => groupHasListing(group, listing)));

    // 一条可用渠道都没有时不给入口: 按钮点了也加不进任何东西, 那是死路。
    if (addable.length === 0) return null;

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">
                <Button size="sm" variant="outline" className="rounded-xl" disabled={allJoined}>
                    <Plus className="size-3.5" />
                    {allJoined ? t('action.allGroupsJoined') : t('action.addToGroup')}
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

/** groupHasListing 判断某分组里是否已有这条挂牌(按授权主键)。 */
function groupHasListing(group: Group, listing: MarketChannelListing): boolean {
    return group.items.some((item) => item.channel_grant_id === listing.channel_grant_id);
}

function Panel({ model, addable, ownedGroups }: { model: MarketModel; addable: MarketChannelListing[]; ownedGroups: Group[] }) {
    const t = useTranslations('market');
    const updateGroup = useUpdateGroup();
    // 加入成功后自行关掉弹层: 保留弹层会停在"已在其中"的状态上, 用户以为没提交成功。
    const { setIsOpen } = useMorphingDialog();
    // 默认选中第一个"还有渠道没加过"的分组, 并把还没加的渠道预先勾上:
    // 多数场景是"这个模型我在某个分组里还没配", 预勾选省掉两次点击。
    const initialGroup = ownedGroups.find((group) => addable.some((listing) => !groupHasListing(group, listing))) ?? ownedGroups[0];
    const [selectedGroupId, setSelectedGroupId] = useState<number | null>(initialGroup?.id ?? null);
    const [selectedGrantIds, setSelectedGrantIds] = useState<number[]>(() => {
        const group = ownedGroups.find((item) => addable.some((listing) => !groupHasListing(item, listing)));
        return group ? addable.filter((listing) => !groupHasListing(group, listing)).map((listing) => listing.channel_grant_id) : addable.map((listing) => listing.channel_grant_id);
    });

    const group = ownedGroups.find((item) => item.id === selectedGroupId);

    const toggleGrant = (grantId: number) => {
        setSelectedGrantIds((current) =>
            current.includes(grantId) ? current.filter((id) => id !== grantId) : [...current, grantId]
        );
    };

    const submit = () => {
        if (!group || selectedGrantIds.length === 0) return;
        // 现有成员按授权主键去重后追加到末尾: 既有成员的顺序与优先级一律不动。
        // 这正是后端 items 整体替换语义的用法 —— 提交的是"完整的新集合", 不是增量。
        const existing = group.items.map((item) => item.channel_grant_id);
        const merged = [...existing];
        for (const grantId of selectedGrantIds) {
            if (!merged.includes(grantId)) merged.push(grantId);
        }
        updateGroup.mutate(
            { id: group.id, items: merged.map((channel_grant_id) => ({ channel_grant_id })) },
            {
                onSuccess: () => {
                    toast.success(t('toast.added', { name: group.name, count: selectedGrantIds.length }));
                    setIsOpen(false);
                },
                onError: (error) => toast.error(t('toast.addFailed'), { description: error.message }),
            }
        );
    };

    return (
        <MorphingDialogContent className="w-[min(92vw,32rem)] rounded-3xl p-6">
            <MorphingDialogTitle className="text-lg font-semibold">{t('dialog.title')}</MorphingDialogTitle>
            <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                {ownedGroups.length === 0 ? t('dialog.noGroup') : t('dialog.description', { name: model.name })}
            </MorphingDialogDescription>

            {ownedGroups.length > 0 && (
                <div className="mt-4 flex flex-col gap-4">
                    <div className="flex flex-col gap-2">
                        <span className="text-xs font-medium text-muted-foreground">{t('dialog.pickGroup')}</span>
                        <div className="flex max-h-40 flex-col gap-1 overflow-y-auto rounded-xl border border-border p-1">
                            {ownedGroups.map((item) => {
                                const joined = addable.every((listing) => groupHasListing(item, listing));
                                return (
                                    <button
                                        key={item.id}
                                        type="button"
                                        onClick={() => setSelectedGroupId(item.id)}
                                        className={cn(
                                            'flex items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted',
                                            selectedGroupId === item.id && 'bg-muted'
                                        )}
                                    >
                                        <span className="truncate font-medium">{item.name}</span>
                                        <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
                                            {t('dialog.groupMeta', { count: item.items.length, mode: t('dialog.mode.' + item.mode) })}
                                        </span>
                                        {joined && (
                                            <span className="shrink-0 rounded bg-background px-1 py-0.5 text-[10px] text-muted-foreground">{t('dialog.alreadyIn')}</span>
                                        )}
                                    </button>
                                );
                            })}
                        </div>
                    </div>

                    <div className="flex flex-col gap-2">
                        <span className="text-xs font-medium text-muted-foreground">{t('dialog.pickChannel')}</span>
                        <div className="flex flex-col gap-1">
                            {addable.map((listing) => {
                                const already = group ? groupHasListing(group, listing) : false;
                                const checked = selectedGrantIds.includes(listing.channel_grant_id);
                                return (
                                    <label
                                        key={listing.channel_grant_id}
                                        className={cn('flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm', already ? 'opacity-60' : 'cursor-pointer hover:bg-muted')}
                                    >
                                        <Checkbox
                                            checked={already || checked}
                                            disabled={already}
                                            onCheckedChange={() => { if (!already) toggleGrant(listing.channel_grant_id); }}
                                        />
                                        <span className="font-mono">{listing.channel_code}</span>
                                        {already && <span className="ml-auto text-[11px] text-muted-foreground">{t('dialog.alreadyIn')}</span>}
                                    </label>
                                );
                            })}
                        </div>
                    </div>
                </div>
            )}

            <div className="mt-6 flex items-center justify-end gap-2">
                <MorphingDialogClose className="static rounded-xl px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
                    {t('dialog.cancel')}
                </MorphingDialogClose>
                <Button
                    size="sm"
                    className="rounded-xl"
                    disabled={ownedGroups.length === 0 || selectedGroupId === null || selectedGrantIds.length === 0 || updateGroup.isPending}
                    onClick={submit}
                >
                    {t('dialog.confirm')}
                </Button>
            </div>
        </MorphingDialogContent>
    );
}
