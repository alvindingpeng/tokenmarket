import { describe, expect, it } from 'vitest';
import { billingExportUrl } from './billing';
import { auditExportUrl } from './audit';

describe('billingExportUrl', () => {
  it('无筛选时只带固定的全量分页参数', () => {
    expect(billingExportUrl()).toBe('./api/v1/stats/billing/records/export?limit=5000&offset=0');
  });
  it('筛选项逐个落到查询串', () => {
    const url = billingExportUrl({ model: 'm1', status: 'settled', user_id: 7 });
    expect(url).toContain('model=m1');
    expect(url).toContain('status=settled');
    expect(url).toContain('user_id=7');
    expect(url).toContain('limit=5000');
  });
  it('空字符串筛选不落进查询串', () => {
    const url = billingExportUrl({ model: '', channel: '' });
    expect(url).not.toContain('model=');
    expect(url).not.toContain('channel=');
  });
});

describe('auditExportUrl', () => {
  it('无筛选时不带问号', () => {
    expect(auditExportUrl({})).toBe('./api/v1/audit/export');
  });
  it('与列表共享同一套筛选参数', () => {
    const url = auditExportUrl({ keyword: 'kw', action: 'user', operator: 'admin', from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' });
    expect(url).toContain('keyword=kw');
    expect(url).toContain('action=user');
    expect(url).toContain('operator=admin');
    expect(url).toContain('from=2026-01-01');
    expect(url).toContain('to=2026-01-02');
  });
  it('分页参数不进导出地址', () => {
    const url = auditExportUrl({ limit: 20, offset: 40, action: 'setting' });
    expect(url).not.toContain('limit=');
    expect(url).not.toContain('offset=');
    expect(url).toContain('action=setting');
  });
});
