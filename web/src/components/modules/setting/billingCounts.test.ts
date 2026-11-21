import { describe, expect, it } from 'vitest';
import { formatTableCount } from './billingCounts';

// 这一组钉的是「0 与『取不到』必须显示成不同的东西」。
// 崩溃点很低(只差一个 ?? 0), 但后果是管理员跳过清理, 而超期的资金明细还在库里。

describe('formatTableCount', () => {
    it('0 显示成 0(表确实是空的, 这是一个事实)', () => {
        // lead 特别指出的反向情况: 把 0 也弄成「—」就反了 ——
        // 「表是空的」与「这次没读到」在界面上一混, 清理决策就失去依据。
        expect(formatTableCount(0)).toBe('0');
    });

    it('undefined 显示成占位符', () => {
        expect(formatTableCount(undefined)).toBe('—');
    });

    it('NaN / Infinity 也走占位符(不是数字的东西一律不显示成数字)', () => {
        expect(formatTableCount(Number.NaN)).toBe('—');
        expect(formatTableCount(Number.POSITIVE_INFINITY)).toBe('—');
    });

    it('真实行数带千分位', () => {
        // 五万行不带分隔符读成 50000, 眼睛数不清是 5 万还是 50 万。
        expect(formatTableCount(50000)).toBe('50,000');
        expect(formatTableCount(1234567)).toBe('1,234,567');
    });

    it('负数不被当成缺值(后端不会给, 但出现了要看得见)', () => {
        // 真出现了说明后端算错了; 显示「—」会把这个 bug 藏起来。
        expect(formatTableCount(-1)).toBe('-1');
    });
});
