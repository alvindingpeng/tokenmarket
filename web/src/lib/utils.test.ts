import { describe, expect, it } from 'vitest';
import { cn, formatCount, formatMoney, formatTime } from './utils';

describe('cn', () => {
  it('后一个冲突类覆盖前一个', () => {
    expect(cn('p-2', 'p-4')).toBe('p-4');
  });
  it('非冲突类保留两侧', () => {
    expect(cn('p-2', 'text-red-500')).toBe('p-2 text-red-500');
  });
});

describe('formatCount', () => {
  it('undefined 回落为 0', () => {
    const r = formatCount(undefined);
    expect(r.raw).toBe(0);
    expect(r.formatted).toEqual({ value: '0.00', unit: ''});
  });
  it('千分位档位依次是 K/M/B', () => {
    expect(formatCount(1_000).formatted).toEqual({ value: '1.00', unit: 'K' });
    expect(formatCount(1_000_000).formatted).toEqual({ value: '1.00', unit: 'M' });
    expect(formatCount(2_500_000_000).formatted).toEqual({ value: '2.50', unit: 'B' });
  });
  it('小于 1000 的数原样保留两位小数', () => {
    expect(formatCount(500).formatted).toEqual({ value: '500.00', unit: ''});
  });
});

describe('formatMoney', () => {
  it('金额带 $ 单位, undefined 回落为 0', () => {
    expect(formatMoney(undefined).raw).toBe(0);
    expect(formatMoney(0).formatted).toEqual({ value: '0.00', unit: '$' });
  });
  it('百万/十亿档位带 M$/B$', () => {
    expect(formatMoney(1_000_000).formatted).toEqual({ value: '1.00', unit: 'M$' });
    expect(formatMoney(1_000_000_000).formatted).toEqual({ value: '1.00', unit: 'B$' });
  });
});

describe('formatTime', () => {
  it('ms 逐级换算到 d/h/m/s', () => {
    expect(formatTime(86_400_000).formatted).toEqual({ value: '1.00', unit: 'd' });
    expect(formatTime(3_600_000).formatted).toEqual({ value: '1.00', unit: 'h' });
    expect(formatTime(60_000).formatted).toEqual({ value: '1.00', unit: 'm' });
    expect(formatTime(1_000).formatted).toEqual({ value: '1.00', unit: 's' });
  });
  it('不足 1 秒按 ms 展示', () => {
    expect(formatTime(500).formatted).toEqual({ value: '500.00', unit: 'ms' });
    expect(formatTime(undefined).formatted).toEqual({ value: '0.00', unit: '' }); // 缺失值回落到首个单位(空), 不是 ms。
  });
});
