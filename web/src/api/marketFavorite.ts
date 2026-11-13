import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';

// 广场收藏: 只存「我关心哪些模型名」, 挂牌数据一律走目录接口。
// 这里刻意不缓存一份挂牌快照 —— 两份口径迟早对不上(价格变了、渠道停了), 收藏只是索引。

const FAVORITES_KEY = ['market', 'favorites'] as const;

/** marketFavoritesQueryOptions 供卡片渲染星标与「只看收藏」筛选。 */
export function marketFavoritesQueryOptions() {
    return {
        queryKey: FAVORITES_KEY,
        queryFn: () => apiRequest<{ models: string[] }>('/api/v1/market/favorites').then((data) => data.models),
        // 收藏是用户自己的操作结果, 不会因为别人改动而变化; 只需在切换页面后重取一次。
        staleTime: 30_000,
    };
}

/** useMarketFavorites 读当前用户的收藏模型名。 */
export function useMarketFavorites() {
    return useQuery(marketFavoritesQueryOptions());
}

/** useToggleMarketFavorite 收藏/取消收藏。 */
export function useToggleMarketFavorite() {
    return useMutation({
        mutationFn: ({ model, favorited }: { model: string; favorited: boolean }) =>
            favorited
                ? apiRequest<void>('/api/v1/market/favorites/' + encodeURIComponent(model), { method: 'DELETE' })
                : apiRequest<void>('/api/v1/market/favorites', { method: 'POST', body: { model } }),
        // 乐观更新: 星标必须立刻响应点击。等一轮网络往返再变色, 用户会以为没点上而再点一次。
        onMutate: async ({ model, favorited }) => {
            await queryClient.cancelQueries({ queryKey: FAVORITES_KEY });
            const previous = queryClient.getQueryData<string[]>(FAVORITES_KEY) ?? [];
            const next = favorited ? previous.filter((name) => name !== model) : [...previous, model];
            queryClient.setQueryData(FAVORITES_KEY, next);
            return { previous };
        },
        // 失败回滚: 否则星标停在「已收藏」而服务端没有, 刷新后消失 —— 比不让点更让人困惑。
        onError: (_error, _vars, context) => {
            if (context?.previous) queryClient.setQueryData(FAVORITES_KEY, context.previous);
        },
        onSettled: () => void queryClient.invalidateQueries({ queryKey: FAVORITES_KEY }),
    });
}

