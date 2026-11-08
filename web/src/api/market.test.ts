import { describe, expect, it } from 'vitest';
import { channelPrice, cheapestListing, availableCount, marketCatalogPath, type MarketModel } from './market';

// 广场的查询串拼装: 这一层最容易静默出错(参数丢了页面照常渲染, 只是筛选看起来"没生效"),
// 而接口本身又在服务端 —— 前端单测只能锁拼装, 服务端行为由 handlers 的 Go 测试负责。
describe('marketCatalogPath', () => {
  it('无筛选时不带问号', () => {
    expect(marketCatalogPath({ q: '', brands: [], kind: '', sort: 'name' })).toBe('/api/v1/market/models');
  });
  it('排序维度非 name 时才落进查询串', () => {
    expect(marketCatalogPath({ q: '', brands: [], kind: '', sort: 'success' })).toContain('sort=success');
    expect(marketCatalogPath({ q: '', brands: [], kind: '', sort: 'name' })).not.toContain('sort=');
  });
  it('品牌多值用重复 key', () => {
    const path = marketCatalogPath({ q: '', brands: ['openai', 'anthropic'], kind: '', sort: 'name' });
    expect(path).toContain('brand=openai');
    expect(path).toContain('brand=anthropic');
  });
  it('空白搜索词与 kind 空值不落查询串', () => {
    const path = marketCatalogPath({ q: '   ', brands: [], kind: '', sort: 'name' });
    expect(path).not.toContain('q=');
    expect(path).not.toContain('kind=');
  });
  it('搜索词按去空白后编码', () => {
    expect(marketCatalogPath({ q: ' gpt 5 ', brands: [], kind: '', sort: 'name' })).toContain('q=gpt+5');
  });
});

// 价格与"最低价"口径: 与后端 model.MarketChannel.SortPrice 的取法必须一致,
// 否则同一模型会在卡片上显示一个价、在后端排序时按另一个价定位。
describe('挂牌价格口径', () => {
  const model = (listings: MarketModel['channels']): MarketModel => ({
    name: 'm', brand: 'openai', channels: listings, in_groups: [], samples: 0, metrics_source: 'none',
  });
  const listing = (over: Partial<MarketModel['channels'][number]>): MarketModel['channels'][number] => ({
    channel_grant_id: 1, channel_model_id: 1, channel_code: '#1', available: true,
    user_price: { input: 0, output: 0, cache_read: 0, cache_write: 0 }, samples: 0, ...over,
  });

  it('文本模型取 (input+output)/2', () => {
    expect(channelPrice(listing({ user_price: { input: 10, output: 30, cache_read: 0, cache_write: 0 } }))).toBe(20);
  });
  it('文本价为零时取媒体价, per_image 优先', () => {
    expect(channelPrice(listing({ user_media: { per_image: 0.5, per_second: 0.2 } }))).toBe(0.5);
  });
  it('最低价只看可用渠道', () => {
    const m = model([
      listing({ channel_grant_id: 1, user_price: { input: 1, output: 1, cache_read: 0, cache_write: 0 } }),
      listing({ channel_grant_id: 2, available: false, user_price: { input: 10, output: 10, cache_read: 0, cache_write: 0 } }),
    ]);
    expect(cheapestListing(m)?.channel_grant_id).toBe(1);
  });
  it('全部未定价时没有最低价(而不是 0 元)', () => {
    expect(cheapestListing(model([listing({})]))).toBeUndefined();
  });
  it('可用渠道计数不含停用与熔断', () => {
    const m = model([listing({}), listing({ channel_grant_id: 2, available: false })]);
    expect(availableCount(m)).toBe(1);
  });
});
