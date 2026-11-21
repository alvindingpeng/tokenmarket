import { describe, expect, it } from 'vitest';
import { apiKeyUsageQuery, apiKeyUsageTokens } from './apikey';
import { billingExportUrl } from './billing';

// apiKeyUsageQuery 是 useAPIKeyUsage 里唯一有分支的逻辑, 抽成纯函数就是为了在这里直接把三种拼法钉住:
// 少一个参数会静默退化成"查全部窗口", 多一个空参数会让后端收到 from= 这种非法日期。
describe('apiKeyUsageQuery', () => {
  it('只给 id 时只拼 api_key_id', () => {
    expect(apiKeyUsageQuery(12)).toBe('api_key_id=12');
  });

  it('带上 from/to 时两个窗口参数都落进查询串', () => {
    expect(apiKeyUsageQuery(12, '2026-05-24', '2026-11-20'))
      .toBe('api_key_id=12&from=2026-05-24&to=2026-11-20');
  });

  it('只给 from 时只有 from, 不会拼出空的 to=', () => {
    const query = apiKeyUsageQuery(12, '2026-05-24');
    expect(query).toContain('from=2026-05-24');
    expect(query).not.toContain('to=');
  });

  it('只给 to 时只有 to', () => {
    const query = apiKeyUsageQuery(12, undefined, '2026-11-20');
    expect(query).toContain('to=2026-11-20');
    expect(query).not.toContain('from=');
  });

  it('空串的窗口参数不落进查询串', () => {
    // 调用方把"没选日期"传成 '' 是最容易发生的一种情况; 拼进 from= 会让后端解析失败。
    expect(apiKeyUsageQuery(12, '', '')).toBe('api_key_id=12');
  });

  it('非法 id 返回空串, 调用方据此不发请求', () => {
    // 0 / 负数 / 非整数都不可能是真实主键; 发出去只会得到 404, 没有意义。
    expect(apiKeyUsageQuery(0)).toBe('');
    expect(apiKeyUsageQuery(-1)).toBe('');
    expect(apiKeyUsageQuery(1.5)).toBe('');
    expect(apiKeyUsageQuery(Number.NaN)).toBe('');
  });
});

// apiKeyUsageTokens 把接口给的四个词元字段加总。
// 注意不能用 input+output: 缓存读/写是独立字段, 漏掉它们的合计会比账单少一截。
describe('apiKeyUsageTokens', () => {
  it('四类词元全部计入合计', () => {
    expect(apiKeyUsageTokens({
      input_token: 100,
      output_token: 50,
      cache_read_token: 30,
      cache_write_token: 20,
      request_count: 3,
      user_cost: 0.5,
      refunded_records: 0,
      refunded_user_cost: 0,
    })).toBe(200);
  });

  it('全零时合计为零, 不会出现 NaN', () => {
    expect(apiKeyUsageTokens({
      input_token: 0,
      output_token: 0,
      cache_read_token: 0,
      cache_write_token: 0,
      request_count: 0,
      user_cost: 0,
      refunded_records: 0,
      refunded_user_cost: 0,
    })).toBe(0);
  });
});

// Key 卡片的"导出该 Key 明细"复用计费导出地址, 只是多带一个 api_key_id。
// 这条断言同时守住两件事: 参数确实拼进去了, 且没有把分页参数写死成别的值。
describe('billingExportUrl 带 api_key_id', () => {
  it('api_key_id 出现在导出地址里', () => {
    const url = billingExportUrl({ api_key_id: 12 });
    expect(url).toContain('api_key_id=12');
    expect(url).toContain('limit=5000');
  });

  it('api_key_id 与其它筛选条件共存', () => {
    const url = billingExportUrl({ api_key_id: 12, status: 'settled' });
    expect(url).toContain('api_key_id=12');
    expect(url).toContain('status=settled');
  });

  it('未给 api_key_id 时不出现该参数', () => {
    // 计费页的导出不带 Key 维度; 多出 api_key_id=0 会被后端当作筛选 0 号 Key。
    expect(billingExportUrl()).not.toContain('api_key_id');
  });
});
