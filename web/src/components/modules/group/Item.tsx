'use client';

import { useState } from 'react';
import { GripVertical, X, Trash2 } from 'lucide-react';
import { Reorder, useDragControls, motion, AnimatePresence } from 'motion/react';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import type { LLMChannel } from '@/api/endpoints/model';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';

export interface SelectedMember extends LLMChannel {
    id: string;
    item_id?: number;
    weight?: number;
}

export function MemberItem({
    member,
    onRemove,
    onWeightChange,
    onDragStart,
    onDragEnd,
    isRemoving,
    index,
    editable = true,
    showWeight = false,
    layoutScope,
}: {
    member: SelectedMember;
    onRemove: (id: string) => void;
    onWeightChange?: (id: string, weight: number) => void;
    onDragStart?: () => void;
    onDragEnd?: () => void;
    isRemoving?: boolean;
    index: number;
    editable?: boolean;
    showWeight?: boolean;
    layoutScope?: string;
}) {
    useTranslations('group'); // keep i18n namespace warm (future-proof)
    const controls = useDragControls();
    const { Avatar: ModelAvatar } = getModelIcon(member.name);
    const [confirmDelete, setConfirmDelete] = useState(false);

    return (
        <Reorder.Item
            value={member}
            dragListener={false}
            dragControls={controls}
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
            className={cn('grid transition-[grid-template-rows] duration-200', isRemoving ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}
            whileDrag={editable ? { scale: 1.02, boxShadow: '0 8px 32px rgba(0,0,0,0.15)', zIndex: 50 } : undefined}
        >
            <div className="overflow-hidden">
                <div className={cn(
                    'flex items-center gap-2 rounded-lg bg-background border border-border/50 px-2.5 py-2 select-none transition-opacity duration-200 relative overflow-hidden',
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
                        <Tooltip side="top" sideOffset={10} align="start">
                            <TooltipTrigger className="text-sm font-medium truncate leading-tight">{member.name}</TooltipTrigger>
                            <TooltipContent>{member.name}</TooltipContent>
                        </Tooltip>
                        <span className="text-[10px] text-muted-foreground truncate leading-tight">{member.channel_name}</span>
                    </div>

                    {showWeight && editable && (
                        <input
                            type="number"
                            min={1}
                            value={member.weight ?? 1}
                            onChange={(e) => onWeightChange?.(member.id, Math.max(1, parseInt(e.target.value) || 1))}
                            className="w-12 h-6 text-xs text-center rounded border border-border bg-muted/50 focus:outline-none focus:ring-1 focus:ring-primary"
                        />
                    )}

                    {!confirmDelete && (
                        <motion.button
                            layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                            type="button"
                            onClick={() => setConfirmDelete(true)}
                            className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                            initial={false}
                            animate={{ opacity: editable ? 1 : 0, x: editable ? 0 : 8 }}
                            transition={{ duration: 0.15 }}
                            style={{ pointerEvents: editable ? 'auto' : 'none' }}
                        >
                            <X className="size-3" />
                        </motion.button>
                    )}

                    <AnimatePresence>
                        {confirmDelete && (
                            <motion.div
                                layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                                className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-1.5 rounded-lg"
                                transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                            >
                                <button
                                    type="button"
                                    onClick={() => setConfirmDelete(false)}
                                    className="flex h-6 w-6 items-center justify-center rounded-md bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                                >
                                    <X className="h-3 w-3" />
                                </button>
                                <button
                                    type="button"
                                    onClick={() => onRemove(member.id)}
                                    className="flex-1 h-6 flex items-center justify-center gap-1.5 rounded-md bg-destructive-foreground text-destructive text-xs font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98]"
                                >
                                    <Trash2 className="h-3 w-3" />
                                </button>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>
            </div>
        </Reorder.Item>
    );
}


