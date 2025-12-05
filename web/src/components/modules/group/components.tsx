'use client';

import { useState, useMemo } from 'react';
import { GripVertical, X, Check } from 'lucide-react';
import { Reorder, useDragControls, motion } from 'motion/react';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { type LLMChannel } from '@/api/endpoints/model';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';

export interface SelectedMember extends LLMChannel {
    id: string;
    item_id?: number;
}

// 可拖拽成员项
export function MemberItem({
    member,
    onRemove,
    isRemoving,
    index,
    editable = true,
}: {
    member: SelectedMember;
    onRemove: (id: string) => void;
    isRemoving?: boolean;
    index: number;
    editable?: boolean;
}) {
    const controls = useDragControls();
    const { Avatar: ModelAvatar } = getModelIcon(member.name);

    return (
        <Reorder.Item
            value={member}
            dragListener={false}
            dragControls={controls}
            className={cn('grid transition-[grid-template-rows] duration-200', isRemoving ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}
            whileDrag={editable ? { scale: 1.02, boxShadow: '0 8px 32px rgba(0,0,0,0.15)', zIndex: 50 } : undefined}
        >
            <div className="overflow-hidden">
                <div className={cn(
                    'flex items-center gap-2 rounded-lg bg-background border border-border/50 px-2.5 py-2 select-none transition-opacity duration-200',
                    isRemoving && 'opacity-0'
                )}>
                    <span className="size-5 rounded-md bg-primary/10 text-primary text-xs font-bold grid place-items-center shrink-0">
                        {index + 1}
                    </span>

                    <div
                        className={cn('p-0.5 rounded transition-colors', editable && 'cursor-grab active:cursor-grabbing hover:bg-muted touch-none')}
                        onPointerDown={editable ? (e) => controls.start(e) : undefined}
                    >
                        <GripVertical className={cn('size-3.5 transition-colors', editable ? 'text-muted-foreground' : 'text-muted-foreground/30')} />
                    </div>

                    <ModelAvatar size={18} />

                    <div className="flex flex-col min-w-0 flex-1">
                        <span className="text-sm font-medium truncate leading-tight">{member.name}</span>
                        <span className="text-[10px] text-muted-foreground truncate leading-tight">{member.channel_name}</span>
                    </div>

                    <motion.button
                        type="button"
                        onClick={() => onRemove(member.id)}
                        className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                        initial={false}
                        animate={{ opacity: editable ? 1 : 0, x: editable ? 0 : 8 }}
                        transition={{ duration: 0.15 }}
                        style={{ pointerEvents: editable ? 'auto' : 'none' }}
                    >
                        <X className="size-3" />
                    </motion.button>
                </div>
            </div>
        </Reorder.Item>
    );
}

// 添加成员行
export function AddMemberRow({
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
                <SelectTrigger className="flex-1 h-7 rounded-md text-xs min-w-0 [&_svg]:text-inherit!" size="sm">
                    <SelectValue placeholder={t('form.selectModel')} />
                </SelectTrigger>
                <SelectContent>
                    {models
                        .filter((m) => !selectedMembers.some((s) => s.channel_id === m.channel_id && s.name === m.name))
                        .map((m) => {
                            const { Avatar } = getModelIcon(m.name);
                            return (
                                <SelectItem key={m.name} value={m.name} className="[&_svg]:text-inherit!">
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
