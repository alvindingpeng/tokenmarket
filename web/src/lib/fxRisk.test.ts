import { describe, expect, it } from 'vitest';
import { fxRiskBlocksSave, fxRiskOf, FX_RISK_DEFAULT_STALE_DAYS, type FxRisk } from './fxRisk';
import type { FxRateItem } from '@/api/fx';

// 用相对今天的日期生成采集日, 避免测试随时间推移而失效。
const dayOf = (ageDays: number): string => {
  const d = new Date();
  d.setDate(d.getDate() - ageDays);
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}${mm}${dd}`;
};

const rate = (to: string, day: string, rate = 7.2): FxRateItem => ({ from: 'USD', to, rate, day, source: 'test' });

describe('fxRiskOf', () => {
  it('USD/空币种无折算风险', () => {
    expect(fxRiskOf([], 'USD').level).toBe('usd');
    expect(fxRiskOf(undefined, '').level).toBe('usd');
    expect(fxRiskOf(undefined, undefined).level).toBe('usd');
  });

  it('新鲜牌价为 ok, 附采集日与日龄', () => {
    const risk = fxRiskOf([rate('CNY', dayOf(0))], 'CNY');
    expect(risk.level).toBe('ok');
    expect(risk.ageDays).toBe(0);
    expect(risk.day).not.toBe('');
  });

  it('昨天采集的牌价仍算 ok(边界内)', () => {
    expect(fxRiskOf([rate('CNY', dayOf(1))], 'CNY').level).toBe('ok');
  });

  it('超过缺省日龄的牌价为 stale, 日龄如实带出', () => {
    const risk = fxRiskOf([rate('CNY', dayOf(3))], 'CNY');
    expect(risk.level).toBe('stale');
    expect(risk.ageDays).toBe(3);
  });

  it('日龄等于阈值时不算 stale(> 判定)', () => {
    expect(fxRiskOf([rate('CNY', dayOf(FX_RISK_DEFAULT_STALE_DAYS))], 'CNY').level).toBe('ok');
  });

  it('无牌价/空表/未返回都按 missing 处理', () => {
    expect(fxRiskOf(undefined, 'CNY').level).toBe('missing');
    expect(fxRiskOf([], 'CNY').level).toBe('missing');
    expect(fxRiskOf([rate('CNY', dayOf(0))], 'EUR').level).toBe('missing');
  });

  it('rate 非正数按 missing 处理, 不当有效牌价', () => {
    expect(fxRiskOf([rate('CNY', dayOf(0), 0)], 'CNY').level).toBe('missing');
  });

  it('无法解析的采集日保守按 stale 处理', () => {
    const risk = fxRiskOf([rate('CNY', 'bad-day')], 'CNY');
    expect(risk.level).toBe('stale');
    expect(risk.ageDays).toBeGreaterThan(FX_RISK_DEFAULT_STALE_DAYS);
  });

  it('自定义阈值生效(采集 3 天前 + 阈值 5 = ok)', () => {
    expect(fxRiskOf([rate('CNY', dayOf(3))], 'CNY', 5).level).toBe('ok');
  });

  it('小写币种输入归一后可匹配', () => {
    expect(fxRiskOf([rate('CNY', dayOf(0))], 'cny').level).toBe('ok');
  });
});

describe('fxRiskBlocksSave', () => {
  it('只拦 missing, stale 与 ok 不拦', () => {
    const missing: FxRisk = { level: 'missing', day: '', ageDays: 0 };
    const stale: FxRisk = { level: 'stale', day: '20260101', ageDays: 5 };
    const ok: FxRisk = { level: 'ok', day: '20260101', ageDays: 0 };
    expect(fxRiskBlocksSave(missing)).toBe(true);
    expect(fxRiskBlocksSave(stale)).toBe(false);
    expect(fxRiskBlocksSave(ok)).toBe(false);
  });

  it('缺风险评估(USD 行)不拦', () => {
    expect(fxRiskBlocksSave(undefined)).toBe(false);
  });
});
