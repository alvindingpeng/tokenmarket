import { describe, expect, it } from 'vitest';
import type { MarketChannelListing, MarketModel } from '@/api/market';
import { blendedPrice, cheapestUsableListing, compareByCost, estimateCost, isMediaModel } from './compare';

// 成本估算的单元测试。
//
// 这里最要紧的一条是**单位**: 后端 LLMPrice 的四个字段都是每百万 token 的单价。少除一次 1e6
// 会得到一个 100 万倍的答案, 而那个数字看起来仍然像"一笔合理的花费" —— 这种错误不会在界面上
// 暴露, 只会让拿着估算去报价的人错得离谱。所以单位必须由测试钉死。

const price = (input: number, output: number, cacheRead = 0, cacheWrite = 0) => ({
    input, output, cache_read: cacheRead, cache_write: cacheWrite,
});

const listing = (overrides: Partial<MarketChannelListing> = {}): MarketChannelListing => ({
    channel_grant_id: 1,
    channel_model_id: 11,
    channel_code: '#1001',
    available: true,
    user_price: price(3, 15),
    samples: 40,
    metrics_source: 'live',
    ...overrides,
});

const model = (name: string, listings: MarketChannelListing[]): MarketModel => ({
    name,
    brand: 'anthropic',
    channels: listings,
    in_groups: [],
    samples: 40,
    metrics_source: 'live',
});

describe('estimateCost 成本估算', () => {
    it('按每百万 token 的单价计算, 不是按单个 token', () => {
        // 入价 $3/百万, 出价 $15/百万; 用量 2000 入 / 500 出。
        // 期望 (2000*3 + 500*15)/1e6 = 13500/1e6 = 0.0135。
        const cost = estimateCost(listing(), { input: 2000, output: 500 });
        expect(cost).toBeCloseTo(0.0135, 12);
        // 若漏除 1e6 会得到 13500 —— 用这个断言把单位钉死。
        expect(cost).toBeLessThan(1);
    });

    it('计入缓存读写单价', () => {
        // 缓存读 $0.3/百万 × 10000 = 3000; 缓存写 $3.75/百万 × 2000 = 7500; 合计 10500/1e6。
        const cost = estimateCost(listing({ user_price: price(3, 15, 0.3, 3.75) }), {
            input: 0, output: 0, cacheRead: 10000, cacheWrite: 2000,
        });
        expect(cost).toBeCloseTo(0.0105, 12);
    });

    it('负用量按零处理, 不会算出一个负数账单', () => {
        expect(estimateCost(listing(), { input: -5, output: -5 })).toBe(0);
    });

    it('零用量为零花费', () => {
        expect(estimateCost(listing(), { input: 0, output: 0 })).toBe(0);
    });
});

describe('cheapestUsableListing 选价', () => {
    it('跳过不可用与未定价的渠道', () => {
        const picked = cheapestUsableListing(model('m', [
            listing({ channel_grant_id: 1, available: false, user_price: price(1, 1) }),
            listing({ channel_grant_id: 2, unpriced: true, user_price: price(2, 2) }),
            listing({ channel_grant_id: 3, user_price: price(4, 4) }),
        ]));
        expect(picked?.channel_grant_id).toBe(3);
    });

    it('不可用渠道再便宜也不构成能买到的价格', () => {
        // 只留一条不可用的极低价渠道时应当没有可估算项, 而不是拿它算出 $0.000002。
        const picked = cheapestUsableListing(model('m', [listing({ available: false, user_price: price(1, 1) })]));
        expect(picked).toBeUndefined();
    });

    it('媒体渠道不参与 token 估算', () => {
        const picked = cheapestUsableListing(model('img', [
            listing({ kind: 'image', user_media: { per_image: 0.02 }, user_price: price(0, 0) }),
        ]));
        expect(picked).toBeUndefined();
    });
});

describe('compareByCost 对比排序', () => {
    it('按成本升序, 无报价的排最后', () => {
        const rows = compareByCost(
            [model('expensive', [listing({ user_price: price(10, 30) })]),
             model('cheap', [listing({ user_price: price(1, 2) })]),
             model('unknown', [listing({ available: false })]),],
            { input: 1000, output: 1000 },
        );
        expect(rows.map((row) => row.model.name)).toEqual(['cheap', 'expensive', 'unknown']);
        expect(rows[2].cost).toBeUndefined();
    });

    it('全部无报价时按名称稳定排序, 不抖动', () => {
        const rows = compareByCost(
            [model('zeta', []), model('alpha', [])],
            { input: 1, output: 1 },
        );
        expect(rows.map((row) => row.model.name)).toEqual(['alpha', 'zeta']);
    });
});

describe('blendedPrice / isMediaModel', () => {
    it('混合口径是入价加出价', () => {
        expect(blendedPrice(listing({ user_price: price(3, 15) }))).toBe(18);
    });

    it('缺价格时排到最后而不是当零', () => {
        expect(blendedPrice(listing({ user_price: undefined as never }))).toBe(Number.POSITIVE_INFINITY);
    });

    it('文本模型不算媒体模型', () => {
        expect(isMediaModel(listing())).toBe(false);
    });
});
