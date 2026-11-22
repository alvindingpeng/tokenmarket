import { describe, expect, it } from 'vitest';
import { availableBalance, balanceLowState, formatBannerAmount, hasThreshold } from './balance-low';

// 判定口径的纯逻辑单测。
// 每条"不提示"的理由都单独钉一条用例 —— 判据表里最容易腐坏的不是"什么时候显示",
// 而是"为什么这次不显示"; 没有用例守着, 下一个人只会在注释里读到理由(或者读不到)。

describe('availableBalance', () => {
    it('可用余额 = 账面余额 - 冻结额', () => {
        expect(availableBalance({ balance: 10, frozen: 6.5 })).toBe(3.5);
        // 冻结为 0 时就是账面余额本身。
        expect(availableBalance({ balance: 7, frozen: 0 })).toBe(7);
    });

    it('冻结大于余额时得到负数(不夹到 0)', () => {
        // 夹到 0 会把"透支/冻结未落定"显示成"刚好用完", 那是两种不同的状态。
        expect(availableBalance({ balance: 5, frozen: 8 })).toBe(-3);
    });

    it('字段缺失或非数字时返回 NaN, 不兜底成 0', () => {
        // 兜底成 0 会让"没取到"看起来像"余额是 0" —— 资金页上这是最危险的假象。
        expect(availableBalance(undefined)).toBeNaN();
        expect(availableBalance({ balance: 10 })).toBeNaN();
        expect(availableBalance({ frozen: 5 })).toBeNaN();
        expect(availableBalance({ balance: 10, frozen: null as unknown as number })).toBeNaN();
    });
});

describe('hasThreshold', () => {
    it('enabled 且阈值 > 0 才算已配置', () => {
        expect(hasThreshold({ threshold_cny: 5, enabled: true })).toBe(true);
        expect(hasThreshold({ threshold_cny: 0.01, enabled: true })).toBe(true);
    });

    it('阈值 <= 0 一律当作未配置', () => {
        // 后端把 threshold_cny === 0 归一成"禁用"; 这里再判一次是防御只带一半状态的旧数据。
        expect(hasThreshold({ threshold_cny: 0, enabled: true })).toBe(false);
        expect(hasThreshold({ threshold_cny: -5, enabled: true })).toBe(false);
    });

    it('enabled=false 时即使阈值有效也算未配置', () => {
        // 用户关掉了预警: 线还在但不该响。
        expect(hasThreshold({ threshold_cny: 5, enabled: false })).toBe(false);
    });

    it('字段缺失/非数字时算未配置', () => {
        expect(hasThreshold(undefined)).toBe(false);
        expect(hasThreshold({})).toBe(false);
        expect(hasThreshold({ threshold_cny: NaN, enabled: true })).toBe(false);
        expect(hasThreshold({ threshold_cny: Infinity, enabled: true })).toBe(false);
        expect(hasThreshold({ threshold_cny: '5' as unknown as number, enabled: true })).toBe(false);
    });
});

describe('balanceLowState: 该显示的只有一种', () => {
    it('可用余额 <= 阈值时给出 available 与 threshold', () => {
        expect(balanceLowState({ balance: 10, frozen: 6.5 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'threshold', available: 3.5, threshold: 5 });
    });

    it('可用余额恰等于阈值也算到线', () => {
        // 用户设线的语义是"低到这个数就叫我", 等于时已经到线了 —— 用 <= 而非 <。
        expect(balanceLowState({ balance: 5, frozen: 0 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'threshold', available: 5, threshold: 5 });
    });
});

describe('balanceLowState: 每一种"不说"都有独立理由', () => {
    it('没有钱包数据 -> no-data', () => {
        expect(balanceLowState(undefined, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'no-data' });
    });

    it('钱包字段缺失 -> no-data(不是 not-finite)', () => {
        // "这次没取到"与"取到了但值坏了"归因不同, 混成一个会让排查时看不出是哪一类。
        expect(balanceLowState({ balance: 10 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'no-data' });
    });

    it('可用余额算出来是 NaN -> not-finite', () => {
        expect(balanceLowState({ balance: NaN, frozen: 0 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'not-finite' });
    });

    it('可用余额 <= 0 -> not-positive(不抢 402 的语义)', () => {
        // 余额 <= 0 是硬失败: 请求已经被后端挡下, 那条错误本身就在告诉用户钱不够。
        // 横幅再喊一遍会抢它的语义, 两个提示同时出现还会让人以为钱被清零了。
        expect(balanceLowState({ balance: 0, frozen: 0 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'not-positive' });
        expect(balanceLowState({ balance: 5, frozen: 8 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'not-positive' });
    });

    it('余额 <= 0 即使没配阈值也走 not-positive(判定顺序固定)', () => {
        // 顺序固定才能保证理由唯一: 否则同一种情形可能报出两个不同的 reason。
        expect(balanceLowState({ balance: 0, frozen: 0 }, undefined))
            .toEqual({ kind: 'hidden', reason: 'not-positive' });
    });

    it('没有预警配置 -> no-threshold(不替用户发明一条线)', () => {
        // 这是本任务最需要说清的一处取舍: 没有阈值就没有客观判据。
        // 要显示就得挑一个我们自己发明的数字, 而那个数字既不是后端算的也不是用户设的 ——
        // 用户会拿它去对账。宁可不说。
        for (const alert of [
            undefined,
            { threshold_cny: 0, enabled: false },
            { threshold_cny: 0, enabled: true },
            { threshold_cny: 5, enabled: false },
        ]) {
            expect(balanceLowState({ balance: 1, frozen: 0 }, alert))
                .toEqual({ kind: 'hidden', reason: 'no-threshold' });
        }
    });

    it('可用余额高于阈值 -> above-threshold', () => {
        expect(balanceLowState({ balance: 100, frozen: 0 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'above-threshold' });
        // 差一分钱也算高于阈值: 没到线就不响。
        expect(balanceLowState({ balance: 5.01, frozen: 0 }, { threshold_cny: 5, enabled: true }))
            .toEqual({ kind: 'hidden', reason: 'above-threshold' });
    });
});

describe('formatBannerAmount', () => {
    it('沿用计费口径精度(固定 6 位小数再截尾零)', () => {
        // 资金页上两套精度会立刻变成"同一笔钱显示成两个数"的工单, 所以复用 formatPlainMoney。
        expect(formatBannerAmount(3.5, { pool_currency: 'USD' })).toBe('3.5 USD');
        expect(formatBannerAmount(1.2345678, { pool_currency: 'USD' })).toBe('1.234568 USD');
        expect(formatBannerAmount(5, { pool_currency: 'USD' })).toBe('5 USD');
        expect(formatBannerAmount(0.1, { pool_currency: 'USD' })).toBe('0.1 USD');
    });

    it('没有池币种时只留金额, 不猜 CNY 也不猜 USD', () => {
        // 错标币种比不标危险得多: 把美元标成人民币会让用户以为额度差 7 倍。
        expect(formatBannerAmount(3.5, {})).toBe('3.5');
        expect(formatBannerAmount(3.5, undefined)).toBe('3.5');
    });

    it('脏币种数据不当作币种', () => {
        expect(formatBannerAmount(3.5, { pool_currency: null as unknown as string })).toBe('3.5');
        expect(formatBannerAmount(3.5, { pool_currency: '  usd  ' })).toBe('3.5 USD');
    });
});
