import { useState } from 'react';
import { Trash2 } from 'lucide-react';
import { useUpdateChannel, useDeleteChannel, ChannelType, type Channel, type UpdateChannelRequest } from '@/api/endpoints/channel';
import {
    MorphingDialogTitle,
    MorphingDialogDescription,
    MorphingDialogClose,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { getTypeLabel } from './typeLabel';
import { type StatsMetricsFormatted } from '@/api/endpoints/stats';

export function CardContent({ channel, stats }: { channel: Channel, stats: StatsMetricsFormatted }) {
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
            return;
        }

        setIsOpen(false);
        setTimeout(() => {
            deleteChannel.mutate(channel.id);
        }, 300);
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
                                type="text"
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
                                <p className="font-medium text-card-foreground">{getTypeLabel(channel.type)}</p>
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
                                    <p className="font-medium text-accent">{stats.request_success.formatted.value}{stats.request_success.formatted.unit}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">失败请求</p>
                                    <p className="font-medium text-destructive">{stats.request_failed.formatted.value}{stats.request_failed.formatted.unit}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输入 Token</p>
                                    <p className="font-medium text-card-foreground">{stats.input_token.formatted.value}{stats.input_token.formatted.unit}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输出 Token</p>
                                    <p className="font-medium text-card-foreground">{stats.output_token.formatted.value}{stats.output_token.formatted.unit}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输入成本</p>
                                    <p className="font-medium text-card-foreground">{stats.input_cost.formatted.value}{stats.input_cost.formatted.unit}</p>
                                </div>
                                <div>
                                    <p className="text-sm text-muted-foreground mb-1">输出成本</p>
                                    <p className="font-medium text-card-foreground">{stats.output_cost.formatted.value}{stats.output_cost.formatted.unit}</p>
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
