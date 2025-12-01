import { useState } from 'react';
import { useCreateChannel, ChannelType, type CreateChannelRequest } from '@/api/endpoints/channel';
import { useMorphingDialog } from '@/components/ui/morphing-dialog';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';

export function CreateForm() {
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
                <Select
                    value={String(formData.type)}
                    onValueChange={(value) => setFormData({ ...formData, type: Number(value) as ChannelType })}
                >
                    <SelectTrigger className="w-full bg-background border-border text-foreground">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={String(ChannelType.OpenAIChat)}>OpenAI Chat</SelectItem>
                        <SelectItem value={String(ChannelType.OpenAIResponse)}>OpenAI Response</SelectItem>
                        <SelectItem value={String(ChannelType.Anthropic)}>Anthropic</SelectItem>
                        <SelectItem value={String(ChannelType.OneAPI)}>OneAPI</SelectItem>
                    </SelectContent>
                </Select>
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
