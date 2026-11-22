import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import type { APIKey, APIKeyUsageDay, APIKeyUsageReport } from '@/api/apikey';
import { APIKeyUsageSection } from './APIKey';
import en from '@/locales/en.json';
import hans from '@/locales/zh_hans.json';
import hant from '@/locales/zh_hant.json';

// 逐日用量图的组件级用例: 四态必须分得开, 有样本才画图。
//
// 为什么必须在组件层面再测一遍: 纯函数测的是"该不该画"的判据, 但判据接错分支照样白搭 ——
// 例如把 hasUsageSamples 用反, 纯函数测试全绿而界面会在"有 5000 次调用"时显示"暂无用量"。
//
// 为什么用 renderSSR: 本仓库测试环境是 node, 没有 jsdom。四态的分支都在 render 内, 出字符串足够断言。
// recharts 的 ResponsiveContainer 在 SSR 里量不到宽度, 因此**图元本身画不出来**(这是已知限制);
// 断言落在图表的标题与容器 testid 上 —— 那正是"该不该出现这张图"的分界线, 比柱子像素更稳。

// 只给用到的最小字段: 这个用例断言的是用量区, Key 的其余字段(掩码/模型/配额)与它无关。
// 用 Pick 声明形状而不是编一遍整个 APIKey —— 后者会在接口加字段时假红。
const apiKey = {
    id: 7,
    name: 'report-key',
    enabled: true,
} as Pick<APIKey, 'id' | 'name' | 'enabled'> as APIKey;

const totals = {
    input_token: 1000, output_token: 200, cache_read_token: 300, cache_write_token: 50,
    request_count: 42, user_cost: 1.25, refunded_records: 0, refunded_user_cost: 0,
};

const dayOf = (date: string, overrides: Partial<APIKeyUsageDay> = {}): APIKeyUsageDay => ({
    date, ...totals, ...overrides,
});

const reportOf = (overrides: Partial<APIKeyUsageReport> = {}): APIKeyUsageReport => ({
    api_key_id: apiKey.id,
    from: '2026-10-22',
    to: '2026-11-20',
    coverage_start: '2026-10-22',
    retention_days: 365,
    total: totals,
    recent: totals,
    recent_from: '2026-10-22',
    days: [dayOf('2026-11-18', { request_count: 5 }), dayOf('2026-11-19', { request_count: 9 })],
    ...overrides,
});

// usageKey 与 useAPIKeyUsage 里的查询键一致: 少了 from/to 两段就种不进缓存。
const usageKey = ['apikey', 'usage', apiKey.id, '', ''];

const render = (seed?: (value: APIKeyUsageReport) => void) => renderSSR(
    <APIKeyUsageSection apiKey={apiKey} />,
    (client) => client.setQueryData(usageKey, seed ? seed(reportOf()) : reportOf()),
);

describe('APIKeyUsageSection: 逐日图四态', () => {
    it('有样本时出现逐日图(标题 + 容器), 且两行数字同时在场', () => {
        const html = render();
        expect(html).toContain(en.setting.apiKey.usage.daysChart);
        expect(html).toContain('data-testid="apikey-usage-days-chart"');
        expect(html).toContain(en.setting.apiKey.usage.recent);
        expect(html).toContain(en.setting.apiKey.usage.total);
    });

    it('有数据但每一天的请求数与费用都是零时走空态, 不画图', () => {
        // 零线看起来与"这天用量很低"一模一样, 真相却是这段窗口没有样本。
        // 注意 dayOf 默认带上非零的 totals, 这里必须显式把两个口径都压成 0,
        // 否则用例测的是"有样本"那一档, 反而会误报成绿。
        const html = render((report) => ({
            ...report,
            days: [
                dayOf('2026-11-18', { request_count: 0, user_cost: 0 }),
                dayOf('2026-11-19', { request_count: 0, user_cost: 0 }),
            ],
        }));
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        expect(html).toContain('data-testid="apikey-usage-days-empty"');
        // 两行数字仍在: 全零的最近/累计是**真实**的累计值(0), 不能因为图空就整段消失。
        expect(html).toContain(en.setting.apiKey.usage.total);
    });

    it('空 days 也走空态, 不画图', () => {
        const html = render((report) => ({ ...report, days: [] }));
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        expect(html).toContain('data-testid="apikey-usage-days-empty"');
    });

    it('只有任意一天有请求数就画图(哪怕其余全零)', () => {
        const html = render((report) => ({
            ...report,
            days: [dayOf('2026-11-18'), dayOf('2026-11-19', { request_count: 1 })],
        }));
        expect(html).toContain('data-testid="apikey-usage-days-chart"');
        expect(html).not.toContain('data-testid="apikey-usage-days-empty"');
    });

    it('只有任意一天有费用就画图(哪怕请求数为 0: 跨日结算/退费净额也是真实计费)', () => {
        const html = render((report) => ({
            ...report,
            days: [dayOf('2026-11-18', { request_count: 0, user_cost: 0.01 }), dayOf('2026-11-19', { request_count: 0, user_cost: 0 })],
        }));
        expect(html).toContain('data-testid="apikey-usage-days-chart"');
    });
});

describe('APIKeyUsageSection: 图表窗口收到最近 30 天', () => {
    it('30 天窗口外的日桶不进图, 窗口内的进图', () => {
        // days 覆盖整个查询窗口(默认终身), 而标题写的是最近 30 天。
        // 这条断言钉的是"画出来的范围必须与标题一致": 窗口外的点若混进来,
        // 图就会变成一条跨越数年的稀疏柱列, 每根不到 1 像素。
        // SSR 下画不出柱子本身(见文件头), 因此断言落在"该不该出现这张图"上:
        // 最近 30 天全零 + 更早的日期有量 => 空态。
        const html = render((report) => ({
            ...report,
            recent_from: '2026-10-22',
            days: [
                dayOf('2026-01-15', { request_count: 30 }),
                dayOf('2026-11-18', { request_count: 0, user_cost: 0 }),
                dayOf('2026-11-19', { request_count: 0, user_cost: 0 }),
            ],
        }));
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        expect(html).toContain('data-testid="apikey-usage-days-empty"');
    });

    it('窗口内任一天有量就画图, 哪怕窗口外另有大量数据', () => {
        const html = render((report) => ({
            ...report,
            recent_from: '2026-10-22',
            days: [
                dayOf('2026-01-15', { request_count: 30 }),
                dayOf('2026-11-19', { request_count: 2 }),
            ],
        }));
        expect(html).toContain('data-testid="apikey-usage-days-chart"');
    });

    it('recent_from 为空串(旧缓存)时退回整份 days, 不悄悄截断', () => {
        const html = render((report) => ({
            ...report,
            recent_from: '',
            days: [dayOf('2026-01-15', { request_count: 3 })],
        }));
        expect(html).toContain('data-testid="apikey-usage-days-chart"');
    });
});

describe('APIKeyUsageSection: 既有三态不被新增的一档挤掉', () => {
    it('加载中: 显示 loading, 不显示图也不显示空态', () => {
        // 不种缓存 = query 处于 pending。
        const html = renderSSR(<APIKeyUsageSection apiKey={apiKey} />);
        expect(html).toContain(en.setting.apiKey.usage.loading);
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        expect(html).not.toContain('data-testid="apikey-usage-days-empty"');
        expect(html).not.toContain(en.setting.apiKey.usage.noData);
    });

    it('失败: 显示 loadFailed, 不显示图也不显示空态(绝不把失败画成 0)', () => {
        // 种一个 error 到同一查询键上, 走 isError 分支。
        const html = renderSSR(<APIKeyUsageSection apiKey={apiKey} />, (client) => {
            // 用 getQueryCache().build 直接建一个 error 状态的查询 —— 与 Billing 的失败态用例同一套做法。
            // 先 setQueryData 再 setState 是行不通的: setQueryData 会把查询置成 success,
            // react-query 认为已有 data 就不算 error, 组件于是走"有数据"分支, 用例会伪装成绿。
            client.setDefaultOptions({ queries: { retryOnMount: false } });
            client.getQueryCache().build(client, { queryKey: usageKey }, {
                data: undefined, dataUpdateCount: 0, dataUpdatedAt: 0,
                error: new Error('boom'), errorUpdateCount: 1, errorUpdatedAt: Date.now(),
                fetchFailureCount: 1, fetchFailureReason: new Error('boom'), fetchMeta: null,
                isInvalidated: false, status: 'error', fetchStatus: 'idle',
            });
        });
        expect(html).toContain(en.setting.apiKey.usage.loadFailed);
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        expect(html).not.toContain('data-testid="apikey-usage-days-empty"');
    });

    it('无数据(coverage_start 为空): 显示 noData, 不显示图', () => {
        const html = render((report) => ({ ...report, coverage_start: '', days: [] }));
        expect(html).toContain(en.setting.apiKey.usage.noData);
        expect(html).not.toContain('data-testid="apikey-usage-days-chart"');
        // 这一档连"有日桶但全零"的结构都不该出现: 它是更早的一档, 走的是整段替换。
        expect(html).not.toContain('data-testid="apikey-usage-days-empty"');
    });
});

describe('APIKeyUsageSection: 三语文案都接上了', () => {
    it.each([['en', en], ['zh-Hans', hans], ['zh-Hant', hant]] as const)('%s 下图表标题走本语', (_locale, messages) => {
        // 缺键时 use-intl 会把 `setting.apiKey.usage.daysChart` 原样画在界面上。
        const html = renderSSR(<APIKeyUsageSection apiKey={apiKey} />,
            (client) => client.setQueryData(usageKey, reportOf()),
            { locale: _locale, messages });
        expect(html).toContain(messages.setting.apiKey.usage.daysChart);
        expect(html).not.toContain('setting.apiKey.usage');
    });
});
