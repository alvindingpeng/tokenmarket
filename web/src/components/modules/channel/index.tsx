'use client';

import { PageWrapper } from '@/components/common/PageWrapper';
import { useChannelList, useCreateChannel, useUpdateChannel, useDeleteChannel, ChannelType, type Channel, type CreateChannelRequest, type UpdateChannelRequest } from '@/api/endpoints/channel';
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
import { Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { GRID_CARD_VARIANTS } from '@/lib/animations/fluid-transitions';

function CreateChannelForm() {
    const { setIsOpen } = useMorphingDialog();
    const createChannel = useCreateChannel();
    const [formData, setFormData] = useState<CreateChannelRequest>({
        name: '',
        type: ChannelType.OpenAIChat,
        base_url: '',
        key: '',
        model: '',
        enabled: true,
        proxy: false,
    });

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        createChannel.mutate(formData, {
            onSuccess: () => {
                setFormData({
                    name: '',
                    type: ChannelType.OpenAIChat,
                    base_url: '',
                    key: '',
                    model: '',
                    enabled: true,
                    proxy: false,
                });
                setIsOpen(false);
            }
        });
    };

    return (
        <form onSubmit={handleSubmit} className="space-y-4">
            <div>
                <label className="block text-sm font-medium mb-2 text-card-foreground">渠道名称</label>
                <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                    required
                />
            </div>

            <div>
                <label className="block text-sm font-medium mb-2 text-card-foreground">渠道类型</label>
                <select
                    value={formData.type}
                    onChange={(e) => setFormData({ ...formData, type: Number(e.target.value) as ChannelType })}
                    className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                >
                    <option value={ChannelType.OpenAIChat}>OpenAI Chat</option>
                    <option value={ChannelType.OpenAIResponse}>OpenAI Response</option>
                    <option value={ChannelType.Anthropic}>Anthropic</option>
                    <option value={ChannelType.OneAPI}>OneAPI</option>
                </select>
            </div>

            <div>
                <label className="block text-sm font-medium mb-2 text-card-foreground">Base URL</label>
                <input
                    type="text"
                    value={formData.base_url}
                    onChange={(e) => setFormData({ ...formData, base_url: e.target.value })}
                    className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                    placeholder="https://api.openai.com"
                    required
                />
            </div>

            <div>
                <label className="block text-sm font-medium mb-2 text-card-foreground">API Key</label>
                <input
                    type="password"
                    value={formData.key}
                    onChange={(e) => setFormData({ ...formData, key: e.target.value })}
                    className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                    required
                />
            </div>

            <div>
                <label className="block text-sm font-medium mb-2 text-card-foreground">模型</label>
                <input
                    type="text"
                    value={formData.model}
                    onChange={(e) => setFormData({ ...formData, model: e.target.value })}
                    className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                    placeholder="gpt-4"
                    required
                />
            </div>

            <div className="flex items-center gap-4">
                <label className="flex items-center gap-2">
                    <input
                        type="checkbox"
                        checked={formData.enabled}
                        onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
                        className="w-4 h-4 accent-primary"
                    />
                    <span className="text-sm text-card-foreground">启用</span>
                </label>

                <label className="flex items-center gap-2">
                    <input
                        type="checkbox"
                        checked={formData.proxy}
                        onChange={(e) => setFormData({ ...formData, proxy: e.target.checked })}
                        className="w-4 h-4 accent-primary"
                    />
                    <span className="text-sm text-card-foreground">使用代理</span>
                </label>
            </div>

            <button
                type="submit"
                className="w-full py-3 bg-primary hover:opacity-90 text-primary-foreground rounded-lg font-medium transition-all disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={createChannel.isPending}
            >
                {createChannel.isPending ? '创建中...' : '创建渠道'}
            </button>
        </form>
    );
}

function CreateChannelCard() {
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <div className="rounded-2xl bg-primary p-6 h-48 flex flex-col items-center justify-center gap-4 custom-shadow hover:opacity-90 transition-all duration-300 hover:scale-[1.02]">
                    <div className="w-16 h-16 rounded-full bg-primary-foreground/20 flex items-center justify-center">
                        <Plus className="w-8 h-8 text-primary-foreground" />
                    </div>
                    <span className="text-primary-foreground font-semibold text-lg">创建渠道</span>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-[500px] bg-card rounded-3xl p-8 custom-shadow">
                    <MorphingDialogTitle>
                        <div className="flex items-center justify-between mb-6">
                            <h2 className="text-2xl font-bold text-card-foreground">创建新渠道</h2>
                            <MorphingDialogClose
                                className="relative top-0 right-0"
                                variants={{
                                    initial: { opacity: 0, scale: 0.8 },
                                    animate: { opacity: 1, scale: 1 },
                                    exit: { opacity: 0, scale: 0.8 }
                                }}
                            />
                        </div>
                    </MorphingDialogTitle>

                    <MorphingDialogDescription>
                        <CreateChannelForm />
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

function ChannelCardContent({ channel }: { channel: Channel }) {
    const { setIsOpen } = useMorphingDialog();
    const updateChannel = useUpdateChannel();
    const deleteChannel = useDeleteChannel();
    const [isEditing, setIsEditing] = useState(false);
    const [isConfirmingDelete, setIsConfirmingDelete] = useState(false);
    const [formData, setFormData] = useState<UpdateChannelRequest>({
        id: channel.id,
        name: channel.name,
        type: channel.type,
        enabled: channel.enabled,
        base_url: channel.base_url,
        key: channel.key,
        model: channel.model,
        proxy: channel.proxy,
    });

    const handleUpdate = (e: React.FormEvent) => {
        e.preventDefault();
        updateChannel.mutate(formData, {
            onSuccess: () => {
                setIsEditing(false);
                setIsOpen(false);
            }
        });
    };

    const handleDeleteClick = () => {
        if (!isConfirmingDelete) {
            setIsConfirmingDelete(true);
        } else {
            // 先关闭对话框
            setIsOpen(false);
            // 等待对话框关闭动画完成后再删除
            setTimeout(() => {
                deleteChannel.mutate(channel.id);
            }, 300);
        }
    };

    const getChannelTypeLabel = (type: ChannelType) => {
        switch (type) {
            case ChannelType.OpenAIChat:
                return 'OpenAI Chat';
            case ChannelType.OpenAIResponse:
                return 'OpenAI Response';
            case ChannelType.Anthropic:
                return 'Anthropic';
            case ChannelType.OneAPI:
                return 'OneAPI';
            default:
                return 'Unknown';
        }
    };

    return (
        <>
            <MorphingDialogTitle>
                <div className="flex items-center justify-between mb-6">
                    <h2 className="text-2xl font-bold text-card-foreground">{isEditing ? '编辑渠道' : '渠道详情'}</h2>
                    <MorphingDialogClose
                        className="relative top-0 right-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 }
                        }}
                    />
                </div>
            </MorphingDialogTitle>

            <MorphingDialogDescription>
                {isEditing ? (
                    <form onSubmit={handleUpdate} className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium mb-2 text-card-foreground">渠道名称</label>
                            <input
                                type="text"
                                value={formData.name}
                                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                                className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                                required
                            />
                        </div>

                        <div>
                            <label className="block text-sm font-medium mb-2 text-card-foreground">渠道类型</label>
                            <select
                                value={formData.type}
                                onChange={(e) => setFormData({ ...formData, type: Number(e.target.value) as ChannelType })}
                                className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                            >
                                <option value={ChannelType.OpenAIChat}>OpenAI Chat</option>
                                <option value={ChannelType.OpenAIResponse}>OpenAI Response</option>
                                <option value={ChannelType.Anthropic}>Anthropic</option>
                                <option value={ChannelType.OneAPI}>OneAPI</option>
                            </select>
                        </div>

                        <div>
                            <label className="block text-sm font-medium mb-2 text-card-foreground">Base URL</label>
                            <input
                                type="text"
                                value={formData.base_url}
                                onChange={(e) => setFormData({ ...formData, base_url: e.target.value })}
                                className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                                required
                            />
                        </div>

                        <div>
                            <label className="block text-sm font-medium mb-2 text-card-foreground">API Key</label>
                            <input
                                type="password"
                                value={formData.key}
                                onChange={(e) => setFormData({ ...formData, key: e.target.value })}
                                className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                                required
                            />
                        </div>

                        <div>
                            <label className="block text-sm font-medium mb-2 text-card-foreground">模型</label>
                            <input
                                type="text"
                                value={formData.model}
                                onChange={(e) => setFormData({ ...formData, model: e.target.value })}
                                className="w-full px-4 py-2 rounded-lg bg-background text-foreground border border-border focus:outline-none focus:ring-2 focus:ring-ring"
                                required
                            />
                        </div>

                        <div className="flex items-center gap-4">
                            <label className="flex items-center gap-2">
                                <input
                                    type="checkbox"
                                    checked={formData.enabled}
                                    onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
                                    className="w-4 h-4 accent-primary"
                                />
                                <span className="text-sm text-card-foreground">启用</span>
                            </label>

                            <label className="flex items-center gap-2">
                                <input
                                    type="checkbox"
                                    checked={formData.proxy}
                                    onChange={(e) => setFormData({ ...formData, proxy: e.target.checked })}
                                    className="w-4 h-4 accent-primary"
                                />
                                <span className="text-sm text-card-foreground">使用代理</span>
                            </label>
                        </div>

                        <div className="flex gap-3 pt-4">
                            <button
                                type="button"
                                onClick={() => setIsEditing(false)}
                                className="flex-1 py-3 bg-muted hover:bg-muted/80 text-muted-foreground rounded-lg font-medium transition-all"
                            >
                                取消
                            </button>
                            <button
                                type="submit"
                                className="flex-1 py-3 bg-primary hover:opacity-90 text-primary-foreground rounded-lg font-medium transition-all disabled:opacity-50"
                                disabled={updateChannel.isPending}
                            >
                                {updateChannel.isPending ? '保存中...' : '保存'}
                            </button>
                        </div>
                    </form>
                ) : (
                    <div className="space-y-6">
                        <div className="grid grid-cols-2 gap-4">
                            <div>
                                <p className="text-sm text-muted-foreground mb-1">渠道类型</p>
                                <p className="font-medium text-card-foreground">{getChannelTypeLabel(channel.type)}</p>
                            </div>
                            <div>
                                <p className="text-sm text-muted-foreground mb-1">状态</p>
                                <p className={`font-medium ${channel.enabled ? 'text-accent' : 'text-destructive'}`}>
                                    {channel.enabled ? '启用' : '禁用'}
                                </p>
                            </div>
                            <div>
                                <p className="text-sm text-muted-foreground mb-1">模型</p>
                                <p className="font-medium text-card-foreground">{channel.model}</p>
                            </div>
                            <div>
                                <p className="text-sm text-muted-foreground mb-1">代理</p>
                                <p className="font-medium text-card-foreground">{channel.proxy ? '是' : '否'}</p>
                            </div>
                        </div>

                        <div>
                            <p className="text-sm text-muted-foreground mb-1">Base URL</p>
                            <p className="font-medium text-card-foreground break-all">{channel.base_url}</p>
                        </div>

                        <div className="border-t border-border pt-4">
                            <h3 className="font-semibold mb-3 text-card-foreground">统计信息</h3>
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">成功请求</p>
                                    <p className="font-medium text-accent">{channel.stats.request_success}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">失败请求</p>
                                    <p className="font-medium text-destructive">{channel.stats.request_failed}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输入 Token</p>
                                    <p className="font-medium text-card-foreground">{channel.stats.input_token.toLocaleString()}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输出 Token</p>
                                    <p className="font-medium text-card-foreground">{channel.stats.output_token.toLocaleString()}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输入成本</p>
                                    <p className="font-medium text-card-foreground">${(channel.stats.input_cost / 1000000).toFixed(4)}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输出成本</p>
                                    <p className="font-medium text-card-foreground">${(channel.stats.output_cost / 1000000).toFixed(4)}</p>
                                </div>
                            </div>
                        </div>

                        <div className="flex gap-3 pt-4">
                            <button
                                onClick={() => isConfirmingDelete ? setIsConfirmingDelete(false) : setIsEditing(true)}
                                className={`flex-1 py-3 rounded-lg font-medium transition-all duration-300 ${isConfirmingDelete
                                    ? 'bg-muted hover:bg-muted/80 text-muted-foreground'
                                    : 'bg-primary hover:opacity-90 text-primary-foreground'
                                    }`}
                            >
                                {isConfirmingDelete ? '取消' : '编辑'}
                            </button>
                            <button
                                onClick={handleDeleteClick}
                                className="flex-1 py-3 bg-destructive hover:opacity-90 text-destructive-foreground rounded-lg font-medium transition-all duration-300 flex items-center justify-center gap-2"
                                disabled={deleteChannel.isPending}
                            >
                                <Trash2 className={`w-4 h-4 transition-transform duration-300 ${isConfirmingDelete ? 'scale-110' : ''}`} />
                                {deleteChannel.isPending ? '删除中...' : isConfirmingDelete ? '确认删除' : '删除'}
                            </button>
                        </div>
                    </div>
                )}
            </MorphingDialogDescription>
        </>
    );
}

function ChannelCard({ channel }: { channel: Channel }) {
    const getChannelTypeLabel = (type: ChannelType) => {
        switch (type) {
            case ChannelType.OpenAIChat:
                return 'OpenAI Chat';
            case ChannelType.OpenAIResponse:
                return 'OpenAI Response';
            case ChannelType.Anthropic:
                return 'Anthropic';
            case ChannelType.OneAPI:
                return 'OneAPI';
            default:
                return 'Unknown';
        }
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <div className="rounded-2xl bg-card border border-border p-6 h-48 flex flex-col justify-between custom-shadow hover:border-primary/50 transition-all duration-300 hover:scale-[1.02]">
                    <div>
                        <div className="flex items-start justify-between mb-2">
                            <h3 className="font-semibold text-lg truncate text-card-foreground">{channel.name}</h3>
                            <span className={`px-2 py-1 rounded-full text-xs ${channel.enabled ? 'bg-accent/20 text-accent-foreground' : 'bg-destructive/20 text-destructive-foreground'}`}>
                                {channel.enabled ? '启用' : '禁用'}
                            </span>
                        </div>
                        <p className="text-sm text-muted-foreground">{getChannelTypeLabel(channel.type)}</p>
                    </div>

                    <div className="space-y-1 text-sm text-muted-foreground">
                        <div className="flex justify-between">
                            <span>请求次数:</span>
                            <span className="font-medium text-card-foreground">{channel.stats.request_success + channel.stats.request_failed}</span>
                        </div>
                        <div className="flex justify-between">
                            <span>总消耗:</span>
                            <span className="font-medium text-card-foreground">${((channel.stats.input_cost + channel.stats.output_cost) / 1000000).toFixed(4)}</span>
                        </div>
                    </div>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-[600px] bg-card rounded-3xl p-8 custom-shadow max-h-[90vh] overflow-y-auto">
                    <ChannelCardContent channel={channel} />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

export function Channel() {
    const { data: channelsData, isLoading, error } = useChannelList();

    return (
        <PageWrapper>
            {isLoading && (
                <div className="flex items-center justify-center py-12">
                    <div className="text-muted-foreground">加载中...</div>
                </div>
            )}

            {error && (
                <div className="flex items-center justify-center py-12">
                    <div className="text-destructive">加载失败: {error.message}</div>
                </div>
            )}

            {channelsData && (
                <motion.div
                    className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4"
                    variants={GRID_CARD_VARIANTS.container}
                    initial="initial"
                    animate="animate"
                >
                    <motion.div
                        variants={GRID_CARD_VARIANTS.item}
                        initial="initial"
                        animate="animate"
                        layout
                    >
                        <CreateChannelCard />
                    </motion.div>
                    <AnimatePresence mode="popLayout">
                        {[...channelsData.raw].sort((a, b) => a.id - b.id).map((channel) => (
                            <motion.div
                                key={channel.id}
                                variants={GRID_CARD_VARIANTS.item}
                                initial="initial"
                                animate="animate"
                                exit={{
                                    opacity: 0,
                                    filter: "blur(8px)",
                                    transition: { duration: 0.3 }
                                }}
                                layout
                            >
                                <ChannelCard channel={channel} />
                            </motion.div>
                        ))}
                    </AnimatePresence>
                </motion.div>
            )}
        </PageWrapper>
    );
}
