'use client';

import { useState, useMemo, useCallback } from 'react';
import { Plus, Layers } from 'lucide-react';
import { Reorder } from 'motion/react';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useCreateGroup, type GroupItem } from '@/api/endpoints/group';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldLabel, FieldGroup } from '@/components/ui/field';
import { cn } from '@/lib/utils';
import { MemberItem, AddMemberRow, type SelectedMember } from './components';

function MembersSection({
    members,
    onReorder,
    onRemove,
    onAdd,
    removingIds,
    emptyText,
    channels,
    modelChannels,
    t,
}: {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    onAdd: (channel: LLMChannel) => void;
    removingIds: Set<string>;
    emptyText: string;
    channels: { id: number; name: string }[];
    modelChannels: LLMChannel[];
    t: (key: string) => string;
}) {
    const [isAdding, setIsAdding] = useState(false);
    const hasMembers = members.length > 0;
    const visibleCount = members.filter((m) => !removingIds.has(m.id)).length;
    const showEmpty = visibleCount === 0 && !isAdding;

    const handleConfirmAdd = (channel: LLMChannel) => {
        onAdd(channel);
        setIsAdding(false);
    };

    return (
        <div className="rounded-xl border border-border/50 bg-muted/30 overflow-hidden">
            {/* 标题栏 */}
            <div className="flex items-center justify-between px-3 py-2 border-b border-border/30 bg-muted/50">
                <span className="text-sm font-medium text-foreground">
                    {t('form.items')}
                    {members.length > 0 && (
                        <span className="ml-1.5 text-xs text-muted-foreground font-normal">
                            ({members.length})
                        </span>
                    )}
                </span>
                <button
                    type="button"
                    onClick={() => setIsAdding(true)}
                    className={cn(
                        'flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                        isAdding
                            ? 'bg-primary/10 text-primary cursor-default'
                            : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                    )}
                    disabled={isAdding}
                >
                    <Plus className="size-3.5" />
                    <span>{t('form.addItem')}</span>
                </button>
            </div>

            {/* 内容区域 */}
            <div className="relative h-96">
                {/* 空状态 */}
                <div
                    className={cn(
                        'absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground',
                        'transition-opacity duration-200 ease-out',
                        showEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none'
                    )}
                >
                    <Layers className="size-10 opacity-40" />
                    <span className="text-sm">{emptyText}</span>
                </div>

                {/* 列表区域 */}
                <div
                    className={cn(
                        'h-full overflow-y-auto transition-opacity duration-200',
                        showEmpty ? 'opacity-0' : 'opacity-100'
                    )}
                >
                    <div className="p-2 flex flex-col gap-1.5">
                        {/* 已添加的成员列表 */}
                        {hasMembers && (
                            <Reorder.Group
                                axis="y"
                                values={members}
                                onReorder={onReorder}
                                className="flex flex-col gap-1.5"
                            >
                                {members.map((member, index) => (
                                    <MemberItem
                                        key={member.id}
                                        member={member}
                                        onRemove={onRemove}
                                        isRemoving={removingIds.has(member.id)}
                                        index={index}
                                    />
                                ))}
                            </Reorder.Group>
                        )}

                        {/* 新增成员项 - 放在列表末尾 */}
                        {isAdding && (
                            <AddMemberRow
                                index={members.length}
                                channels={channels}
                                modelChannels={modelChannels}
                                selectedMembers={members}
                                onConfirm={handleConfirmAdd}
                                onCancel={() => setIsAdding(false)}
                                t={t}
                            />
                        )}
                    </div>
                </div>
            </div>
        </div>
    );
}

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const createGroup = useCreateGroup();
    const { data: modelChannels = [] } = useModelChannelList();
    const t = useTranslations('group');

    const [groupName, setGroupName] = useState('');
    const [selectedMembers, setSelectedMembers] = useState<SelectedMember[]>([]);
    const [removingIds, setRemovingIds] = useState<Set<string>>(new Set());

    // 获取唯一的渠道列表
    const channels = useMemo(() => {
        const channelMap = new Map<number, { id: number; name: string }>();
        modelChannels.forEach((mc) => {
            if (!channelMap.has(mc.channel_id)) {
                channelMap.set(mc.channel_id, { id: mc.channel_id, name: mc.channel_name });
            }
        });
        return Array.from(channelMap.values());
    }, [modelChannels]);

    const handleAddMember = (channel: LLMChannel) => {
        const newMember: SelectedMember = {
            ...channel,
            id: `${channel.channel_id}-${channel.name}-${Date.now()}`,
        };
        setSelectedMembers((prev) => [...prev, newMember]);
    };

    const handleRemoveMember = useCallback((id: string) => {
        setRemovingIds((prev) => new Set(prev).add(id));

        setTimeout(() => {
            setSelectedMembers((prev) => prev.filter((m) => m.id !== id));
            setRemovingIds((prev) => {
                const next = new Set(prev);
                next.delete(id);
                return next;
            });
        }, 200);
    }, []);

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();

        const items: GroupItem[] = selectedMembers.map((member, index) => ({
            channel_id: member.channel_id,
            model_name: member.name,
            priority: index + 1,
        }));

        createGroup.mutate(
            {
                name: groupName,
                items,
            },
            {
                onSuccess: () => {
                    setGroupName('');
                    setSelectedMembers([]);
                    setIsOpen(false);
                },
            }
        );
    };

    const isValid = groupName.trim() && selectedMembers.length > 0;

    return (
        <>
            <MorphingDialogTitle>
                <header className="mb-5 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">
                        {t('create.title')}
                    </h2>
                    <MorphingDialogClose
                        className="relative right-0 top-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 },
                        }}
                    />
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit}>
                    <FieldGroup className="gap-4">
                        {/* Group Name */}
                        <Field>
                            <FieldLabel htmlFor="group-name">{t('form.name')}</FieldLabel>
                            <Input
                                id="group-name"
                                value={groupName}
                                onChange={(e) => setGroupName(e.target.value)}
                                className="rounded-xl"
                            />
                        </Field>

                        {/* 路由规则区域（包含添加功能） */}
                        <MembersSection
                            members={selectedMembers}
                            onReorder={setSelectedMembers}
                            onRemove={handleRemoveMember}
                            onAdd={handleAddMember}
                            removingIds={removingIds}
                            emptyText={t('form.noItems')}
                            channels={channels}
                            modelChannels={modelChannels}
                            t={t}
                        />

                        {/* Submit Button */}
                        <Button
                            type="submit"
                            disabled={!isValid || createGroup.isPending}
                            className="w-full rounded-xl h-11"
                        >
                            {createGroup.isPending ? t('create.submitting') : t('create.submit')}
                        </Button>
                    </FieldGroup>
                </form>
            </MorphingDialogDescription>
        </>
    );
}
