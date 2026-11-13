import type { MarketChannelListing, MarketModel } from '@/api/market';

// 成本估算与对比的工具函数。抽成纯函数是为了能被直接测试:
// 成本算错是这类页面最贵的错误 —— 用户会拿它做报价, 差一个数量级就是真实的钱。

/** TokenVolume 是一次典型调用的用量: 输入输出各自的 token 数, 可选缓存读写。 */
export interface TokenVolume {
    input: number;
    output: number;
    cacheRead?: number;
    cacheWrite?: number;
}

/**
 * estimateCost 按挂牌的用户价估算一次调用的花费。
 *
 * 单位是这段代码最容易出错的地方: LLMPrice 的四个字段都是**每百万 token** 的单价,
 * 所以必须先除以 1e6 再乘用量。直接相乘会得到 100 万倍的答案, 而那个数字看起来仍然
 * 像一笔合理的花费, 不会有人一眼发现。
 *
 * 媒体模型(按张/按条)不走这条路径: 它们的计价单位不是 token, 用 token 估出来的数没有意义。
 * 调用方应先用 isMediaModel 分流。
 */
export function estimateCost(listing: MarketChannelListing, volume: TokenVolume): number {
    const price = listing.user_price;
    if (!price) return 0;
    const read = Math.max(0, volume.input);
    const output = Math.max(0, volume.output);
    const cacheRead = Math.max(0, volume.cacheRead ?? 0);
    const cacheWrite = Math.max(0, volume.cacheWrite ?? 0);
    return (read * price.input + output * price.output + cacheRead * price.cache_read + cacheWrite * price.cache_write) / 1_000_000;
}

/** isMediaModel 判断这是不是按张/按秒计价的媒体模型(不能按 token 估)。 */
export function isMediaModel(listing: MarketChannelListing): boolean {
    return (listing.kind === 'image' || listing.kind === 'video') && Boolean(listing.user_media);
}

/**
 * cheapestUsableListing 选出模型下可用于估算的那条挂牌: 可用且已定价的最便宜一条。
 *
 * 不可用的渠道不构成能买到的价格, 未定价的渠道没有价格可算 —— 用它们做估算等于给出一个
 * 用户根本付不出去的数字。
 */
export function cheapestUsableListing(model: MarketModel): MarketChannelListing | undefined {
    const usable = model.channels.filter((listing) => listing.available && !listing.unpriced && !isMediaModel(listing));
    if (usable.length === 0) return undefined;
    return usable.reduce((best, listing) => (blendedPrice(listing) < blendedPrice(best) ? listing : best));
}

/** blendedPrice 是混合口径的排序价: 与后端 SortPrice 同思路(入价与出价各占一份权重)。 */
export function blendedPrice(listing: MarketChannelListing): number {
    const price = listing.user_price;
    if (!price) return Number.POSITIVE_INFINITY;
    return price.input + price.output;
}

/**
 * compareByCost 按给定用量算出各模型的最低价, 供对比表按成本升序排列。
 * 没有可用报价的模型排在最后: 未知不是便宜。
 */
export function compareByCost(models: MarketModel[], volume: TokenVolume): Array<{ model: MarketModel; cost: number | undefined; listing: MarketChannelListing | undefined }> {
    return models
        .map((model) => {
            const listing = cheapestUsableListing(model);
            return { model, listing, cost: listing ? estimateCost(listing, volume) : undefined };
        })
        .sort((a, b) => {
            if (a.cost === undefined && b.cost === undefined) return a.model.name.localeCompare(b.model.name);
            if (a.cost === undefined) return 1;
            if (b.cost === undefined) return -1;
            return a.cost - b.cost;
        });
}
