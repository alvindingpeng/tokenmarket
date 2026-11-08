import { describe, expect, it } from 'vitest';
import {
  formatStatsHome,
  formatStatsMetrics,
  type StatsHome,
  type StatsMetrics,
} from './stats';

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
  it('总词元 = 输入 + 输出; 输入是毛输入(缓存读/写已由后端并入)', () => {
    // 后端已把缓存读写并入 input_token, 前端不做二次拆分: 300(毛) + 500 = 800。
    const r = formatStatsMetrics({ ...base, input_token: 300, output_token: 500 });
    expect(r.total_token.raw).toBe(800);
  });
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

// homeFixture 是一份聚合响应: 各段取自同一套口径, 便于断言"一次请求各段都不失真"。
const homeFixture: StatsHome = {
  total: { id: 0, ...base },
  daily: {
    max_request_count: 10,
    items: [{ date: '20260101', ...base }],
  },
  hourly: [{ hour: 9, date: '20260101', ...base }],
  channels: [
    {
      id: 7,
      name: '#1001',
      channelName: 'rank-channel',
      total_cost: 0.75,
      total_token: 100,
      request_count: 4,
      request_success: 3,
      request_failed: 1,
      models: [
        {
          id: 9,
          name: 'rank-model',
          channelName: '#1001',
          total_cost: 0.3,
          total_token: 30,
          request_count: 3,
          request_success: 2,
          request_failed: 1,
        },
      ],
    },
  ],
};

describe('formatStatsHome 聚合响应就地格式化', () => {
  it('total/daily/hourly 与各自独立接口的格式化结果一致', () => {
    const home = formatStatsHome(homeFixture);
    expect(home.total).toEqual(formatStatsMetrics(base));
    expect(home.daily.items[0]).toEqual({ ...formatStatsMetrics(base), date: '20260101' });
    expect(home.hourly[0]).toEqual({ ...formatStatsMetrics(base), hour: 9, date: '20260101' });
    // 热力图纵轴上界原样透传, 换算逻辑留在组件里。
    expect(home.daily.max_request_count).toBe(10);
  });
  it('榜单合计项只格式化, 不再二次求和', () => {
    const home = formatStatsHome(homeFixture);
    const channel = home.channels[0];
    // 后端已把成本/词元/请求数算成合计值, 前端若再求和就会翻倍。
    expect(channel.total_cost.raw).toBeCloseTo(0.75, 9);
    expect(channel.total_token.raw).toBe(100);
    expect(channel.request_count.raw).toBe(4);
    expect(channel.total_cost.formatted.unit).toBe('$');
    expect(channel.total_token.formatted.unit).toBe('');
    expect(channel.models[0].total_cost.raw).toBeCloseTo(0.3, 9);
    expect(channel.models[0].request_failed.raw).toBe(1);
  });
  it('榜单条目带名称与渠道名: 渠道榜用 name, 模型榜按 name 聚合、channelName 展示来源', () => {
    const home = formatStatsHome(homeFixture);
    expect(home.channels[0].id).toBe(7);
    expect(home.channels[0].name).toBe('#1001');
    expect(home.channels[0].channelName).toBe('rank-channel');
    expect(home.channels[0].models[0].name).toBe('rank-model');
    expect(home.channels[0].models[0].channelName).toBe('#1001');
  });
  it('没有任何渠道时 channels 仍是数组, 组件无需判空分支', () => {
    const home = formatStatsHome({ ...homeFixture, channels: [] });
    expect(home.channels).toEqual([]);
  });
});
