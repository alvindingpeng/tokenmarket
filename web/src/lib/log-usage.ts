import type { RelayUsage } from '@/api/log';

// usageMetrics 是日志界面统一的口径来源。后端把 Anthropic 系上游的 usage 归一化时,
// prompt_tokens 是「毛输入」—— 它已经把 cache_read 与 cache_creation(写缓存)都算进去了
// (vendored axonhub/llm 的 convertToLlmUsage), 而计费桶 internal/relay/state.go 的
// usageBuckets 又把写缓存单独按 CacheWrite 单价计价。所以界面若直接拿 cached/prompt
// 当命中率, 会把「写缓存」这一笔全新成本当成未命中的分母去稀释命中率, 且各列相加会超过
// 真实输入。这里把三个口径固定成一组纯函数, 与后端保持一致:
//   新鲜输入 = prompt - 缓存读 - 缓存写   (按"读"计价的部分)
//   可服务输入 = prompt - 缓存写          (真正可能被缓存命中的部分)
//   命中率 = 缓存读 / 可服务输入

// clampTokens 把缺失、负数或非有限值收敛为可用的 Token 数; 展示层宁可少算也不能出现 NaN。
function clampTokens(value: number | undefined | null): number {
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return 0;
    return value;
}

// detailTokens 从统一用量的 prompt_tokens_details 中取出缓存读与缓存写 Token 数。
export function detailTokens(usage: RelayUsage | undefined | null): { cached: number; cacheWrite: number } {
    const details = usage?.prompt_tokens_details;
    return {
        cached: clampTokens(details?.cached_tokens),
        cacheWrite: clampTokens(details?.write_cached_tokens),
    };
}

// freshInputTokens 返回未命中缓存、按"读"计价的新鲜输入 Token 数。
export function freshInputTokens(promptTokens: number, cached: number, cacheWrite: number): number {
    const prompt = clampTokens(promptTokens);
    return Math.max(0, prompt - clampTokens(cached) - clampTokens(cacheWrite));
}

// cacheHitRatePercent 返回输入缓存命中率(百分数, 未取整)。分母是可能被缓存命中的输入,
// 即毛输入扣掉写缓存; 无此类输入时命中率为零, 结果上限 100。
export function cacheHitRatePercent(promptTokens: number, cached: number, cacheWrite: number): number {
    const servable = clampTokens(promptTokens) - clampTokens(cacheWrite);
    if (servable <= 0) return 0;
    const rate = (clampTokens(cached) / servable) * 100;
    return Math.min(100, Math.max(0, rate));
}

// formatCacheHitRate 固定保留一位小数: 一位既能区分 49.8% 与 49.9% 这类相邻档位,
// 又不至于让日志栅格里的这一列宽度抖动。
export function formatCacheHitRate(percent: number): string {
    return `${clampTokens(percent).toFixed(1)}%`;
}
