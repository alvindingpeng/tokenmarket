import { describe, expect, it } from 'vitest';
import { formatCashflowAmount, isZeroDecimalCurrency } from './currency';

describe('isZeroDecimalCurrency', () => {
    it('JPY/KRW 为零小数币种, 大小写不敏感', () => {
        expect(isZeroDecimalCurrency('JPY')).toBe(true);
        expect(isZeroDecimalCurrency('jpy')).toBe(true);
        expect(isZeroDecimalCurrency('KRW')).toBe(true);
    });
    it('其余币种非零小数, 空值安全', () => {
        expect(isZeroDecimalCurrency('CNY')).toBe(false);
        expect(isZeroDecimalCurrency('USD')).toBe(false);
        expect(isZeroDecimalCurrency('')).toBe(false);
        expect(isZeroDecimalCurrency(undefined)).toBe(false);
    });
});

describe('formatCashflowAmount', () => {
    it('零小数币种按整数 + 千分位展示', () => {
        expect(formatCashflowAmount(15000, 'JPY')).toBe('15,000');
        expect(formatCashflowAmount(1234.6, 'KRW')).toBe('1,235'); // 四舍五入
        expect(formatCashflowAmount(0, 'JPY')).toBe('0');
    });
    it('其余币种沿用原口径(最多 6 位小数去尾零)', () => {
        expect(formatCashflowAmount(12.5, 'CNY')).toBe('12.5');
        expect(formatCashflowAmount(99, 'USD')).toBe('99');
        expect(formatCashflowAmount(712.8, 'CNY')).toBe('712.8');
    });
});
