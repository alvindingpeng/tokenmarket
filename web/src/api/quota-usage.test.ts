import { describe, expect, it } from 'vitest';
import { quotaLevel, quotaUsageAdminQueryOptions, quotaUsageSelfPath, usageRatio, quotaUsageSelfQueryOptions } from './quota-usage';

describe('quota usage 路径与查询键', () => {
    it('自助与管理员走不同路径', () => {
        expect(quotaUsageSelfPath).toBe('/api/v1/quota-usage');
        expect(quotaUsageAdminQueryOptions(7).queryKey).toEqual(['quota-usage', 'admin', 7]);
        expect(quotaUsageSelfQueryOptions(3).queryKey).toEqual(['quota-usage', 'self', 3]);
    });

    it('管理员查询在 user_id 为 0 时不发请求', () => {
        // 0 会撞后端 required 校验返回 400, 与其发一个必然失败的请求不如不发。
        expect(quotaUsageAdminQueryOptions(0).enabled).toBe(false);
        expect(quotaUsageAdminQueryOptions(5).enabled).toBe(true);
    });
});

describe('usageRatio 与 quotaLevel', () => {
    it('额度为 0 表示不限制, 比例按 0 且等级为 unlimited', () => {
        expect(usageRatio(0, 999)).toBe(0);
        expect(quotaLevel(0, 999)).toBe('unlimited');
    });

    it('超限时比例封顶为 1, 进度条不会溢出', () => {
        expect(usageRatio(10, 25)).toBe(1);
        expect(quotaLevel(10, 25)).toBe('danger');
    });

    it('按 70% 与 90% 两个边界分级', () => {
        expect(quotaLevel(10, 6.9)).toBe('ok');
        expect(quotaLevel(10, 7)).toBe('warn');
        expect(quotaLevel(10, 8.9)).toBe('warn');
        expect(quotaLevel(10, 9)).toBe('danger');
    });

    it('常规比例计算', () => {
        expect(usageRatio(20, 5)).toBeCloseTo(0.25);
        expect(usageRatio(1000, 250)).toBeCloseTo(0.25);
    });
});
