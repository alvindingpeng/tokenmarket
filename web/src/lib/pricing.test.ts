import { describe, expect, it } from 'vitest';
import { markupOf, userPriceOf } from './pricing';
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
