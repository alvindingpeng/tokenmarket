import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * 四类单价（每百万 token）：输入读、输出写、缓存读、缓存写；与后端 model.LLMPrice 对齐。
 */
export type LLMPrice = {
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
};

/**
 * 渠道模型上架条目：listed 决定用户可见性，supply_price 为供货价（渠道商收入口径）。
 * 未设置价格的已上架模型按 0 计费，不回退到全局价格表。
 */
/**
 * 媒体定价(每张/每秒/分档): 仅媒体形态模型使用, 与 token 四类价互不干扰。
 */
export type MediaPrice = {
    per_image?: number;
    per_second?: number;
    resolution?: Record<string, number>;
};

/**
 * 渠道模型上架条目：listed 决定用户可见性，supply_price 为供货价（渠道商收入口径）。
 * 未设置价格的已上架模型按 0 计费，不回退到全局价格表。
 * kind === 'image' 的行按 media_supply.per_image 计价(元/张)。
 */
export type ChannelModelListing = {
    name: string;
    listed: boolean;
    supply_price: LLMPrice;
    media_supply?: MediaPrice;
    kind?: string;
};

/**
 * 发布请求：shared 为 true 发布（首次生成唯一编码，重复发布沿用），false 收回。
 */
export type ChannelPublishRequest = {
    id: number;
    shared: boolean;
};

/**
 * 发布结果：share_code 为对外展示的渠道名称（大小写字母与数字组成的唯一编码）。
 */
export type ChannelPublishResult = {
    id: number;
    share_code: string;
    shared: boolean;
};

/**
 * useChannelListings 读取渠道全部模型的上架状态与供货价（管理员与渠道商）。
 */
export function useChannelListings(channelID: number, enabled = true) {
    return useQuery({
        queryKey: ['channels', 'models', channelID],
        queryFn: () => apiRequest<ChannelModelListing[]>(`/api/v1/channel/models/${channelID}`),
        enabled,
    });
}

/**
 * usePublishChannel 发布或下架渠道。
 */
export function usePublishChannel() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: ChannelPublishRequest) =>
            apiRequest<ChannelPublishResult>('/api/v1/channel/publish', {
                method: 'POST',
                body: data,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['channels', 'stats'] });
        },
    });
}

/**
 * useUpdateChannelListings 批量保存模型上架状态与供货价；未出现在提交里的模型保持原状。
 */
export function useUpdateChannelListings() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { id: number; listings: ChannelModelListing[] }) =>
            apiRequest<string>('/api/v1/channel/models/update', {
                method: 'POST',
                body: data,
            }),
        onSuccess: (_data, variables) => {
            void queryClient.invalidateQueries({ queryKey: ['channels', 'models', variables.id] });
            void queryClient.invalidateQueries({ queryKey: ['channels', 'grants'] });
        },
    });
}
