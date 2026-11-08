import { describe, expect, it } from 'vitest';
import { cashflowFxRate, CASHFLOW_CURRENCIES, formatFxSnapshotDay, fxReconciliationBucketOf, fxRangeToCompactDate, type AdminFxReconciliation } from './fx';

describe('formatFxSnapshotDay', () => {
    it('把 YYYYMMDD 规范化为 ISO 日期', () => {
        expect(formatFxSnapshotDay('20260107')).toBe('2026-01-07');
    });
    it('兼容已是 ISO 形式的输入并放行其它格式', () => {
        expect(formatFxSnapshotDay('2026-01-07')).toBe('2026-01-07');
        expect(formatFxSnapshotDay('')).toBe('');
        expect(formatFxSnapshotDay(undefined)).toBe('');
    });
});

describe('cashflowFxRate', () => {
    const rates = [
        { from: 'USD', to: 'CNY', rate: 7.2, day: '20260107', source: 'manual' },
        { from: 'USD', to: 'EUR', rate: 0.9, day: '20260107', source: 'ecb' },
    ];
    it('取 USD→quote 快照', () => {
        expect(cashflowFxRate(rates, 'cny')?.rate).toBe(7.2);
        expect(cashflowFxRate(rates, 'EUR')?.source).toBe('ecb');
    });
    it('USD 恒为 1, 不查表', () => {
        expect(cashflowFxRate([], 'USD')?.rate).toBe(1);
    });
    it('CNY 缺快照时回退服务端静态汇率并标记 configured', () => {
        const item = cashflowFxRate([], 'CNY', 7.0);
        expect(item?.rate).toBe(7.0);
        expect(item?.source).toBe('configured');
    });
    it('其它币种缺快照不猜数', () => {
        expect(cashflowFxRate([], 'GBP')).toBeUndefined();
        expect(cashflowFxRate([], 'CNY')).toBeUndefined();
    });
    it('扩展币种(AUD/CAD/KRW/CHF/THB)与后端支持集合同步可选', () => {
        // CASHFLOW_CURRENCIES 与后端 model.ValidPriceCurrency 对齐: 现有六种 + 扩展五种。
        expect([...CASHFLOW_CURRENCIES]).toEqual(['CNY', 'USD', 'EUR', 'GBP', 'JPY', 'HKD', 'SGD', 'AUD', 'CAD', 'KRW', 'CHF', 'THB']);
        const extendedRates = [
            { from: 'USD', to: 'AUD', rate: 1.52, day: '20260107', source: 'ecb' },
            { from: 'USD', to: 'KRW', rate: 1380, day: '20260107', source: 'ecb' },
        ];
        expect(cashflowFxRate(extendedRates, 'aud')?.rate).toBe(1.52);
        expect(cashflowFxRate(extendedRates, 'KRW')?.rate).toBe(1380);
        expect(cashflowFxRate(extendedRates, 'CHF')).toBeUndefined(); // 无牌价仍不猜数
    });
});

describe('fxReconciliationBucketOf', () => {
    it('嵌套 ok/stale/missing 桶优先', () => {
        const report: AdminFxReconciliation = { from: 'a', to: 'b', ok: { count: 3, amount: 12.5 }, stale: { count: 1, amount: 2 } };
        expect(fxReconciliationBucketOf(report, 'ok')).toEqual({ count: 3, amount: 12.5 });
        expect(fxReconciliationBucketOf(report, 'stale')).toEqual({ count: 1, amount: 2 });
        expect(fxReconciliationBucketOf(report, 'missing')).toEqual({ count: 0, amount: 0 });
    });
    it('扁平 ok_count 等字段兜底', () => {
        const report: AdminFxReconciliation = { from: 'a', to: 'b', ok_count: 5, ok_amount: 9, missing_count: 2, missing_amount: 4 };
        expect(fxReconciliationBucketOf(report, 'ok')).toEqual({ count: 5, amount: 9 });
        expect(fxReconciliationBucketOf(report, 'missing')).toEqual({ count: 2, amount: 4 });
    });
    it('支持 usd_native 嵌套桶与扁平字段', () => {
        const nested: AdminFxReconciliation = { from: 'a', to: 'b', usd_native: { count: 7, amount: 21 } };
        const flat: AdminFxReconciliation = { from: 'a', to: 'b', usd_native_count: 4, usd_native_amount: 10 };
        expect(fxReconciliationBucketOf(nested, 'usd_native')).toEqual({ count: 7, amount: 21 });
        expect(fxReconciliationBucketOf(flat, 'usd_native')).toEqual({ count: 4, amount: 10 });
    });
    it('空报表按 0 计', () => {
        expect(fxReconciliationBucketOf(undefined, 'ok')).toEqual({ count: 0, amount: 0 });
    });
});

describe('fxRangeToCompactDate', () => {
    it('YYYY-MM-DD 转 YYYYMMDD, 空值回空串', () => {
        expect(fxRangeToCompactDate('2026-01-07')).toBe('20260107');
        expect(fxRangeToCompactDate('')).toBe('');
    });
});
