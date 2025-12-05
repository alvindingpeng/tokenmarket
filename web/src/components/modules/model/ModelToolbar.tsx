'use client';

import { useState } from 'react';
import { Search, Plus, Check, X, Loader } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useCreateModel } from '@/api/endpoints/model';
import { Input } from '@/components/ui/input';
import { toast } from '@/components/common/Toast';

interface ModelToolbarProps {
    searchTerm: string;
    onSearchChange: (value: string) => void;
}

export function ModelToolbar({ searchTerm, onSearchChange }: ModelToolbarProps) {
    const [isCreating, setIsCreating] = useState(false);
    const [createValues, setCreateValues] = useState({
        name: '',
        input: '',
        output: '',
        cache_read: '',
        cache_write: '',
    });

    const createModel = useCreateModel();

    const handleCreateClick = () => {
        setCreateValues({
            name: '',
            input: '',
            output: '',
            cache_read: '',
            cache_write: '',
        });
        setIsCreating(true);
    };

    const handleCancelCreate = () => {
        setIsCreating(false);
    };

    const handleSaveCreate = () => {
        if (!createValues.name.trim()) {
            toast.error('请输入模型名称');
            return;
        }

        createModel.mutate({
            name: createValues.name.trim(),
            input: parseFloat(createValues.input) || 0,
            output: parseFloat(createValues.output) || 0,
            cache_read: parseFloat(createValues.cache_read) || 0,
            cache_write: parseFloat(createValues.cache_write) || 0,
        }, {
            onSuccess: () => {
                setIsCreating(false);
                toast.success('模型创建成功');
            },
            onError: (error) => {
                toast.error('创建失败', { description: error.message });
            }
        });
    };

    return (
        <div className="group relative h-28 rounded-3xl border border-border bg-card custom-shadow transition-all duration-300">
            <div className="p-4 h-full flex flex-col gap-2">
                {/* Search */}
                <div className="relative flex-1">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input
                        type="text"
                        value={searchTerm}
                        onChange={(e) => onSearchChange(e.target.value)}
                        placeholder="搜索模型..."
                        className="pl-10 h-full rounded-xl"
                    />
                </div>

                {/* Create Button */}
                <div className={`flex-1 ${isCreating ? 'invisible' : ''}`}>
                    <motion.button
                        layoutId="create-model-btn"
                        onClick={handleCreateClick}
                        className="w-full h-full flex items-center justify-center gap-2 rounded-xl bg-primary text-primary-foreground text-sm font-medium transition-colors hover:bg-primary/90"
                        disabled={isCreating}
                    >
                        <Plus className="h-4 w-4" />
                        创建模型
                    </motion.button>
                </div>
            </div>

            {/* Create Overlay */}
            <AnimatePresence>
                {isCreating && (
                    <motion.div
                        layoutId="create-model-btn"
                        className="absolute inset-x-0 top-0 z-10 flex flex-col bg-card p-5 rounded-3xl border border-border custom-shadow"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        {/* Create Form */}
                        <div className="space-y-3">
                            {/* Model Name */}
                            <div>
                                <label className="text-xs text-muted-foreground mb-1 block">模型名称</label>
                                <Input
                                    type="text"
                                    value={createValues.name}
                                    onChange={(e) => setCreateValues({ ...createValues, name: e.target.value })}
                                    placeholder="例如: gpt-4o"
                                    className="h-9 text-sm rounded-xl"
                                />
                            </div>

                            {/* Price Fields */}
                            <div className="grid grid-cols-2 gap-2">
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">输入</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={createValues.input}
                                        onChange={(e) => setCreateValues({ ...createValues, input: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">输出</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={createValues.output}
                                        onChange={(e) => setCreateValues({ ...createValues, output: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">缓存读取</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={createValues.cache_read}
                                        onChange={(e) => setCreateValues({ ...createValues, cache_read: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                                <div>
                                    <label className="text-xs text-muted-foreground mb-1 block">缓存写入</label>
                                    <Input
                                        type="number"
                                        step="any"
                                        value={createValues.cache_write}
                                        onChange={(e) => setCreateValues({ ...createValues, cache_write: e.target.value })}
                                        className="h-9 text-sm rounded-xl"
                                    />
                                </div>
                            </div>

                            {/* Action Buttons */}
                            <div className="flex gap-2 pt-2">
                                <button
                                    onClick={handleCancelCreate}
                                    disabled={createModel.isPending}
                                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl bg-muted text-muted-foreground text-sm font-medium transition-all hover:bg-muted/80 active:scale-[0.98] disabled:opacity-50"
                                >
                                    <X className="h-4 w-4" />
                                    取消
                                </button>
                                <button
                                    onClick={handleSaveCreate}
                                    disabled={createModel.isPending}
                                    className="flex-1 h-9 flex items-center justify-center gap-1.5 rounded-xl bg-primary text-primary-foreground text-sm font-medium transition-all hover:bg-primary/90 active:scale-[0.98] disabled:opacity-50"
                                >
                                    {createModel.isPending ? (
                                        <Loader className="h-4 w-4 animate-spin" />
                                    ) : (
                                        <Check className="h-4 w-4" />
                                    )}
                                    创建
                                </button>
                            </div>
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
}

