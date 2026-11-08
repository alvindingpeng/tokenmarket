import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';
import { POLL_NORMAL } from '@/lib/polling';
import type { LLMPrice } from './model';

// 模型广场(P1)的只读挂牌表。字段与后端 handlers/market.go 的对外形状一一对应:
// 供给侧事实(价格/编码/可用)由 op 出, 成功率与延迟由 relay 的进程内指标在处理器侧合并。

/** 媒体单价: 生图按张、生视频按条/按秒; 文本模型为零值。 */
export interface MarketMediaPrice {
    per_image?: number;
    per_video?: number;
    per_second?: number;
    resolution?: Record<string, number>;
}

/** 广场上一条"渠道 × 模型"的挂牌。 */
export interface MarketChannelListing {
    channel_grant_id: number; // 加进自有分组时提交的就是它。
    channel_model_id: number;
    channel_code: string; // 形如 "#1001"。
    available: boolean;
    status_reason?: '' | 'fx_missing' | 'disabled' | 'key_disabled' | 'breaker' | 'unpriced';
    user_price: LLMPrice;
    user_media?: MarketMediaPrice;
    kind?: '' | 'image' | 'video';
    price_currency?: string;
    fx_missing?: boolean;
    unpriced?: boolean;
    wait_ms?: number;
    success?: number;
    samples: number;
}

/** 按小写模型名折叠后的一屏一行。 */
export interface MarketModel {
    name: string;
    brand: string;
    channels: MarketChannelListing[];
    in_groups: number[];
    wait_ms?: number;
    success?: number;
    samples: number;
    // metrics_source 是界面分流的唯一依据: none 表示无样本, 必须显示占位而不是 100% 成功。
    metrics_source: 'live' | 'none';
}

export interface MarketCatalog {
    models: MarketModel[];
    // brands 由后端在筛选之前汇总, 故任何筛选组合下都是全集: 下拉不会因选中某项而自己收窄。
    brands: string[];
    updated_at: string;
}

/** 广场的排序维度。每个维度只有唯一方向, 故没有 order 参数。 */
export type MarketSort = 'price' | 'success' | 'latency' | 'name';
/** 形态筛选: 空表示不限。 */
export type MarketKind = '' | 'text' | 'image' | 'video';

/** MarketQuery 是广场的查询条件; 排序与筛选一律交给服务端, 前端不复算一遍。 */
export interface MarketQuery {
    q: string;
    brands: string[];
    kind: MarketKind;
    sort: MarketSort;
}

// DEFAULT_MARKET_SORT 是首屏默认维度: 用户最关心"能不能跑通", 故先看成功率。
// 无样本的模型一律置底(后端保证), 于是首屏既是最可靠的那批, 又不会因为没数据而霸榜。
export const DEFAULT_MARKET_SORT: MarketSort = 'success';

// marketCatalogPath 拼出带查询参数的路径。品牌用重复 key(brand=a&brand=b)而不是逗号分隔:
// 品牌键本身不含逗号, 但重复 key 是服务端 queryValues 明确支持的形式, 也与 curl 手测一致。
export function marketCatalogPath(query: MarketQuery): string {
    const params = new URLSearchParams();
    if (query.q.trim() !== '') params.set('q', query.q.trim());
    for (const brand of query.brands) params.append('brand', brand);
    if (query.kind !== '') params.set('kind', query.kind);
    if (query.sort !== 'name') params.set('sort', query.sort);
    const suffix = params.toString();
    return '/api/v1/market/models' + (suffix ? '?' + suffix : '');
}

// marketCatalogQueryOptions 供页面查询与启动预取共享广场定义。
// 查询条件进入 queryKey: 换筛选就是换一份数据, 同时旧条件的结果留在缓存里, 切回去不用重拉。
export function marketCatalogQueryOptions(query: MarketQuery) {
    return {
        queryKey: ['market', 'models', query.q.trim(), query.brands.slice().sort(), query.kind, query.sort],
        queryFn: () => apiRequest<MarketCatalog>(marketCatalogPath(query)),
    };
}

/**
 * useMarketCatalog 获取广场挂牌表。
 *
 * 轮询用 POLL_NORMAL 而非 POLL_FAST: 价格与可售集合变动少, 变的是指标;
 * 每 5 秒重拉约 18 KB 收益很低。标签页不可见时 react-query 会自动暂停。
 */
export function useMarketCatalog(query: MarketQuery) {
    return useQuery({
        ...marketCatalogQueryOptions(query),
        refetchInterval: POLL_NORMAL,
        refetchOnMount: 'always',
    });
}

/** channelPrice 返回一条挂牌的代表单价: 文本取混合 token 价, 媒体取媒体价。 */
export function channelPrice(listing: MarketChannelListing): number {
    const blended = (listing.user_price.input + listing.user_price.output) / 2;
    if (blended !== 0) return blended;
    const media = listing.user_media;
    if (!media) return 0;
    return media.per_image || media.per_second || media.per_video || 0;
}

/** cheapestListing 返回模型下最便宜的可用挂牌; 没有可用报价时返回 undefined。 */
export function cheapestListing(model: MarketModel): MarketChannelListing | undefined {
    let best: MarketChannelListing | undefined;
    for (const listing of model.channels) {
        if (!listing.available) continue;
        const price = channelPrice(listing);
        if (price <= 0) continue;
        if (!best || price < channelPrice(best)) best = listing;
    }
    return best;
}

/** availableCount 返回可用渠道数; 界面用它区分"有几家在卖"与"几家现在真能跑"。 */
export function availableCount(model: MarketModel): number {
    return model.channels.filter((listing) => listing.available).length;
}
