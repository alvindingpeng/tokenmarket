import { describe, expect, it } from 'vitest';
import { formatStatsMetrics, type StatsMetrics } from './stats';

const base: StatsMetrics = {
  input_token: 1000,
  output_token: 500,
  input_cost: 0.001,
  output_cost: 0.002,
  wait_time: 1_000,
  request_success: 8,
  request_failed: 2,
};

describe('formatStatsMetrics', () => {
  it('合计项 = 成功 + 失败 / 输入 + 输出', () => {
    const r = formatStatsMetrics(base);
    expect(r.total_token.raw).toBe(1500);
    expect(r.request_count.raw).toBe(10);
    expect(r.total_cost.raw).toBeCloseTo(0.003, 9);
  });
  it('各项走同一套格式化口径', () => {
    const r = formatStatsMetrics(base);
    expect(r.input_token.formatted).toEqual({ value: '1.00', unit: 'K' });
    expect(r.wait_time.formatted).toEqual({ value: '1.00', unit: 's' });
    expect(r.total_cost.formatted.unit).toBe('$');
  });
  it('全部为零时仍给出可渲染的格式化值', () => {
    const r = formatStatsMetrics({ input_token: 0, output_token: 0, input_cost: 0, output_cost: 0, wait_time: 0, request_success: 0, request_failed: 0 });
    expect(r.request_count.formatted.value).toBe('0.00');
    expect(r.total_cost.formatted.value).toBe('0.00');
  });
});
