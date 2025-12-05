'use client';

import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { Pencil, Trash2, Layers, GripVertical, X, Plus, Check } from 'lucide-react';
import { Reorder, useDragControls, motion, AnimatePresence } from 'motion/react';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { type Group, type GroupUpdateRequest, useDeleteGroup, useUpdateGroup } from '@/api/endpoints/group';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';

interface SelectedMember extends LLMChannel {
    id: string;
    item_id?: number; // 数据库中的 item id，新增时无此字段
}

// 成员项
function MemberItem({
    member,
    onRemove,
    isRemoving,
    index,
    editable,
}: {
    member: SelectedMember;
    onRemove?: (id: string) => void;
    isRemoving?: boolean;
    index: number;
    editable: boolean;
}) {
    const controls = useDragControls();
    const { Avatar: ModelAvatar } = getModelIcon(member.name);

    const inner = (
        <div className={cn(
            'flex items-center gap-2 rounded-lg bg-background border border-border/50 px-2.5 py-2 select-none',
            isRemoving && 'opacity-0'
        )}
            style={{ transition: 'opacity var(--duration-fast)' }}
        >
            <span className="size-5 rounded-md bg-primary/10 text-primary text-xs font-bold grid place-items-center shrink-0">
                {index + 1}
            </span>

            <div
                className={cn('p-0.5 rounded', editable && 'cursor-grab active:cursor-grabbing hover:bg-muted touch-none')}
                onPointerDown={editable ? (e) => controls.start(e) : undefined}
            >
                <GripVertical className={cn('size-3.5', editable ? 'text-muted-foreground' : 'text-muted-foreground/30')} />
            </div>

            <ModelAvatar size={18} />

            <div className="flex flex-col min-w-0 flex-1">
                <span className="text-sm font-medium truncate leading-tight">{member.name}</span>
                <span className="text-[10px] text-muted-foreground truncate leading-tight">{member.channel_name}</span>
            </div>

            {editable && onRemove && (
                <button
                    type="button"
                    onClick={() => onRemove(member.id)}
                    className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                >
                    <X className="size-3" />
                </button>
            )}
        </div>
    );

    if (!editable) return inner;

    return (
        <Reorder.Item
            value={member}
            dragListener={false}
            dragControls={controls}
            style={{
                display: 'grid',
                gridTemplateRows: isRemoving ? '0fr' : '1fr',
                transition: 'grid-template-rows var(--duration-fast)',
            }}
            whileDrag={{ scale: 1.02, boxShadow: '0 8px 32px rgba(0,0,0,0.15)', zIndex: 50 }}
        >
            <div className="overflow-hidden">{inner}</div>
        </Reorder.Item>
    );
}

// 添加成员行
function AddMemberRow({
    index,
    channels,
    modelChannels,
    selectedMembers,
    onConfirm,
    onCancel,
    t,
}: {
    index: number;
    channels: { id: number; name: string }[];
    modelChannels: LLMChannel[];
    selectedMembers: SelectedMember[];
    onConfirm: (channel: LLMChannel) => void;
    onCancel: () => void;
    t: (key: string) => string;
}) {
    const [channelId, setChannelId] = useState('');
    const [modelName, setModelName] = useState('');

    const models = useMemo(() => {
        if (!channelId) return [];
        return modelChannels.filter((mc) => mc.channel_id === +channelId);
    }, [modelChannels, channelId]);

    const isDuplicate = useMemo(() => {
        if (!channelId || !modelName) return false;
        return selectedMembers.some((m) => m.channel_id === +channelId && m.name === modelName);
    }, [selectedMembers, channelId, modelName]);

    const canConfirm = channelId && modelName && !isDuplicate;

    const handleConfirm = () => {
        if (!canConfirm) return;
        const channel = modelChannels.find((mc) => mc.channel_id === +channelId && mc.name === modelName);
        if (channel) onConfirm(channel);
    };

    return (
        <div className="flex items-center gap-2 rounded-lg bg-background border-2 border-dashed border-primary/30 px-2.5 py-2">
            <span className="size-5 rounded-md bg-primary/10 text-primary text-xs font-bold grid place-items-center shrink-0">
                {index + 1}
            </span>
            <div className="p-0.5">
                <GripVertical className="size-3.5 text-muted-foreground/30" />
            </div>

            <Select value={channelId} onValueChange={(v) => { setChannelId(v); setModelName(''); }}>
                <SelectTrigger className="flex-1 h-7 rounded-md text-xs min-w-0" size="sm">
                    <SelectValue placeholder={t('form.selectChannel')} />
                </SelectTrigger>
                <SelectContent>
                    {channels.map((c) => (
                        <SelectItem key={c.id} value={String(c.id)}>{c.name}</SelectItem>
                    ))}
                </SelectContent>
            </Select>

            <Select value={modelName} onValueChange={setModelName} disabled={!channelId}>
                <SelectTrigger className="flex-1 h-7 rounded-md text-xs min-w-0" size="sm">
                    <SelectValue placeholder={t('form.selectModel')} />
                </SelectTrigger>
                <SelectContent>
                    {models.map((m) => {
                        const { Avatar } = getModelIcon(m.name);
                        const added = selectedMembers.some((s) => s.channel_id === m.channel_id && s.name === m.name);
                        return (
                            <SelectItem key={m.name} value={m.name} disabled={added} className={cn(added && 'opacity-50')}>
                                <span className="flex items-center gap-2"><Avatar size={14} />{m.name}</span>
                            </SelectItem>
                        );
                    })}
                </SelectContent>
            </Select>

            <button
                type="button"
                onClick={handleConfirm}
                disabled={!canConfirm}
                className={cn(
                    'size-6 rounded-md grid place-items-center shrink-0 transition-colors',
                    canConfirm ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'bg-muted text-muted-foreground cursor-not-allowed'
                )}
            >
                <Check className="size-3.5" />
            </button>

            <button
                type="button"
                onClick={onCancel}
                className="size-6 rounded-md grid place-items-center shrink-0 hover:bg-destructive/10 hover:text-destructive text-muted-foreground transition-colors"
            >
                <X className="size-3.5" />
            </button>
        </div>
    );
}

// 主组件
export function GroupCard({ group }: { group: Group }) {
    const t = useTranslations('group');
    const updateGroup = useUpdateGroup();
    const deleteGroup = useDeleteGroup();
    const { data: modelChannels = [] } = useModelChannelList();

    const [isEditing, setIsEditing] = useState(false);
    const [editName, setEditName] = useState(group.name);
    const [editMembers, setEditMembers] = useState<SelectedMember[]>([]);
    const [removingIds, setRemovingIds] = useState<Set<string>>(new Set());
    const [isAdding, setIsAdding] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);

    // 用于追踪是否正在等待数据刷新
    const waitingForRefreshRef = useRef(false);

    // 当保存成功后，等待 group 数据刷新再退出编辑模式
    useEffect(() => {
        if (waitingForRefreshRef.current) {
            waitingForRefreshRef.current = false;
            setIsEditing(false);
        }
    }, [group]);

    // 合并计算渠道映射和列表
    const { channelMap, channels } = useMemo(() => {
        const map = new Map<number, string>();
        const list: { id: number; name: string }[] = [];
        modelChannels.forEach((mc) => {
            if (!map.has(mc.channel_id)) {
                map.set(mc.channel_id, mc.channel_name);
                list.push({ id: mc.channel_id, name: mc.channel_name });
            }
        });
        return { channelMap: map, channels: list };
    }, [modelChannels]);

    // 展示用成员列表
    const displayMembers = useMemo(() =>
        [...(group.items || [])]
            .sort((a, b) => a.priority - b.priority)
            .map((item) => ({
                id: `${item.channel_id}-${item.model_name}-${item.id || 0}`,
                name: item.model_name,
                channel_id: item.channel_id,
                channel_name: channelMap.get(item.channel_id) || `Channel ${item.channel_id}`,
                item_id: item.id, // 保存原始数据库 id
            })),
        [group.items, channelMap]
    );

    const handleStartEdit = () => {
        setEditName(group.name);
        setEditMembers(displayMembers.map((m, i) => ({
            ...m,
            id: `${m.id}-${Date.now()}-${i}`,
            item_id: m.item_id, // 保留原始 item id
        })));
        setIsEditing(true);
        setIsAdding(false);
        setConfirmDelete(false);
    };

    const handleCancelEdit = () => {
        setIsEditing(false);
        setIsAdding(false);
    };

    const handleAddMember = (channel: LLMChannel) => {
        setEditMembers((prev) => [...prev, { ...channel, id: `${channel.channel_id}-${channel.name}-${Date.now()}` }]);
        setIsAdding(false);
    };

    const handleRemoveMember = useCallback((id: string) => {
        setRemovingIds((prev) => new Set(prev).add(id));
        setTimeout(() => {
            setEditMembers((prev) => prev.filter((m) => m.id !== id));
            setRemovingIds((prev) => { const n = new Set(prev); n.delete(id); return n; });
        }, 200);
    }, []);

    const handleSave = () => {
        // 构建原始 items 的 Map (按 item_id)
        const originalItems = new Map(
            (group.items || []).map((item) => [item.id, item])
        );

        // 构建编辑后 items 的 Map (按 item_id)
        const editItemIds = new Set(
            editMembers.filter((m) => m.item_id !== undefined).map((m) => m.item_id)
        );

        const req: GroupUpdateRequest = {
            id: group.id!,
        };

        // 名称变更
        if (editName !== group.name) {
            req.name = editName;
        }

        // 删除的 items: 原始存在但编辑后不存在的
        const itemsToDelete = [...originalItems.keys()].filter(
            (id) => id !== undefined && !editItemIds.has(id)
        ) as number[];
        if (itemsToDelete.length > 0) {
            req.items_to_delete = itemsToDelete;
        }

        // 新增的 items: 编辑后存在但无 item_id 的
        const itemsToAdd = editMembers
            .filter((m) => m.item_id === undefined)
            .map((m, _, arr) => ({
                channel_id: m.channel_id,
                model_name: m.name,
                priority: editMembers.indexOf(m) + 1,
            }));
        if (itemsToAdd.length > 0) {
            req.items_to_add = itemsToAdd;
        }

        // 更新的 items: 原始存在且编辑后仍存在，但 priority 变更的
        const itemsToUpdate = editMembers
            .filter((m) => {
                if (m.item_id === undefined) return false;
                const original = originalItems.get(m.item_id);
                if (!original) return false;
                const newPriority = editMembers.indexOf(m) + 1;
                return original.priority !== newPriority;
            })
            .map((m) => ({
                id: m.item_id!,
                priority: editMembers.indexOf(m) + 1,
            }));
        if (itemsToUpdate.length > 0) {
            req.items_to_update = itemsToUpdate;
        }

        updateGroup.mutate(req, {
            onSuccess: () => {
                // 标记等待数据刷新，当 group 数据更新后再退出编辑模式
                waitingForRefreshRef.current = true;
            },
        });
    };

    const handleDeleteClick = () => {
        setConfirmDelete(true);
    };

    const handleConfirmDelete = () => {
        if (group.id) {
            deleteGroup.mutate(group.id);
        }
    };

    const handleCancelDelete = () => {
        setConfirmDelete(false);
    };

    const isValid = editName.trim() && editMembers.length > 0;
    const members = isEditing ? editMembers : displayMembers;
    const visibleCount = isEditing ? editMembers.filter((m) => !removingIds.has(m.id)).length : displayMembers.length;
    const isEmpty = visibleCount === 0 && !isAdding;

    return (
        <article
            className="h-full flex flex-col rounded-3xl border border-border bg-card text-card-foreground p-4 custom-shadow"
            style={{ '--duration-fast': '200ms' } as React.CSSProperties}
        >
            {/* Header */}
            <header className="flex items-start justify-between mb-3 relative overflow-hidden rounded-xl -mx-1 px-1 -my-1 py-1">
                <h3
                    contentEditable={isEditing}
                    suppressContentEditableWarning
                    onBlur={(e) => isEditing && setEditName(e.currentTarget.textContent || '')}
                    onInput={(e) => isEditing && setEditName(e.currentTarget.textContent || '')}
                    className="text-lg font-bold truncate flex-1 mr-2 outline-none"
                >
                    {isEditing ? editName : group.name}
                </h3>

                <nav className="flex items-center gap-1 shrink-0">
                    {isEditing ? (
                        <>
                            <button type="button" onClick={handleCancelEdit} className="p-1.5 rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition-colors" title={t('detail.actions.cancel')}>
                                <X className="size-4" />
                            </button>
                            <button type="button" onClick={() => setIsAdding(true)} disabled={isAdding} className={cn('p-1.5 rounded-lg transition-colors', isAdding ? 'bg-primary/10 text-primary' : 'hover:bg-muted text-muted-foreground hover:text-foreground')} title={t('form.addItem')}>
                                <Plus className="size-4" />
                            </button>
                            <button type="button" onClick={handleSave} disabled={!isValid || updateGroup.isPending} className={cn('p-1.5 rounded-lg transition-colors', isValid && !updateGroup.isPending ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'bg-muted text-muted-foreground cursor-not-allowed')} title={t('detail.actions.save')}>
                                {updateGroup.isPending ? <div className="size-4 border-2 border-current border-t-transparent rounded-full animate-spin" /> : <Check className="size-4" />}
                            </button>
                        </>
                    ) : (
                        <>
                            <button type="button" onClick={handleStartEdit} className="p-1.5 rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition-colors" title={t('detail.actions.edit')}>
                                <Pencil className="size-4" />
                            </button>
                            {!confirmDelete && (
                                <motion.button
                                    layoutId={`delete-btn-group-${group.id}`}
                                    type="button"
                                    onClick={handleDeleteClick}
                                    className="p-1.5 rounded-lg hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
                                    title={t('detail.actions.delete')}
                                >
                                    <Trash2 className="size-4" />
                                </motion.button>
                            )}
                        </>
                    )}
                </nav>

                {/* Delete Confirmation Overlay */}
                <AnimatePresence>
                    {confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-group-${group.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive px-2 py-1 rounded-xl"
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={handleCancelDelete}
                                className="flex h-8 w-8 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                                title={t('detail.actions.cancel')}
                            >
                                <X className="h-4 w-4" />
                            </button>
                            <button
                                type="button"
                                onClick={handleConfirmDelete}
                                disabled={deleteGroup.isPending}
                                className="flex-1 h-8 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                <Trash2 className="h-3.5 w-3.5" />
                                {deleteGroup.isPending ? t('detail.actions.deleting') : t('detail.actions.confirmDelete')}
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </header>

            {/* Members */}
            <section className="flex-1 rounded-xl border border-border/50 bg-muted/30 overflow-hidden relative min-h-0">
                {/* Empty state */}
                <div className={cn('absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground transition-opacity', isEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none')} style={{ transitionDuration: 'var(--duration-fast)' }}>
                    <Layers className="size-8 opacity-40" />
                    <span className="text-xs">{t('card.empty')}</span>
                </div>

                {/* List */}
                <div className={cn('h-full overflow-y-auto transition-opacity', isEmpty && 'opacity-0')} style={{ transitionDuration: 'var(--duration-fast)' }}>
                    <div className="p-2 flex flex-col gap-1.5">
                        {isEditing ? (
                            <>
                                {members.length > 0 && (
                                    <Reorder.Group axis="y" values={editMembers} onReorder={setEditMembers} className="flex flex-col gap-1.5">
                                        {editMembers.map((m, i) => (
                                            <MemberItem key={m.id} member={m} onRemove={handleRemoveMember} isRemoving={removingIds.has(m.id)} index={i} editable />
                                        ))}
                                    </Reorder.Group>
                                )}
                                {isAdding && <AddMemberRow index={editMembers.length} channels={channels} modelChannels={modelChannels} selectedMembers={editMembers} onConfirm={handleAddMember} onCancel={() => setIsAdding(false)} t={t} />}
                            </>
                        ) : (
                            members.map((m, i) => <MemberItem key={m.id} member={m} index={i} editable={false} />)
                        )}
                    </div>
                </div>
            </section>
        </article>
    );
}
