import { memo, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { Pencil, Trash2, ArrowDownToLine, ArrowUpFromLine, Gauge } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useTranslations } from 'use-intl';
import { useUpdateModel, useDeleteModel, type LLMInfo, type ModelScore } from '@/api/model';
import { getModelIcon } from '@/lib/model-icons';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ModelDeleteOverlay, ModelEditOverlay } from './ItemOverlays';
import { cn } from '@/lib/utils';
import { createPortal } from 'react-dom';

interface ModelItemProps {
    model: LLMInfo;
    layout?: 'grid' | 'list';
    score?: ModelScore; // 该模型的全局选路评分; 无观测时按冷启动展示。
}

// 缓存编辑弹层实测高度，首次打开即可正确判断是否需要向上弹出
let cachedEditOverlayHeight = 0;

// formatScoreLatency 把毫秒滑动平均缩写为展示文本, 与分组页成员徽章口径一致。
const formatScoreLatency = (ms: number) => (ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`);

// scoreTooltip 组装评分悬浮说明: 汇总口径 + 各渠道来源(渠道模型按上游模型逐个评分, 同名模型跨渠道归并)。
function scoreTooltip(score: ModelScore | undefined, t: ReturnType<typeof useTranslations>) {
    const head = t('card.scoreTitle');
    if (!score || score.samples <= 0) return head;
    const sources = score.channels
        .map((ch) => `${ch.channel}: ${formatScoreLatency(ch.wait_ms)} / ${Math.round(ch.success * 100)}% (${ch.samples})`)
        .join(', ');
    return sources ? `${head}\n${sources}` : head;
}

export const ModelItem = memo(function ModelItem({ model, layout = 'grid', score }: ModelItemProps) {
    const t = useTranslations('model');
    const isListLayout = layout === 'list';
    const [isEditOpen, setIsEditOpen] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [overlayRect, setOverlayRect] = useState<{ top: number; left: number; width: number } | null>(null);
    const instanceId = useId();
    const editLayoutId = `edit-btn-${model.name}-${instanceId}`;
    const deleteLayoutId = `delete-btn-${model.name}-${instanceId}`;
    const cardRef = useRef<HTMLElement | null>(null);
    const [editValues, setEditValues] = useState(() => ({
        input: model.input.toString(),
        output: model.output.toString(),
        cache_read: model.cache_read.toString(),
        cache_write: model.cache_write.toString(),
    }));

    const updateModel = useUpdateModel();
    const deleteModel = useDeleteModel();

    const { Icon, className: iconClassName, color: brandColor } = useMemo(() => getModelIcon(model.name), [model.name]);

    const updateOverlayRect = useCallback(() => {
        const card = cardRef.current;
        if (!card) return;
        const rect = card.getBoundingClientRect();
        const height = cachedEditOverlayHeight;
        // 下方空间不足时改为向上弹出，避免最后一行弹层被视口底部截断
        const flipUp = height > 0 && rect.top + height > window.innerHeight;
        const top = flipUp
            ? Math.min(Math.max(rect.bottom - height, 0), Math.max(window.innerHeight - height, 0))
            : rect.top;
        setOverlayRect((prev) => {
            if (prev && prev.top === top && prev.left === rect.left && prev.width === rect.width) {
                return prev;
            }
            return { top, left: rect.left, width: rect.width };
        });
    }, []);

    const closeEdit = useCallback(() => {
        setIsEditOpen(false);
    }, []);

    const handleOverlayHeightChange = useCallback((height: number) => {
        if (height === cachedEditOverlayHeight) return;
        cachedEditOverlayHeight = height;
        updateOverlayRect();
    }, [updateOverlayRect]);

    const handleEditClick = () => {
        setConfirmDelete(false);
        setEditValues({
            input: model.input.toString(),
            output: model.output.toString(),
            cache_read: model.cache_read.toString(),
            cache_write: model.cache_write.toString(),
        });
        // Ensure first open already has anchor geometry so layout animation can run.
        updateOverlayRect();
        setIsEditOpen(true);
    };

    const handleCancelEdit = () => {
        closeEdit();
    };

    const handleSaveEdit = () => {
        updateModel.mutate({
            name: model.name,
            input: parseFloat(editValues.input) || 0,
            output: parseFloat(editValues.output) || 0,
            cache_read: parseFloat(editValues.cache_read) || 0,
            cache_write: parseFloat(editValues.cache_write) || 0,
        }, {
            onSuccess: () => {
                closeEdit();
                toast.success(t('toast.updated'));
            },
            onError: (error) => {
                toast.error(t('toast.updateFailed'), { description: error.message });
            }
        });
    };

    const handleDeleteClick = () => {
        closeEdit();
        setConfirmDelete(true);
    };
    const handleCancelDelete = () => setConfirmDelete(false);
    const handleConfirmDelete = () => {
        deleteModel.mutate(model.name, {
            onSuccess: () => {
                setConfirmDelete(false);
                toast.success(t('toast.deleted'));
            },
            onError: (error) => {
                setConfirmDelete(false);
                toast.error(t('toast.deleteFailed'), { description: error.message });
            }
        });
    };

    useEffect(() => {
        if (!isEditOpen) return;

        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') closeEdit();
        };

        updateOverlayRect();
        window.addEventListener('resize', updateOverlayRect);
        window.addEventListener('scroll', updateOverlayRect, true);
        document.addEventListener('keydown', handleKeyDown);

        return () => {
            window.removeEventListener('resize', updateOverlayRect);
            window.removeEventListener('scroll', updateOverlayRect, true);
            document.removeEventListener('keydown', handleKeyDown);
        };
    }, [isEditOpen, updateOverlayRect, closeEdit]);

    const shouldRenderEditPortal = isEditOpen || overlayRect !== null;

    return (
        <article
            ref={cardRef}
            className={cn(
                'group relative rounded-3xl border border-border bg-card flex items-center gap-3 p-4',
                (isEditOpen || confirmDelete) && 'z-50'
            )}
        >
            <Icon aria-hidden="true" className={iconClassName} width={52} height={52} />

            <div className="flex-1 min-w-0 flex flex-col justify-center gap-2">
                <Tooltip>
                    <TooltipTrigger asChild>
                        <span className="w-fit max-w-full text-base font-semibold text-card-foreground leading-tight truncate">
                            {model.name}
                        </span>
                    </TooltipTrigger>
                    <TooltipContent key={model.name} side="top" sideOffset={10} align="center">
                        {model.name}
                    </TooltipContent>
                </Tooltip>

                {isListLayout ? (
                    <p className="flex items-center gap-2 overflow-hidden text-sm text-muted-foreground whitespace-nowrap">
                        <span className="inline-flex items-center gap-1">
                            <ArrowDownToLine className="size-3.5 shrink-0" style={{ color: brandColor }} />
                            {t('card.inputCache')}
                            <span className="tabular-nums">{model.input.toFixed(2)}/{model.cache_read.toFixed(2)}$</span>
                        </span>
                        <span className="text-muted-foreground/60">|</span>
                        <span className="inline-flex items-center gap-1 overflow-hidden">
                            <ArrowUpFromLine className="size-3.5 shrink-0" style={{ color: brandColor }} />
                            {t('card.outputCache')}
                            <span className="tabular-nums truncate">{model.output.toFixed(2)}/{model.cache_write.toFixed(2)}$</span>
                        </span>
                        <span className="text-muted-foreground/60">|</span>
                        <span className="inline-flex items-center gap-1 shrink-0">
                            <Gauge className={cn('size-3.5 shrink-0', score && score.samples > 0 && score.success < 0.7 && 'text-destructive')} />
                            {score && score.samples > 0 ? (
                                <span title={scoreTooltip(score, t)} className={cn('tabular-nums', score.success >= 0.7 ? 'text-primary' : 'text-destructive')}>
                                    {t('card.scoreLine', { latency: formatScoreLatency(score.wait_ms), success: Math.round(score.success * 100), samples: score.samples })}
                                </span>
                            ) : (
                                <span title={scoreTooltip(score, t)} className="text-muted-foreground/70">{t('card.scoreCold')}</span>
                            )}
                        </span>
                    </p>
                ) : (
                    <>
                        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                            <ArrowDownToLine className="size-3.5" style={{ color: brandColor }} />
                            {t('card.inputCache')}
                            <span className="tabular-nums">{model.input.toFixed(2)}/{model.cache_read.toFixed(2)}$</span>
                        </p>

                        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                            <ArrowUpFromLine className="size-3.5" style={{ color: brandColor }} />
                            {t('card.outputCache')}
                            <span className="tabular-nums">{model.output.toFixed(2)}/{model.cache_write.toFixed(2)}$</span>
                        </p>

                <p className="flex items-center gap-1.5 text-sm">
                    <Gauge className="size-3.5 shrink-0 text-muted-foreground" />
                    {score && score.samples > 0 ? (
                        <span
                            title={scoreTooltip(score, t)}
                            className={cn(
                                'tabular-nums font-medium',
                                score.success >= 0.7 ? 'text-primary' : 'text-destructive'
                            )}
                        >
                            {t('card.scoreLine', { latency: formatScoreLatency(score.wait_ms), success: Math.round(score.success * 100), samples: score.samples })}
                        </span>
                    ) : (
                        <span title={scoreTooltip(score, t)} className="text-muted-foreground/70">{t('card.scoreCold')}</span>
                    )}
                </p>
                    </>
                )}
            </div>

            <div
                className={cn(
                    isListLayout
                        ? 'shrink-0 flex items-center gap-2 self-center'
                        : 'shrink-0 flex flex-col justify-between self-stretch',
                    (isEditOpen || confirmDelete) && 'invisible pointer-events-none'
                )}
            >
                <motion.button
                    layoutId={editLayoutId}
                    type="button"
                    onClick={handleEditClick}
                    disabled={isEditOpen || confirmDelete}
                    className="h-9 w-9 flex items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted disabled:opacity-50"
                >
                    <Pencil className="size-4" />
                </motion.button>

                <motion.button
                    layoutId={deleteLayoutId}
                    type="button"
                    onClick={handleDeleteClick}
                    disabled={isEditOpen || confirmDelete}
                    className="h-9 w-9 flex items-center justify-center rounded-lg bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground disabled:opacity-50"
                >
                    <Trash2 className="size-4" />
                </motion.button>
            </div>

            <AnimatePresence>
                {confirmDelete && (
                    <ModelDeleteOverlay
                        layoutId={deleteLayoutId}
                        isPending={deleteModel.isPending}
                        onCancel={handleCancelDelete}
                        onConfirm={handleConfirmDelete}
                    />
                )}
            </AnimatePresence>

            {shouldRenderEditPortal && typeof document !== 'undefined'
                ? createPortal(
                    <AnimatePresence onExitComplete={() => setOverlayRect(null)}>
                        {isEditOpen && overlayRect && (
                            <div
                                className="fixed z-[90]"
                                style={{
                                    top: `${overlayRect.top}px`,
                                    left: `${overlayRect.left}px`,
                                    width: `${overlayRect.width}px`,
                                }}
                            >
                                <ModelEditOverlay
                                    layoutId={editLayoutId}
                                    onHeightChange={handleOverlayHeightChange}
                                    modelName={model.name}
                                    brandColor={brandColor}
                                    editValues={editValues}
                                    isPending={updateModel.isPending}
                                    onChange={setEditValues}
                                    onCancel={handleCancelEdit}
                                    onSave={handleSaveEdit}
                                />
                            </div>
                        )}
                    </AnimatePresence>,
                    document.body
                )
                : null}
        </article>
    );
});