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
    // 逐行也给一份口径: 同一模型的不同渠道样本量能差两个数量级, 由前端各自判阈值必然与服务端分叉。
    metrics_source: MarketMetricsSource;
    // 质量区间: 近 7 天的成功率/延迟/可用率各自落成区间文案。
    // 三个维度独立判定样本门槛, 故可能只出现其中一两个; 全不达标时整段省略。
    quality_band?: MarketQualityBand;
}

/** 一条挂牌的质量区间。只给区间不给精确值(与分享页同一道脱敏防线)。 */
export interface MarketQualityBand {
    success?: string; // 如 "99.5%"+" / "99%+" / "95%+" / "<95%"
    latency?: string; // 如 "<2s" / "2-5s" / ">5s"
    uptime?: string;  // 如 "99.9%+" / "99%+" / "95%+" / "<95%"
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
    // metrics_source 是界面分流的唯一依据。四档而不是两档:
    // live=本次进程的样本够; daily=本进程没跑过, 数字来自近 7 天持久化统计;
    // insufficient=有样本但不足门槛, 仍显示 —; none=无样本。
    // 只有 live/daily 才给数字 —— 把"跑过 1 次"当成可用成绩, 与把"没数据"当成 100% 成功同源。
    // daily 与 live 必须分开: 两者 WaitTime 口径不同源, 且"实时"与"近一周"是两种不同的承诺。
    metrics_source: MarketMetricsSource;
}

/** MarketMetricsSource 是成绩口径标注, 与后端 handlers 的 marketMetrics* 常量一一对应。 */
export type MarketMetricsSource = 'live' | 'daily' | 'insufficient' | 'none';

/**
 * hasNumericMetrics 判断这条记录的指标是否给出了数字。
 *
 * 单点在此而不在各组件里写 '=== 'live'': 'daily'(近 7 天回落)同样有达标样本、同样可比,
 * 只是来源不同。每处各自判一次, 新增来源时必然漏改某一处 —— 那处就会静默退回—,
 * 而界面上看不出是漏改还是真的没数据。
 */
export function hasNumericMetrics(source: MarketMetricsSource): boolean {
    return source === 'live' || source === 'daily';
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
