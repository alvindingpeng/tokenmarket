import { describe, expect, it } from 'vitest';
import { formatTokenCount } from './format';

describe('adaptive token units', () => {
    it.each([
        [0, '0'], [999, '999'], [1000, '1.0K'], [456000, '456.0K'],
        [1e6, '1.0M'], [1.5e9, '1.5B'], [1e12, '1.0KB'], [1e15, '1.0MB'],
        [999999, '1.0M'], [999999999, '1.0B'], [-1, '0'], [NaN, '0'], [Infinity, '0'],
    ])('formats %s as %s', (value, expected) => expect(formatTokenCount(value)).toBe(expected));
    it('handles absent values', () => {
        expect(formatTokenCount(null)).toBe('0');
        expect(formatTokenCount(undefined)).toBe('0');
    });
});
