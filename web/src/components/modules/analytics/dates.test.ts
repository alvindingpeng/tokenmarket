import { describe, expect, it } from 'vitest';
import {
    customDateRange,
    effectiveGranularity,
    formatDate,
    getDateRange,
    isValidDate,
    isValidDateRange,
    PRESETS,
    rangeSpanDays,
} from './dates';

// 日期口径回归: 统计范围的起止必须是「浏览器本地的年月日」。
// toISOString() 输出 UTC 时刻, 在 UTC+8 早上 8 点前/UTC-8 傍晚 4 点后会偏移一天,
// 于是「今天」悄悄变成昨天或明天 —— 数据没错, 范围错了。

const originalTZ = process.env.TZ;

// withTZ 在指定时区里跑断言, 跑完恢复现场。
// 恢复不能直接赋值 undefined —— process.env 会把它字符串化成 "undefined", 反而弄脏时区。
function withTZ(tz: string, run: () => void) {
    process.env.TZ = tz;
    try {
        run();
    } finally {
        if (originalTZ === undefined) delete process.env.TZ;
        else process.env.TZ = originalTZ;
    }
}

describe('formatDate', () => {
    it('UTC+8 本地凌晨不偏移到前一天(toISOString 会给出 2025-12-31)', () => {
        withTZ('Asia/Shanghai', () => {
            expect(new Date(2026, 0, 1).toISOString().slice(0, 10)).toBe('2025-12-31'); // 偏移确实存在
            expect(formatDate(new Date(2026, 0, 1))).toBe('2026-01-01');
        });
    });

    it('UTC-8 本地傍晚不偏移到第二天(toISOString 会给出 2026-01-02)', () => {
        withTZ('America/Los_Angeles', () => {
            expect(new Date(2026, 0, 1, 23, 30).toISOString().slice(0, 10)).toBe('2026-01-02'); // 偏移确实存在
            expect(formatDate(new Date(2026, 0, 1, 23, 30))).toBe('2026-01-01');
        });
    });

    it('月/日补零, 恒等于本地年月日拼接', () => {
        const date = new Date(2026, 2, 5, 13, 45);
        const pad = (n: number) => String(n).padStart(2, '0');
        expect(formatDate(date)).toBe(`${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`);
        expect(formatDate(new Date(2026, 2, 5))).toBe('2026-03-05');
    });
});

describe('getDateRange', () => {
    // now 在每个用例里就地构造: 跨用例共用一个 Date 实例时, 用例间切换时区会让读取口径漂移一天。
    const now = () => new Date(2026, 0, 15); // 本地 2026-01-15

    it('today: 起止同为本地今天', () => {
        expect(getDateRange('today', now())).toEqual({ start_date: '2026-01-15', end_date: '2026-01-15' });
    });

    it('week: 最近 7 天, 含首尾', () => {
        expect(getDateRange('week', now())).toEqual({ start_date: '2026-01-09', end_date: '2026-01-15' });
    });

    it('month: 最近 30 天, 含首尾, 跨月跨年', () => {
        expect(getDateRange('month', now())).toEqual({ start_date: '2025-12-17', end_date: '2026-01-15' });
    });

    it('范围都是本地日期: 换时区读取也不漂移', () => {
        withTZ('Pacific/Kiritimati', () => {
            expect(getDateRange('month', now())).toEqual({ start_date: '2025-12-17', end_date: '2026-01-15' });
        });
    });

    it('默认取当前时间, 不传 now 也能给出同一天的闭区间', () => {
        const range = getDateRange('today');
        const pad = (n: number) => String(n).padStart(2, '0');
        const today = new Date();
        const expected = `${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`;
        expect(range).toEqual({ start_date: expected, end_date: expected });
    });

    it('custom 不伪造区间: 起止都退回今天, 真正的起止由 customDateRange 给出', () => {
        // 关键: custom 绝不能悄悄退化成"最近 30 天"那种假范围 —— 那是本次补控件的起因。
        // 它给出的是"今天"这个合法占位, 页面在用户没填日期前不会把它当作用户意图发出去。
        expect(getDateRange('custom', now())).toEqual({ start_date: '2026-01-15', end_date: '2026-01-15' });
    });
});

describe('PRESETS', () => {
    it('含 custom: 日期控件已补齐, 这个选项不再是假范围', () => {
        expect([...PRESETS]).toEqual(['today', 'week', 'month', 'custom']);
    });
});

describe('isValidDate', () => {
    it('接受真实存在的日期', () => {
        expect(isValidDate('2026-01-15')).toBe(true);
        expect(isValidDate('2024-02-29')).toBe(true); // 闰年
        expect(isValidDate('2025-12-31')).toBe(true);
    });

    it('拒绝形状合法但不存在的日期', () => {
        // 2026 不是闰年; 4 月没有 31 号; 13 月不存在。只看正则会把它们放进查询串。
        expect(isValidDate('2026-02-31')).toBe(false);
        expect(isValidDate('2026-04-31')).toBe(false);
        expect(isValidDate('2026-13-01')).toBe(false);
        expect(isValidDate('2026-00-10')).toBe(false);
    });

    it('拒绝形状不对的输入(含空串与前后空格)', () => {
        for (const bad of ['', '2026-1-5', '2026/01/15', '20260115', '2026-01-15 ', ' 2026-01-15', 'not-a-date']) {
            expect(isValidDate(bad)).toBe(false);
        }
    });
});

describe('isValidDateRange', () => {
    it('起止都合法且顺序正确时成立', () => {
        expect(isValidDateRange('2026-01-01', '2026-01-31')).toBe(true);
        // 同一天是合法的闭区间: 单日查询真实存在。
        expect(isValidDateRange('2026-01-15', '2026-01-15')).toBe(true);
    });

    it('起止写反时不成立(不悄悄交换两端)', () => {
        // 交换会把用户填的值改掉, 他下次看到的值就不是自己输入的 —— 必须显式拒绝。
        expect(isValidDateRange('2026-01-31', '2026-01-01')).toBe(false);
    });

    it('任一端非法时不成立', () => {
        expect(isValidDateRange('', '2026-01-31')).toBe(false);
        expect(isValidDateRange('2026-01-01', '')).toBe(false);
        expect(isValidDateRange('2026-02-31', '2026-03-01')).toBe(false);
    });
});

describe('customDateRange', () => {
    it('原样透传用户的起止, 不做任何兜底转换', () => {
        expect(customDateRange('2026-01-01', '2026-01-31')).toEqual({
            start_date: '2026-01-01',
            end_date: '2026-01-31',
        });
    });
});

describe('rangeSpanDays', () => {
    it('闭区间含首尾: 同一天为 1, 跨一天为 2', () => {
        expect(rangeSpanDays('2026-01-15', '2026-01-15')).toBe(1);
        expect(rangeSpanDays('2026-01-14', '2026-01-15')).toBe(2);
        expect(rangeSpanDays('2026-01-01', '2026-01-31')).toBe(31);
    });

    it('跨月跨年按真实天数算', () => {
        expect(rangeSpanDays('2025-12-17', '2026-01-15')).toBe(30);
        expect(rangeSpanDays('2025-12-31', '2026-01-01')).toBe(2);
    });

    it('夏令时切换日不因为本地日长 23/25 小时而算错', () => {
        // 美国 2026-03-08 进入夏令时(那天只有 23 小时)。若按本地毫秒差除以 86400000,
        // 会得到 1.958 天, 四舍五入成 2 —— 这里直接数日历日, 结论恒为 2。
        withTZ('America/New_York', () => {
            expect(rangeSpanDays('2026-03-07', '2026-03-08')).toBe(2);
            expect(rangeSpanDays('2026-03-08', '2026-03-09')).toBe(2);
        });
    });

    it('非法区间返回 0(调用方据此不发查询)', () => {
        expect(rangeSpanDays('2026-01-31', '2026-01-01')).toBe(0);
        expect(rangeSpanDays('', '2026-01-01')).toBe(0);
    });
});

describe('effectiveGranularity', () => {
    it('today 恒按小时', () => {
        expect(effectiveGranularity('today', 'day', 1)).toBe('hour');
        expect(effectiveGranularity('today', 'hour', 1)).toBe('hour');
    });

    it('多日预设把小时降级为按天, 其余粒度原样透传', () => {
        expect(effectiveGranularity('week', 'hour', 7)).toBe('day');
        expect(effectiveGranularity('month', 'hour', 30)).toBe('day');
        expect(effectiveGranularity('week', 'week', 7)).toBe('week');
        expect(effectiveGranularity('month', 'month', 30)).toBe('month');
    });

    it('custom 单日保留用户选的粒度(含小时)', () => {
        // 单日区间看小时是既定组合, 与 today 同义。
        expect(effectiveGranularity('custom', 'hour', 1)).toBe('hour');
        expect(effectiveGranularity('custom', 'day', 1)).toBe('day');
    });

    it('custom 多日按与预设同一条规则降级', () => {
        // 720 个小时桶(30 天)画不下也读不出, 与"最近 30 天"同档处理。
        expect(effectiveGranularity('custom', 'hour', 2)).toBe('day');
        expect(effectiveGranularity('custom', 'hour', 30)).toBe('day');
        expect(effectiveGranularity('custom', 'week', 2)).toBe('week');
    });
});
