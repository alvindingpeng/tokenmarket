import { describe, expect, it } from 'vitest';
import type { UserStatsOverview } from '@/api/user-stats';
import { formatOffset, hourTickLabel, timezoneNotice } from './timezone';

// 时区口径的三条判据。
// 这里钉的是"什么情况下界面会多出一行说明", 所以三种分支都要有自己的用例 ——
// 尤其是"偏移相等但名字不同"那种不该提示的情形, 少了它就会给大多数用户平白多加一条提示。

// mkOverview 只关心时区四字段, 其余给零值(纯函数不读它们)。
function mkOverview(patch: Partial<UserStatsOverview> = {}): UserStatsOverview {
    return {
        total_requests: 0, success_rate: 0, total_cost: 0, total_tokens: 0,
        request_total: 0, request_success: 0, request_failed: 0, request_canceled: 0,
        request_coverage_start: '', request_retention_days: 7,
        model_breakdown_coverage_start: '',
        input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0,
        cache_hit_rate: 0,
        stats_timezone: 'Asia/Shanghai',
        stats_timezone_offset_minutes: 480,
        stats_timezone_source: 'server',
        stats_calendar_timezone: 'Asia/Shanghai',
        stats_calendar_timezone_offset_minutes: 480,
        ...patch,
    };
}

describe('timezoneNotice: 拿不到就不提示', () => {
    it('overview 未就绪(undefined)时走 legacy, 不提示', () => {
        // 请求还没回来: 没有事实依据, 不该先摆一行说明。
        expect(timezoneNotice(undefined)).toEqual({ kind: 'legacy' });
    });

    it('老后端没有这些字段时走 legacy, 不把 undefined 拼进文案', () => {
        // 部署里很可能还是旧版本: 类型上必填不代表运行时真有, 这里靠运行时校验兜住。
        const stale = mkOverview();
        delete (stale as Partial<UserStatsOverview>).stats_timezone;
        delete (stale as Partial<UserStatsOverview>).stats_calendar_timezone;
        delete (stale as Partial<UserStatsOverview>).stats_timezone_offset_minutes;
        delete (stale as Partial<UserStatsOverview>).stats_calendar_timezone_offset_minutes;
        expect(timezoneNotice(stale)).toEqual({ kind: 'legacy' });
    });

    it('只有偏移、缺时区名时也走 legacy', () => {
        // 半套字段拼出来的提示只会更让人困惑(例如"按 算"这种空名字)。
        const noName = mkOverview({ stats_timezone: undefined as unknown as string });
        expect(timezoneNotice(noName)).toEqual({ kind: 'legacy' });
    });

    it('偏移是脏数据(非数字)时走 legacy', () => {
        expect(timezoneNotice(mkOverview({ stats_timezone_offset_minutes: null as unknown as number })))
            .toEqual({ kind: 'legacy' });
    });
});

describe('timezoneNotice: 挂钟一致就不提示', () => {
    it('两个时区同名同偏移时 kind=none', () => {
        expect(timezoneNotice(mkOverview())).toEqual({ kind: 'none' });
    });

    it('偏移相等但时区名不同时也不提示', () => {
        // 关键一条: 用户能感知的只有偏移(日期落在哪天、桶从几点起)。
        // 按名字判会给一大批挂钟完全一致的用户平白加一条提示。
        expect(timezoneNotice(mkOverview({
            stats_timezone: 'Asia/Macau',
            stats_calendar_timezone: 'Asia/Shanghai',
        }))).toEqual({ kind: 'none' });
    });

    it('用户时区生效但偏移与服务器一致时不提示', () => {
        expect(timezoneNotice(mkOverview({
            stats_timezone: 'Asia/Chongqing',
            stats_timezone_source: 'user',
            stats_calendar_timezone: 'Asia/Shanghai',
        }))).toEqual({ kind: 'none' });
    });
});

describe('timezoneNotice: 只有真的错开才提示', () => {
    it('偏移不同时给出日历提示, 并带上两个时区名', () => {
        // 只说"时区不同"而不说是哪两个, 用户无从判断差在哪、影响哪部分。
        expect(timezoneNotice(mkOverview({
            stats_timezone: 'America/New_York',
            stats_timezone_offset_minutes: -300,
            stats_timezone_source: 'user',
            stats_calendar_timezone: 'Asia/Shanghai',
            stats_calendar_timezone_offset_minutes: 480,
        }))).toEqual({
            kind: 'calendar',
            calendarTimezone: 'Asia/Shanghai',
            windowTimezone: 'America/New_York',
        });
    });

    it('非整点偏移同样按数值判定', () => {
        // +05:45(尼泊尔)与 +08:00 差 135 分钟: 名字不同且偏移不同, 必须提示。
        expect(timezoneNotice(mkOverview({
            stats_timezone: 'Asia/Kathmandu',
            stats_timezone_offset_minutes: 345,
        }))).toEqual({
            kind: 'calendar',
            calendarTimezone: 'Asia/Shanghai',
            windowTimezone: 'Asia/Kathmandu',
        });
    });

    it('偏移只差 1 分钟也算错开', () => {
        // 不要用阈值近似: 差一分钟也意味着某些桶边界会落到不同的一天。
        expect(timezoneNotice(mkOverview({ stats_timezone_offset_minutes: 481 })))
            .toEqual({
                kind: 'calendar',
                calendarTimezone: 'Asia/Shanghai',
                windowTimezone: 'Asia/Shanghai',
            });
    });
});

describe('hourTickLabel', () => {
    it('YYYY-MM-DD HH:MM 取后 5 位', () => {
        expect(hourTickLabel('2026-01-15 08:00')).toBe('08:00');
    });

    it('非整点分钟照原样保留, 不抹成 :00', () => {
        // +05:45 时区的小时桶会落在 :45 —— 那是真实时间点, 抹平就是改数据。
        expect(hourTickLabel('2026-01-15 08:45')).toBe('08:45');
        expect(hourTickLabel('2026-01-15 00:30')).toBe('00:30');
    });

    it('纯日期(按天/周/月的桶)原样返回', () => {
        expect(hourTickLabel('2026-01-15')).toBe('2026-01-15');
    });

    it('其它长度一律原样返回, 不做猜测性截断', () => {
        // 截错位置会把标签变成乱码, 不如原样显示。
        expect(hourTickLabel('')).toBe('');
        expect(hourTickLabel('08:00')).toBe('08:00');
        expect(hourTickLabel('2026-01-15 08:00:00')).toBe('2026-01-15 08:00:00');
    });
});

describe('formatOffset', () => {
    it('正偏移补零到 HH:MM', () => {
        expect(formatOffset(480)).toBe('UTC+08:00');
        expect(formatOffset(330)).toBe('UTC+05:30');
    });

    it('负偏移', () => {
        expect(formatOffset(-300)).toBe('UTC-05:00');
        // 负的半小时时区: 符号与分钟要各自处理, 否则会算出 UTC-00:-30 这类东西。
        expect(formatOffset(-210)).toBe('UTC-03:30');
    });

    it('非整点偏移', () => {
        expect(formatOffset(345)).toBe('UTC+05:45');
        expect(formatOffset(-585)).toBe('UTC-09:45');
    });

    it('0 与 -0 都写 UTC+00:00', () => {
        // 没有 "UTC-00:00" 这个时区: -0 与 +0 是同一个偏移。
        expect(formatOffset(0)).toBe('UTC+00:00');
        expect(formatOffset(-0)).toBe('UTC+00:00');
    });

    it('非有限数返回空串, 不把 NaN 印到界面上', () => {
        expect(formatOffset(NaN)).toBe('');
        expect(formatOffset(Infinity)).toBe('');
        expect(formatOffset(-Infinity)).toBe('');
    });
});
