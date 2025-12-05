'use client';

import { useState, useMemo, useCallback } from 'react';
import { Plus, GripVertical, X, Layers, Check } from 'lucide-react';
import { Reorder, useDragControls } from 'motion/react';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { useCreateGroup, type GroupItem } from '@/api/endpoints/group';
import { useModelChannelList, type LLMChannel } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldLabel, FieldGroup } from '@/components/ui/field';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';

interface SelectedMember extends LLMChannel {
    id: string;
}

// 已确认的成员项 - 可拖拽
function DraggableItem({
    member,
    onRemove,
    isRemoving,
    index,
}: {
    member: SelectedMember;
    onRemove: (id: string) => void;
    isRemoving: boolean;
    index: number;
}) {
    const controls = useDragControls();
    const { Avatar: ModelAvatar } = getModelIcon(member.name);

    return (
        <Reorder.Item
            value={member}
            dragListener={false}
            dragControls={controls}
            className="relative"
            style={{
                display: 'grid',
                gridTemplateRows: isRemoving ? '0fr' : '1fr',
                transition: 'grid-template-rows 200ms ease-out',
            }}
            whileDrag={{
                scale: 1.02,
                boxShadow: '0 8px 32px rgba(0, 0, 0, 0.15)',
                zIndex: 50,
            }}
        >
            <div className="overflow-hidden">
                <div
                    className={cn(
                        'flex items-center gap-2 rounded-lg bg-background border border-border/50 px-2.5 py-2 select-none',
                        'transition-opacity duration-200 ease-out',
                        isRemoving && 'opacity-0'
                    )}
                >
                    <span className="flex items-center justify-center size-5 rounded-md bg-primary/10 text-primary text-xs font-bold shrink-0">
                        {index + 1}
                    </span>

                    <button
                        type="button"
                        onPointerDown={(e) => controls.start(e)}
                        className="cursor-grab active:cursor-grabbing p-0.5 rounded hover:bg-muted transition-colors touch-none"
                    >
                        <GripVertical className="size-3.5 text-muted-foreground" />
                    </button>

                    <ModelAvatar size={18} />
                    <div className="flex flex-col min-w-0 flex-1 gap-0">
                        <span className="text-sm font-medium truncate leading-tight">{member.name}</span>
                        <span className="text-[10px] text-muted-foreground truncate leading-tight">
                            {member.channel_name}
                        </span>
                    </div>

                    <button
                        type="button"
                        onClick={() => onRemove(member.id)}
                        className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                        disabled={isRemoving}
                    >
                        <X className="size-3" />
                    </button>
                </div>
            </div>
        </Reorder.Item>
    );
}

// 新增成员项 - 编辑模式（和成员项样式一致）
function EditingMemberItem({
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
    const [selectedChannelId, setSelectedChannelId] = useState<string>('');
    const [selectedModelName, setSelectedModelName] = useState<string>('');

    // 根据选中的渠道筛选可用的模型
    const availableModels = useMemo(() => {
        if (!selectedChannelId) return [];
        const channelIdNum = parseInt(selectedChannelId, 10);
        return modelChannels.filter((mc) => mc.channel_id === channelIdNum);
    }, [modelChannels, selectedChannelId]);

    // 检查是否已经添加过该模型
    const isModelAlreadyAdded = useMemo(() => {
        if (!selectedChannelId || !selectedModelName) return false;
        const channelIdNum = parseInt(selectedChannelId, 10);
        return selectedMembers.some(
            (m) => m.channel_id === channelIdNum && m.name === selectedModelName
        );
    }, [selectedMembers, selectedChannelId, selectedModelName]);

    const handleChannelChange = (value: string) => {
        setSelectedChannelId(value);
        setSelectedModelName('');
    };

    const handleConfirm = () => {
        if (!selectedChannelId || !selectedModelName || isModelAlreadyAdded) return;

        const channelIdNum = parseInt(selectedChannelId, 10);
        const channel = modelChannels.find(
            (mc) => mc.channel_id === channelIdNum && mc.name === selectedModelName
        );

        if (channel) {
            onConfirm(channel);
        }
    };

    const canConfirm = selectedChannelId && selectedModelName && !isModelAlreadyAdded;

    return (
        <div className="flex items-center gap-2 rounded-lg bg-background border-2 border-dashed border-primary/30 px-2.5 py-2">
            {/* 序号 */}
            <span className="flex items-center justify-center size-5 rounded-md bg-primary/10 text-primary text-xs font-bold shrink-0">
                {index + 1}
            </span>

            {/* 占位 - 对应拖拽手柄 */}
            <div className="p-0.5">
                <GripVertical className="size-3.5 text-muted-foreground/30" />
            </div>

            {/* 选择渠道 */}
            <Select value={selectedChannelId} onValueChange={handleChannelChange}>
                <SelectTrigger className="flex-1 h-7 rounded-md text-xs min-w-0" size="sm">
                    <SelectValue placeholder={t('form.selectChannel')} />
                </SelectTrigger>
                <SelectContent>
                    {channels.map((channel) => (
                        <SelectItem key={channel.id} value={String(channel.id)}>
                            {channel.name}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>

            {/* 选择模型 */}
            <Select
                value={selectedModelName}
                onValueChange={setSelectedModelName}
                disabled={!selectedChannelId}
            >
                <SelectTrigger className="flex-1 h-7 rounded-md text-xs min-w-0" size="sm">
                    <SelectValue placeholder={t('form.selectModel')} />
                </SelectTrigger>
                <SelectContent>
                    {availableModels.map((model) => {
                        const { Avatar: ModelAvatar } = getModelIcon(model.name);
                        const isAdded = selectedMembers.some(
                            (m) => m.channel_id === model.channel_id && m.name === model.name
                        );
                        return (
                            <SelectItem
                                key={model.name}
                                value={model.name}
                                disabled={isAdded}
                                className={cn(isAdded && 'opacity-50')}
                            >
                                <div className="flex items-center gap-2">
                                    <ModelAvatar size={14} />
                                    <span>{model.name}</span>
                                </div>
                            </SelectItem>
                        );
                    })}
                </SelectContent>
            </Select>

            {/* 确认按钮 */}
            <button
                type="button"
                onClick={handleConfirm}
                disabled={!canConfirm}
                className={cn(
                    'flex items-center justify-center size-6 rounded-md shrink-0 transition-colors',
                    canConfirm
                        ? 'bg-primary text-primary-foreground hover:bg-primary/90'
                        : 'bg-muted text-muted-foreground cursor-not-allowed'
                )}
            >
                <Check className="size-3.5" />
            </button>

            {/* 取消按钮 */}
            <button
                type="button"
                onClick={onCancel}
                className="flex items-center justify-center size-6 rounded-md shrink-0 hover:bg-destructive/10 hover:text-destructive text-muted-foreground transition-colors"
            >
                <X className="size-3.5" />
            </button>
        </div>
    );
}

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
                                    <DraggableItem
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
                            <EditingMemberItem
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

function CreateDialogContent() {
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
                                placeholder={t('form.namePlaceholder')}
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

export function CreateGroupButton() {
    const t = useTranslations('group.create');

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className={buttonVariants({ size: "default", className: "rounded-xl gap-2 transition-none" })}>
                <Plus className="size-4" />
                <span>{t('button')}</span>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-full max-w-xl bg-card text-card-foreground px-6 py-4 rounded-3xl custom-shadow max-h-[90vh] overflow-y-auto">
                    <CreateDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
