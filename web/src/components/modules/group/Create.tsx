'use client';

import { useMemo, useState, useCallback, type FormEvent } from 'react';
import { Plus, Layers, Sparkles } from 'lucide-react';
import { Reorder } from 'motion/react';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useCreateGroup, type GroupItem, type GroupMode } from '@/api/endpoints/group';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldLabel, FieldGroup } from '@/components/ui/field';
import { cn } from '@/lib/utils';
import { MemberItem, AddMemberRow, type SelectedMember } from './components';

function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

function memberKey(member: Pick<SelectedMember, 'channel_id' | 'name'>) {
    return `${member.channel_id}-${member.name}`;
}

function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}

function MembersSection({
    members,
    onReorder,
    onRemove,
    onWeightChange,
    onAdd,
    onAutoAdd,
    removingIds,
    emptyText,
    showWeight,
    autoAddDisabled,
}: {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    onWeightChange: (id: string, weight: number) => void;
    onAdd: (channel: LLMChannel) => void;
    onAutoAdd: () => void;
    removingIds: Set<string>;
    emptyText: string;
    showWeight: boolean;
    autoAddDisabled: boolean;
}) {
    const t = useTranslations('group');
    const [isAdding, setIsAdding] = useState(false);
    const showEmpty = members.filter((m) => !removingIds.has(m.id)).length === 0 && !isAdding;

    const handleConfirmAdd = useCallback((channel: LLMChannel) => {
        onAdd(channel);
        setIsAdding(false);
    }, [onAdd]);

    const handleCancelAdd = useCallback(() => {
        setIsAdding(false);
    }, []);

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
                <div className="flex items-center gap-2">
                    <button
                        type="button"
                        onClick={onAutoAdd}
                        className={cn(
                            'flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                            autoAddDisabled
                                ? 'text-muted-foreground/50 cursor-not-allowed'
                                : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                        )}
                        disabled={autoAddDisabled}
                        title={t('form.autoAdd')}
                    >
                        <Sparkles className="size-3.5" />
                        <span>{t('form.autoAdd')}</span>
                    </button>
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
                        {members.length > 0 && (
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
                                        onWeightChange={onWeightChange}
                                        isRemoving={removingIds.has(member.id)}
                                        index={index}
                                        showWeight={showWeight}
                                    />
                                ))}
                            </Reorder.Group>
                        )}

                        {/* 新增成员项 - 放在列表末尾 */}
                        {isAdding && (
                            <AddMemberRow
                                index={members.length}
                                selectedMembers={members}
                                onConfirm={handleConfirmAdd}
                                onCancel={handleCancelAdd}
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
    const t = useTranslations('group');
    const { data: modelChannels = [] } = useModelChannelList();

    const [groupName, setGroupName] = useState('');
    const [mode, setMode] = useState<GroupMode>(1);
    const [selectedMembers, setSelectedMembers] = useState<SelectedMember[]>([]);
    const [removingIds, setRemovingIds] = useState<Set<string>>(new Set());

    const groupKey = useMemo(() => normalizeKey(groupName), [groupName]);

    const handleAddMember = useCallback((channel: LLMChannel) => {
        const key = memberKey(channel);
        setSelectedMembers((prev) => {
            if (prev.some((m) => memberKey(m) === key)) return prev;
            return [...prev, { ...channel, id: key, weight: 1 }];
        });
    }, []);

    const autoAddDisabled = useMemo(() => {
        if (!groupKey) return true;
        const existing = new Set(selectedMembers.map(memberKey));
        return !modelChannels.some((mc) => matchesGroupName(mc.name, groupKey) && !existing.has(memberKey(mc)));
    }, [groupKey, modelChannels, selectedMembers]);

    const handleAutoAdd = useCallback(() => {
        if (!groupKey) return;

        const matched = modelChannels.filter((mc) => matchesGroupName(mc.name, groupKey));
        if (matched.length === 0) return;

        setSelectedMembers((prev) => {
            const existing = new Set(prev.map(memberKey));
            const toAdd: SelectedMember[] = [];
            matched.forEach((mc) => {
                const k = memberKey(mc);
                if (!existing.has(k)) {
                    existing.add(k);
                    toAdd.push({ ...mc, id: k, weight: 1 });
                }
            });
            return toAdd.length ? [...prev, ...toAdd] : prev;
        });
    }, [groupKey, modelChannels]);

    const handleWeightChange = useCallback((id: string, weight: number) => {
        setSelectedMembers((prev) => prev.map((m) => m.id === id ? { ...m, weight } : m));
    }, []);

    const handleRemoveMember = useCallback((id: string) => {
        setRemovingIds((prev) => new Set(prev).add(id));
        setTimeout(() => {
            setSelectedMembers((prev) => prev.filter((m) => m.id !== id));
            setRemovingIds((prev) => { const n = new Set(prev); n.delete(id); return n; });
        }, 200);
    }, []);

    const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();

        const items: GroupItem[] = selectedMembers.map((member, index) => ({
            channel_id: member.channel_id,
            model_name: member.name,
            priority: index + 1,
            weight: member.weight ?? 1,
        }));

        createGroup.mutate(
            {
                name: groupName,
                mode,
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

    const isValid = groupKey.length > 0 && selectedMembers.length > 0;

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

                        {/* Mode */}
                        <div className="flex gap-1">
                            {([1, 2, 3, 4] as const).map((m) => (
                                <button
                                    key={m}
                                    type="button"
                                    onClick={() => setMode(m)}
                                    className={cn(
                                        'flex-1 py-1 text-xs rounded-lg transition-colors',
                                        mode === m ? 'bg-primary text-primary-foreground' : 'bg-muted hover:bg-muted/80'
                                    )}
                                >
                                    {t(`mode.${m === 1 ? 'roundRobin' : m === 2 ? 'random' : m === 3 ? 'failover' : 'weighted'}`)}
                                </button>
                            ))}
                        </div>

                        {/* 模型区域（包含添加功能） */}
                        <MembersSection
                            members={selectedMembers}
                            onReorder={setSelectedMembers}
                            onRemove={handleRemoveMember}
                            onWeightChange={handleWeightChange}
                            onAdd={handleAddMember}
                            onAutoAdd={handleAutoAdd}
                            removingIds={removingIds}
                            emptyText={t('form.noItems')}
                            showWeight={mode === 4}
                            autoAddDisabled={autoAddDisabled}
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
