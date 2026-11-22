import { describe, expect, it } from 'vitest';
import type { APIKeyUsageDay } from '@/api/apikey';
import { buildUsageChartData, hasUsageSamples, usageDayLabel, usageDaysSince } from './apiKeyUsageChart';

// 逐日用量图纯逻辑的用例。
//
// 这些断言都指向"图上会被看成什么", 不是实现细节:
// - 全零画成一条贴底的线, 会被读成"用量很低"而不是"没有样本";
// - 日期标签切片切错, 坐标轴上会出现看起来正常但其实是半截的字符串;
// - NaN 进了 recharts 不报错, 只画出一张畸形的坏图。

const day = (date: string, overrides: Partial<APIKeyUsageDay> = {}): APIKeyUsageDay => ({
    date,
    input_token: 0,
    output_token: 0,
    cache_read_token: 0,
    cache_write_token: 0,
    request_count: 0,
    user_cost: 0,
    refunded_records: 0,
    refunded_user_cost: 0,
    ...overrides,
});

describe('usageDayLabel', () => {
    it('标准 YYYY-MM-DD 只留 MM-DD', () => {
        expect(usageDayLabel('2026-11-20')).toBe('11-20');
    });

    it('个位月份与个位日都正常(不补零也不丢字)', () => {
        expect(usageDayLabel('2026-01-05')).toBe('01-05');
    });

    it('旧桶键 YYYYMMDD 原样返回, 不切出半截', () => {
        // 直接 slice(5) 会得到 "1120" —— 看起来像个正常的月日, 其实是错的位数。
        // 宁可印一个完整的 8 位串(用户能看出这不是日期), 也不要印一个像对但错位的值。
        expect(usageDayLabel('20261120')).toBe('20261120');
    });

    it('长度不足/超出时原样返回', () => {
        expect(usageDayLabel('')).toBe('');
        expect(usageDayLabel('2026-1-5')).toBe('2026-1-5');
        expect(usageDayLabel('2026-11-20T00:00:00Z')).toBe('2026-11-20T00:00:00Z');
    });

    it('分隔符位置不对时原样返回', () => {
        expect(usageDayLabel('2026/11/20')).toBe('2026/11/20');
        expect(usageDayLabel('2026-1120-')).toBe('2026-1120-');
    });

    it('含非数字字符时原样返回(不把脏数据切得像正常日期)', () => {
        expect(usageDayLabel('2026-1a-20')).toBe('2026-1a-20');
        expect(usageDayLabel('abcd-ef-gh')).toBe('abcd-ef-gh');
    });
});

describe('buildUsageChartData', () => {
    it('字段名保持后端原样, 不做改名', () => {
        // 改一层名字之后, 以后对不上接口的人要多一次心算; 这份数据就是接口原样。
        const point = buildUsageChartData([day('2026-11-20', { request_count: 7, user_cost: 0.42 })])[0];
        expect(point.request_count).toBe(7);
        expect(point.user_cost).toBe(0.42);
    });

    it('tokens 是四类词元之和(与账户卡片同一份加总逻辑)', () => {
        const point = buildUsageChartData([day('2026-11-20', {
            input_token: 100, output_token: 50, cache_read_token: 30, cache_write_token: 20,
        })])[0];
        expect(point.tokens).toBe(200);
    });

    it('顺序原样保留, 不重排', () => {
        // 后端给的是升序。再排一次只会多一个可能出错的比较函数;
        // 万一顺序变了, 那该由后端修, 不该由前端悄悄纠正。
        const data = buildUsageChartData([day('2026-11-18'), day('2026-11-19'), day('2026-11-20')]);
        expect(data.map((p) => p.label)).toEqual(['11-18', '11-19', '11-20']);
    });

    it('保留完整日期(悬浮提示要显示年月日, 不能只剩 MM-DD)', () => {
        const data = buildUsageChartData([day('2026-11-20')]);
        expect(data[0].day).toBe('2026-11-20');
        expect(data[0].label).toBe('11-20');
    });

    it('days 缺失(旧缓存/后端未升级)返回空数组而不是崩掉', () => {
        // 运行时 days 可能整个不存在; 在 map 上崩掉会让整张卡片白屏。
        expect(buildUsageChartData(undefined)).toEqual([]);
        expect(buildUsageChartData(null)).toEqual([]);
    });

    it('空数组返回空数组', () => {
        expect(buildUsageChartData([])).toEqual([]);
    });

    it('NaN / Infinity / 缺失字段收成 0, 不把畸形坐标交给 recharts', () => {
        // recharts 拿到 NaN 不报错, 它会照那个值算出一张看起来"有数据"的坏图。
        const data = buildUsageChartData([{
            date: '2026-11-20',
            input_token: Number.NaN,
            output_token: Number.POSITIVE_INFINITY,
            cache_read_token: 10,
            cache_write_token: 0,
            request_count: Number.NaN,
            user_cost: Number.POSITIVE_INFINITY,
            refunded_records: 0,
            refunded_user_cost: 0,
        } as APIKeyUsageDay]);
        expect(data[0].request_count).toBe(0);
        expect(data[0].user_cost).toBe(0);
        expect(data[0].tokens).toBe(10);
        expect(Number.isFinite(data[0].request_count)).toBe(true);
        expect(Number.isFinite(data[0].user_cost)).toBe(true);
        expect(Number.isFinite(data[0].tokens)).toBe(true);
    });
});

describe('hasUsageSamples', () => {
    it('空数组视为无样本', () => {
        expect(hasUsageSamples([])).toBe(false);
    });

    it('days 缺失/非数组视为无样本', () => {
        expect(hasUsageSamples(undefined)).toBe(false);
        expect(hasUsageSamples(null)).toBe(false);
    });

    it('所有天的请求数与费用都是 0 时视为无样本(不许画一条贴底的零线)', () => {
        // 零线看起来与"这一天用量很低"一模一样, 但真相是这段窗口没有样本。
        // 一条零线就是"不许把没取到画成 0"这条口径在图形上的违例。
        const allZero = [day('2026-11-18'), day('2026-11-19'), day('2026-11-20')];
        expect(hasUsageSamples(allZero)).toBe(false);
    });

    it('任意一天有请求数即有样本(哪怕费用为 0: 免费模型是真实用量)', () => {
        expect(hasUsageSamples([day('2026-11-18'), day('2026-11-19', { request_count: 1 })])).toBe(true);
    });

    it('任意一天有费用即有样本(哪怕请求数为 0: 跨日结算/退费净额也是真实计费)', () => {
        expect(hasUsageSamples([day('2026-11-18'), day('2026-11-19', { user_cost: 0.01 })])).toBe(true);
    });

    it('NaN 不算样本(不能因为脏值就画图)', () => {
        expect(hasUsageSamples([day('2026-11-18', { request_count: Number.NaN })])).toBe(false);
    });
});

describe('usageDaysSince', () => {
    it('只留下 since 当天及之后的日桶', () => {
        // 报表的 days 覆盖整个查询窗口(默认终身); 而图表标题三语都写"最近 30 天"。
        // 终身几百个日桶塞进 320px 宽会把每根柱子压到不足 1 像素, 画出来的范围还与标题不符。
        const days = [day('2026-10-01'), day('2026-10-22'), day('2026-11-20')];
        expect(usageDaysSince(days, '2026-10-22').map((d) => d.date)).toEqual(['2026-10-22', '2026-11-20']);
    });

    it('边界当天算在内(后端 recent_from 是闭区间起点)', () => {
        expect(usageDaysSince([day('2026-10-22')], '2026-10-22').map((d) => d.date)).toEqual(['2026-10-22']);
    });

    it('since 为空串(旧缓存/后端未升级)时原样返回全部, 不截断', () => {
        // 拿不到边界就整份画: 悄悄丢掉一段时间与"覆盖不全"是同一类谎。
        const days = [day('2026-01-01'), day('2026-11-20')];
        expect(usageDaysSince(days, '').map((d) => d.date)).toEqual(['2026-01-01', '2026-11-20']);
    });

    it('since 形状不对(非定长日期)时也原样返回, 不做字符串比较', () => {
        // 用 '2026-1-5' 这类非定长串做 >= 比较会得到错序的结果: 定长零填充才让字符串序等于日期序。
        const days = [day('2026-01-01')];
        expect(usageDaysSince(days, '2026-1-5').map((d) => d.date)).toEqual(['2026-01-01']);
    });

    it('days 缺失时返回空数组', () => {
        expect(usageDaysSince(undefined, '2026-10-22')).toEqual([]);
        expect(usageDaysSince(null, '2026-10-22')).toEqual([]);
    });

    it('窗口内一个都没有时返回空数组(由调用方走空态, 不是补 0)', () => {
        expect(usageDaysSince([day('2026-01-01', { request_count: 5 })], '2026-10-22')).toEqual([]);
    });
});
