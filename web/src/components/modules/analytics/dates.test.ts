import { describe, expect, it } from 'vitest';
import { formatDate, getDateRange, PRESETS } from './dates';

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
});

describe('PRESETS', () => {
    it('不含 custom: 没有日期输入控件的选项等于假范围', () => {
        expect([...PRESETS]).toEqual(['today', 'week', 'month']);
    });
});
