'use client';

import { useState, useMemo } from 'react';
import { Pencil, Trash2, Check, X, Loader, ArrowDownToLine, ArrowUpFromLine } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useUpdateModel, useDeleteModel, type LLMInfo } from '@/api/endpoints/model';
import { Input } from '@/components/ui/input';
import { getModelIcon } from '@/lib/model-icons';
import { toast } from '@/components/common/Toast';

interface ModelItemProps {
    model: LLMInfo;
}

export function ModelItem({ model }: ModelItemProps) {
    const [isEditing, setIsEditing] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [editValues, setEditValues] = useState({
        input: model.input.toString(),
        output: model.output.toString(),
        cache_read: model.cache_read.toString(),
        cache_write: model.cache_write.toString(),
    });

    const updateModel = useUpdateModel();
    const deleteModel = useDeleteModel();

    const { Avatar: ModelAvatar, color: brandColor } = useMemo(() => getModelIcon(model.name), [model.name]);

    const handleEditClick = () => {
        setEditValues({
            input: model.input.toString(),
            output: model.output.toString(),
            cache_read: model.cache_read.toString(),
            cache_write: model.cache_write.toString(),
        });
        setIsEditing(true);
    };

    const handleCancelEdit = () => {
        setIsEditing(false);
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
                setIsEditing(false);
                toast.success('模型价格已更新');
            },
            onError: (error) => {
                toast.error('更新失败', { description: error.message });
            }
        });
    };

    const handleDeleteClick = () => setConfirmDelete(true);
    const handleCancelDelete = () => setConfirmDelete(false);
    const handleConfirmDelete = () => {
        deleteModel.mutate(model.name, {
            onSuccess: () => {
                setConfirmDelete(false);
                toast.success('模型已删除');
            },
            onError: (error) => {
                setConfirmDelete(false);
                toast.error('删除失败', { description: error.message });
            }
        });
    };

    return (
        <div className="group relative h-28 rounded-3xl border border-border bg-card custom-shadow transition-all duration-300">
            <div className="p-4 h-full flex gap-2">
                {/* Left: Avatar */}
                <div className="flex-shrink-0 flex items-center">
                    <ModelAvatar size={52} />
                </div>

                {/* Middle: Content */}
                <div className="flex-1 min-w-0 flex flex-col justify-center gap-2">
                    {/* Row 1: Name */}
                    <h3
                        className="text-base font-semibold text-card-foreground leading-tight truncate"
                        title={model.name}
                    >
                        {model.name}
                    </h3>

                    {/* Row 2: Input/Cache */}
                    <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
                        <ArrowDownToLine className="h-3.5 w-3.5" style={{ color: brandColor }} />
                        <span>输入/缓存</span>
                        <span className="tabular-nums">
                            {model.input.toFixed(2)}/{model.cache_read.toFixed(2)}$
                        </span>
                    </div>

                    {/* Row 3: Output/Cache */}
                    <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
                        <ArrowUpFromLine className="h-3.5 w-3.5" style={{ color: brandColor }} />
                        <span>输出/缓存</span>
                        <span className="tabular-nums">
                            {model.output.toFixed(2)}/{model.cache_write.toFixed(2)}$
                        </span>
                    </div>
                </div>

                {/* Right: Action buttons */}
                <div className="flex-shrink-0 flex flex-col justify-between">
                    <div className={`flex flex-col justify-between h-full ${isEditing || confirmDelete ? 'invisible' : ''}`}>
                        <motion.button
                            layoutId={`edit-btn-${model.name}`}
                            onClick={handleEditClick}
                            className="h-9 w-9 flex items-center justify-center rounded-lg bg-muted/60 text-muted-foreground transition-colors hover:bg-muted"
                            title="编辑"
                            disabled={isEditing || confirmDelete}
                        >
                            <Pencil className="h-4 w-4" />
                        </motion.button>

                        <motion.button
                            layoutId={`delete-btn-${model.name}`}
                            onClick={handleDeleteClick}
                            className="h-9 w-9 flex items-center justify-center rounded-lg bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                            title="删除"
                            disabled={isEditing || confirmDelete}
                        >
                            <Trash2 className="h-4 w-4" />
                        </motion.button>
                    </div>
                </div>
            </div>

            {/* Delete Confirmation Overlay */}
            <AnimatePresence>
                {confirmDelete && (
                    <motion.div
                        layoutId={`delete-btn-${model.name}`}
                        className="absolute inset-0 flex items-center justify-center gap-3 bg-destructive p-4 rounded-2xl"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        <button
                            onClick={handleCancelDelete}
                            className="h-9 px-4 flex items-center justify-center gap-1.5 rounded-xl bg-destructive-foreground/20 text-destructive-foreground text-sm font-medium transition-all hover:bg-destructive-foreground/30 active:scale-[0.98]"
                        >
                            <X className="h-4 w-4" />
                            取消
                        </button>
                        <button
                            onClick={handleConfirmDelete}
                            disabled={deleteModel.isPending}
                            className="h-9 px-4 flex items-center justify-center gap-1.5 rounded-xl bg-destructive-foreground text-destructive text-sm font-medium transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            {deleteModel.isPending ? (
                                <Loader className="h-4 w-4 animate-spin" />
                            ) : (
                                <Trash2 className="h-4 w-4" />
                            )}
                            {deleteModel.isPending ? '删除中...' : '确认删除'}
                        </button>
                    </motion.div>
                )}
            </AnimatePresence>

            {/* Edit Overlay */}
            <AnimatePresence>
                {isEditing && (
                    <motion.div
                        layoutId={`edit-btn-${model.name}`}
                        className="absolute inset-x-0 top-0 z-10 flex flex-col bg-card p-5 rounded-3xl border border-border custom-shadow"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        {/* Header */}
                        <div className="mb-3">
                            <h3 className="text-sm font-semibold text-card-foreground line-clamp-1">
                                {model.name}
                            </h3>
                        </div>

                        {/* Edit Form */}
                        <div className="space-y-3">
                            <div className="grid grid-cols-2 gap-2">
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">输入</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={editValues.input}
                                        onChange={(e) => setEditValues({ ...editValues, input: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">输出</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={editValues.output}
                                        onChange={(e) => setEditValues({ ...editValues, output: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">缓存读取</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={editValues.cache_read}
                                        onChange={(e) => setEditValues({ ...editValues, cache_read: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">缓存写入</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={editValues.cache_write}
                                        onChange={(e) => setEditValues({ ...editValues, cache_write: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                            </div>

                            {/* Action Buttons */}
                            <div className="flex gap-2 pt-2">
                                <button
                                    onClick={handleCancelEdit}
                                    disabled={updateModel.isPending}
                                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl bg-muted text-muted-foreground text-sm font-medium transition-all hover:bg-muted/80 active:scale-[0.98] disabled:opacity-50"
                                >
                                    <X className="h-4 w-4" />
                                    取消
                                </button>
                                <button
                                    onClick={handleSaveEdit}
                                    disabled={updateModel.isPending}
                                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl text-sm font-medium transition-all active:scale-[0.98] disabled:opacity-50"
                                    style={{ backgroundColor: brandColor, color: '#fff' }}
                                >
                                    {updateModel.isPending ? (
                                        <Loader className="h-4 w-4 animate-spin" />
                                    ) : (
                                        <Check className="h-4 w-4" />
                                    )}
                                    保存
                                </button>
                            </div>
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
}
