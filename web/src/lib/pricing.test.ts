import { describe, expect, it } from 'vitest';
import { fxRateIsStale, ledgerRateOf, markupOf, userPriceInLedger, userPriceOf } from './pricing';
import { SettingKey, type Setting } from '@/api/setting';

const withMarkup = (value: string | undefined): Setting[] =>
  value === undefined ? [] : [{ key: SettingKey.MarkupRatio, value } as Setting];

describe('markupOf', () => {
  it('读取上浮比例', () => {
    expect(markupOf(withMarkup('0.25'))).toBe(0.25);
  });
  it('缺设置项回落 0', () => {
    expect(markupOf(undefined)).toBe(0);
    expect(markupOf([])).toBe(0);
  });
  it('非法值/负数回落 0 而非 NaN', () => {
    expect(markupOf(withMarkup('abc'))).toBe(0);
    expect(markupOf(withMarkup('-0.5'))).toBe(0);
    expect(markupOf(withMarkup('0'))).toBe(0);
  });
});

describe('ledgerRateOf', () => {
  it('把原币报价按 1 USD = quote 牌价折回 USD', () => {
    expect(ledgerRateOf([{ to: 'CNY', rate: 7.2 }], 'CNY')).toBeCloseTo(1 / 7.2);
  });
  it('USD/空币种不折算, 缺牌价不猜汇率', () => {
    expect(ledgerRateOf([], 'USD')).toBe(1);
    expect(ledgerRateOf([], undefined)).toBe(1);
    expect(ledgerRateOf([], 'EUR')).toBeUndefined();
  });
});

describe('userPriceInLedger', () => {
  it('折算后再上浮', () => {
    expect(userPriceInLedger(72, 'CNY', [{ to: 'CNY', rate: 7.2 }], 0.2)).toBeCloseTo(12);
  });
  it('缺牌价返回 undefined', () => {
    expect(userPriceInLedger(10, 'GBP', [], 0.2)).toBeUndefined();
  });
});

describe('fxRateIsStale', () => {
  const now = Date.parse('2026-11-07T00:00:00');
  it('2 天内视为正常', () => {
    expect(fxRateIsStale('20261107', now)).toBe(false);
    expect(fxRateIsStale('20261105', now)).toBe(false);
  });
  it('超过 2 天或非法/未来日期视为陈旧', () => {
    expect(fxRateIsStale('20261104', now)).toBe(true);
    expect(fxRateIsStale('20261108', now)).toBe(true);
    expect(fxRateIsStale('bad', now)).toBe(true);
  });
});

describe('userPriceOf', () => {
  it('无上浮时用户价等于供货价', () => {
    expect(userPriceOf(1.0, 0)).toBe(1.0);
  });
  it('上浮 25% 后为 1.25 倍', () => {
    expect(userPriceOf(2, 0.25)).toBe(2.5);
  });
  it('零供货价仍是 0', () => {
    expect(userPriceOf(0, 0.3)).toBe(0);
  });
});
