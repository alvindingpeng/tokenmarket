import { describe, expect, it } from 'vitest';
import { formatTokenCount } from './format';

// 档位约定: <1e6 实际 token(千位分组), 1e6~1e9 用 m, 1e9~1e12 用 b, 1e12 以上用 mb。
describe('adaptive token units', () => {
    it.each([
        [0, '0'], [999, '999'], [1000, '1,000'], [456000, '456,000'],
        [1e6, '1.0m'], [1.5e9, '1.5b'], [1e12, '1.0mb'],
        [999999, '999,999'], [999999999, '1.0b'], [-1, '0'], [NaN, '0'], [Infinity, '0'],
    ])('formats %s as %s', (value, expected) => expect(formatTokenCount(value)).toBe(expected));
    it('handles absent values', () => {
        expect(formatTokenCount(null)).toBe('0');
        expect(formatTokenCount(undefined)).toBe('0');
    });
});
