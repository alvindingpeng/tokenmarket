import { describe, expect, it } from 'vitest';
import type { UserStatsOverview } from '@/api/user-stats';
import { isCoverageLimited, summarizeCacheHitRate, summarizeSuccess } from './success';

// 缓存命中率与成功率共用一套哨兵规则: 没有样本时显示 "—", 绝不显示 0.0%。
// 后端在分母 <= 0 时只能返回 0(否则是 0/0 的 NaN), 这个 0 是哨兵不是真值 ——
// 直接渲染会把"这段区间没数据"读成"所有请求都没命中缓存"。

// mkOverview 只关心词元三项与命中率, 其余字段给零值即可(纯函数不读它们)。
// output 单独给参数: 它进 total_tokens 但不进缓存命中率的分母, 混进 input 会让测试失去分辨力。
function mkOverview(tokens: { input: number; cacheRead: number; cacheWrite: number }, rate: number): UserStatsOverview {
    return {
        total_requests: 0,
        success_rate: 0,
        total_cost: 0,
        total_tokens: tokens.input + tokens.cacheRead + tokens.cacheWrite,
        request_total: 0,
        request_success: 0,
        request_failed: 0,
        request_canceled: 0,
        request_coverage_start: '',
        request_retention_days: 7,
        input_tokens: tokens.input,
        output_tokens: 0,
        cache_read_tokens: tokens.cacheRead,
        cache_write_tokens: tokens.cacheWrite,
        cache_hit_rate: rate,
    };
}

describe('summarizeCacheHitRate 零样本走空态', () => {
    it('三类词元全为 0 时显示哨兵 "—", 而不是 0.0%', () => {
        expect(summarizeCacheHitRate(mkOverview({ input: 0, cacheRead: 0, cacheWrite: 0 }, 0))).toBe('—');
    });

    it('有词元样本时显示真实百分比(一位小数)', () => {
        // 75.0% = 300000 / (300000 + 100000): 雨数只取 cache_read+input, 输出不进分母。
        expect(summarizeCacheHitRate(mkOverview({ input: 100000, cacheRead: 300000, cacheWrite: 0 }, 75))).toBe('75.0%');
    });

    it('只有写缓存(无人读命中)时仍是真实样本: 显示 0.0% 而非 "—"', () => {
        // 0.0% 在这里是真的: 确实跑了调用, 只是没有一次命中读缓存。两者必须区分开。
        expect(summarizeCacheHitRate(mkOverview({ input: 0, cacheRead: 0, cacheWrite: 120 }, 0))).toBe('0.0%');
    });

    it('只有普通输入时也按真实样本处理', () => {
        expect(summarizeCacheHitRate(mkOverview({ input: 800, cacheRead: 0, cacheWrite: 0 }, 0))).toBe('0.0%');
    });
});

describe('summarizeSuccess 与缓存命中率同规则', () => {
    it('无成功/失败样本时显示 "—"', () => {
        expect(summarizeSuccess(mkOverview({ input: 0, cacheRead: 0, cacheWrite: 0 }, 0)).value).toBe('—');
    });
});

describe('isCoverageLimited', () => {
    it('空串(区间内无日志)不算覆盖不全', () => {
        expect(isCoverageLimited('', '2026-01-01')).toBe(false);
    });
});
