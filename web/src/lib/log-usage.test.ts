import { describe, expect, it } from 'vitest';
import { cacheHitRatePercent, detailTokens, formatCacheHitRate, freshInputTokens } from './log-usage';

// 生产实测样本(2026-10-08 gpt-6-sol, 走 Anthropic 显式缓存写入):
// 毛输入 193993 里 182377 是写缓存、11613 是命中, 真正可被命中的输入只有 193993-182377=11616。
// 旧口径 11613/193993 显示 6%, 与后端计费桶口径 cache_read/(prompt-cache_write)=99.97% 差约 94 个百分点。
describe('cacheHitRatePercent', () => {
    it('命中率分母扣掉写缓存, 与后端 usageBuckets 同口径', () => {
        expect(cacheHitRatePercent(193993, 11613, 182377)).toBeCloseTo(99.97, 2);
    });
    it('无写缓存时退化为 cached/prompt', () => {
        expect(cacheHitRatePercent(1000, 250, 0)).toBeCloseTo(25, 6);
    });
    it('几乎整轮命中且只有少量写缓存时接近 100', () => {
        expect(cacheHitRatePercent(85226, 83167, 2056)).toBeCloseTo(99.996, 3);
    });
    it('纯写缓存(首次建缓存)命中率为 0', () => {
        expect(cacheHitRatePercent(210351, 0, 210348)).toBe(0);
    });
    it('分母为零或输入缺失时为零, 不出现 NaN/Infinity', () => {
        expect(cacheHitRatePercent(0, 0, 0)).toBe(0);
        expect(cacheHitRatePercent(100, 100, 100)).toBe(0);
        expect(cacheHitRatePercent(-5, 3, 1)).toBe(0);
        expect(Number.isNaN(cacheHitRatePercent(Number.NaN, 1, 1))).toBe(false);
    });
    it('异常数据(命中+写>毛输入)封顶 100 而不超过', () => {
        expect(cacheHitRatePercent(100, 90, 50)).toBe(100);
    });
});

describe('freshInputTokens', () => {
    it('同时扣掉缓存读与缓存写, 三列相加不再超过真实输入', () => {
        expect(freshInputTokens(193993, 11613, 182377)).toBe(3);
        expect(freshInputTokens(1000, 250, 0)).toBe(750);
    });
    it('不会算出负数', () => {
        expect(freshInputTokens(100, 90, 50)).toBe(0);
        expect(freshInputTokens(0, 0, 0)).toBe(0);
    });
});

describe('detailTokens', () => {
    it('details 缺失或整段为空时回落 0', () => {
        expect(detailTokens(undefined)).toEqual({ cached: 0, cacheWrite: 0 });
        expect(detailTokens({ prompt_tokens: 1, completion_tokens: 1, total_tokens: 2, prompt_tokens_details: null })).toEqual({ cached: 0, cacheWrite: 0 });
    });
    it('write_cached_tokens 为可选字段, 缺省按 0', () => {
        expect(detailTokens({ prompt_tokens: 1, completion_tokens: 1, total_tokens: 2, prompt_tokens_details: { cached_tokens: 7 } })).toEqual({ cached: 7, cacheWrite: 0 });
    });
});

describe('formatCacheHitRate', () => {
    it('保留一位小数, 能区分 49.8 与 49.9', () => {
        expect(formatCacheHitRate(49.84)).toBe('49.8%');
        expect(formatCacheHitRate(49.86)).toBe('49.9%');
        expect(formatCacheHitRate(100)).toBe('100.0%');
        expect(formatCacheHitRate(0)).toBe('0.0%');
    });
    it('生产样本 6% 修正为 100.0%', () => {
        expect(formatCacheHitRate(cacheHitRatePercent(193993, 11613, 182377))).toBe('100.0%');
        expect(formatCacheHitRate(cacheHitRatePercent(158060, 78848, 0))).toBe('49.9%');
    });
});
