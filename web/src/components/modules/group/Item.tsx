'use client';

import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { Pencil, Trash2, Layers, X, Plus, Check, Copy } from 'lucide-react';
import { Reorder, motion, AnimatePresence } from 'motion/react';
import { type Group, type GroupUpdateRequest, useDeleteGroup, useUpdateGroup } from '@/api/endpoints/group';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { MemberItem, AddMemberRow, type SelectedMember } from './components';

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
    const [copied, setCopied] = useState(false);

    // 用于追踪是否正在等待数据刷新
    const waitingForRefreshRef = useRef(false);

    // 当保存成功后，等待 group 数据刷新再退出编辑模式
    useEffect(() => {
        if (waitingForRefreshRef.current) {
            waitingForRefreshRef.current = false;
            queueMicrotask(() => setIsEditing(false));
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
        setEditMembers([...displayMembers]);
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
            .map((m) => ({
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
        <article className="flex flex-col rounded-3xl border border-border bg-card text-card-foreground p-4 custom-shadow">
            <header className="flex items-start justify-between mb-3 relative overflow-hidden rounded-xl -mx-1 px-1 -my-1 py-1">
                <h3
                    contentEditable={isEditing}
                    suppressContentEditableWarning
                    onBlur={(e) => isEditing && setEditName(e.currentTarget.textContent || '')}
                    className="text-lg font-bold truncate flex-1 mr-2 outline-none"
                >
                    {isEditing ? editName : group.name}
                </h3>

                <div className="flex items-center gap-1 shrink-0">
                    <AnimatePresence mode="wait">
                        {isEditing ? (
                            <motion.div
                                key="editing"
                                className="flex items-center gap-1"
                                initial={{ opacity: 0, scale: 0.8 }}
                                animate={{ opacity: 1, scale: 1 }}
                                exit={{ opacity: 0, scale: 0.8 }}
                                transition={{ duration: 0.15 }}
                            >
                                <button type="button" onClick={handleCancelEdit} className="p-1.5 rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition-colors">
                                    <X className="size-4" />
                                </button>
                                <button type="button" onClick={() => setIsAdding(true)} disabled={isAdding} className={cn('p-1.5 rounded-lg transition-colors', isAdding ? 'bg-primary/10 text-primary' : 'hover:bg-muted text-muted-foreground hover:text-foreground')}>
                                    <Plus className="size-4" />
                                </button>
                                <button type="button" onClick={handleSave} disabled={!isValid || updateGroup.isPending} className={cn('p-1.5 rounded-lg transition-colors', isValid && !updateGroup.isPending ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'bg-muted text-muted-foreground cursor-not-allowed')}>
                                    {updateGroup.isPending ? <div className="size-4 border-2 border-current border-t-transparent rounded-full animate-spin" /> : <Check className="size-4" />}
                                </button>
                            </motion.div>
                        ) : (
                            <motion.div
                                key="viewing"
                                className="flex items-center gap-1"
                                initial={{ opacity: 0, scale: 0.8 }}
                                animate={{ opacity: 1, scale: 1 }}
                                exit={{ opacity: 0, scale: 0.8 }}
                                transition={{ duration: 0.15 }}
                            >
                                <button
                                    type="button"
                                    onClick={() => {
                                        navigator.clipboard.writeText(group.name);
                                        setCopied(true);
                                        setTimeout(() => setCopied(false), 1500);
                                    }}
                                    className="p-1.5 rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition-colors"
                                >
                                    <AnimatePresence mode="wait">
                                        {copied ? (
                                            <motion.div key="check" initial={{ scale: 0 }} animate={{ scale: 1 }} exit={{ scale: 0 }}>
                                                <Check className="size-4" />
                                            </motion.div>
                                        ) : (
                                            <motion.div key="copy" initial={{ scale: 0 }} animate={{ scale: 1 }} exit={{ scale: 0 }}>
                                                <Copy className="size-4" />
                                            </motion.div>
                                        )}
                                    </AnimatePresence>
                                </button>
                                <button type="button" onClick={handleStartEdit} className="p-1.5 rounded-lg hover:bg-muted text-muted-foreground hover:text-foreground transition-colors"  >
                                    <Pencil className="size-4" />
                                </button>
                                {!confirmDelete && (
                                    <motion.button
                                        layoutId={`delete-btn-group-${group.id}`}
                                        type="button"
                                        onClick={handleDeleteClick}
                                        className="p-1.5 rounded-lg hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
                                    >
                                        <Trash2 className="size-4" />
                                    </motion.button>
                                )}
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>

                <AnimatePresence>
                    {confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-group-${group.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-2 rounded-xl"
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={handleCancelDelete}
                                className="flex h-7 w-7 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                            >
                                <X className="h-4 w-4" />
                            </button>
                            <button
                                type="button"
                                onClick={handleConfirmDelete}
                                disabled={deleteGroup.isPending}
                                className="flex-1 h-7 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                <Trash2 className="h-3.5 w-3.5" />
                                {t('detail.actions.confirmDelete')}
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </header>

            <section className={cn('flex-1 rounded-xl border bg-muted/30 overflow-hidden relative min-h-0 transition-colors duration-200', isEditing ? 'border-primary/50' : 'border-border/50')}>
                <div className={cn('absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground transition-opacity duration-200', isEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none')}>
                    <Layers className="size-8 opacity-40" />
                    <span className="text-xs">{t('card.empty')}</span>
                </div>

                <div className={cn('min-h-100  overflow-y-auto transition-opacity duration-200', isEmpty && 'opacity-0')}>
                    <div className="p-2 flex flex-col gap-1.5">
                        <Reorder.Group axis="y" values={members} onReorder={isEditing ? setEditMembers : () => { }} className="flex flex-col gap-1.5">
                            {members.map((m, i) => (
                                <MemberItem key={m.id} member={m} onRemove={handleRemoveMember} isRemoving={removingIds.has(m.id)} index={i} editable={isEditing} />
                            ))}
                        </Reorder.Group>
                        {isAdding && <AddMemberRow index={editMembers.length} channels={channels} modelChannels={modelChannels} selectedMembers={editMembers} onConfirm={handleAddMember} onCancel={() => setIsAdding(false)} t={t} />}
                    </div>
                </div>
            </section>
        </article>
    );
}
