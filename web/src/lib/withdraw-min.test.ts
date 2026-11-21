import { describe, expect, it } from 'vitest';
import { canSubmitWithdraw, withdrawAmountIssue } from './withdraw-min';

// 提现前置校验。这里守的是三件相反的事:
//   1) 该拦的拦(输入非法、低于最低额、超过可提余额) —— 否则用户输完 step-up 密码才被后端拒;
//   2) 不该拦的绝不拦(口径拿不到时) —— 拦住正确操作是硬故障, 比放行更糟;
//   3) 三档原因不许混(输入非法 ≠ 余额不足), 否则提示会把人带到错误的下一步。
//
// 后端口径(internal/op/withdrawal.go:171): if spec.Amount < rules.minAmount,
// 比较的是**原始申请额**而不是扣费后的到手额。

describe('withdrawAmountIssue 输入合法性(最优先)', () => {
    it('金额为 0 或负数时报「输入不合法」, 不报「低于最低额」', () => {
        // 填个负数却得到「还差 N 才到最低额」, 那句提示在说一件不相干的事。
        expect(withdrawAmountIssue({ amount: 0, minAmount: 50, available: 100 })).toEqual({ kind: 'invalid', reason: 'notPositive' });
        expect(withdrawAmountIssue({ amount: -5, minAmount: 50, available: 100 })).toEqual({ kind: 'invalid', reason: 'notPositive' });
    });

    it('超过两位小数时报「小数位过多」', () => {
        // 收益池列是 decimal(18,6), 但提现申请精确到 0.001 在任何打款通道上都执行不了。
        expect(withdrawAmountIssue({ amount: 50.001, raw: '50.001', minAmount: 10, available: 100 }))
            .toEqual({ kind: 'invalid', reason: 'tooManyDecimals' });
    });

    it('两位小数与整数都通过', () => {
        expect(withdrawAmountIssue({ amount: 50.99, raw: '50.99', minAmount: 10, available: 100 })).toBeUndefined();
        expect(withdrawAmountIssue({ amount: 50, raw: '50', minAmount: 10, available: 100 })).toBeUndefined();
    });

    it('空输入不是错误(一打开弹窗不该顶红字)', () => {
        expect(withdrawAmountIssue({ amount: 0, raw: '', minAmount: 50, available: 100 })).toBeUndefined();
    });
});

describe('withdrawAmountIssue 最低额', () => {
    it('低于最低额时给出最低额与差额', () => {
        expect(withdrawAmountIssue({ amount: 20, minAmount: 50, available: 100 }))
            .toEqual({ kind: 'belowMin', min: 50, short: 30 });
    });

    it('差额保留到分, 不出现浮点尾巴', () => {
        // 50 - 12.3 在浮点里是 37.699999999999996。
        const issue = withdrawAmountIssue({ amount: 12.3, minAmount: 50, available: 100 });
        expect(issue?.kind).toBe('belowMin');
        expect((issue as { short: number }).short).toBe(37.7);
    });

    it('恰好等于最低额时通过(边界是 >=, 不是 >)', () => {
        // 后端用的是 < 所以 50 通过。前端用 <= 会拦掉正好填 50 的正常操作。
        expect(withdrawAmountIssue({ amount: 50, minAmount: 50, available: 100 })).toBeUndefined();
    });

    it('拿不到 min_amount 时不拦(后台可配值, 前端不硬编)', () => {
        // 管理员可以把最低额调到 10; 前端写死 50 会把 10~50 的合规申请全拦掉。
        expect(withdrawAmountIssue({ amount: 20, available: 100 })).toBeUndefined();
        expect(withdrawAmountIssue({ amount: 20, minAmount: 0, available: 100 })).toBeUndefined();
        expect(withdrawAmountIssue({ amount: 20, minAmount: Number.NaN, available: 100 })).toBeUndefined();
    });

    it('最低额优先于余额报错', () => {
        // 两个都不过时先说最低额: 余额补到 20 仍然提不出来。
        expect(withdrawAmountIssue({ amount: 10, minAmount: 50, available: 5 })?.kind).toBe('belowMin');
    });
});

describe('withdrawAmountIssue 可提余额', () => {
    it('超过可提余额时给出可用额', () => {
        expect(withdrawAmountIssue({ amount: 150, minAmount: 50, available: 100 }))
            .toEqual({ kind: 'belowAvailable', available: 100 });
    });

    it('拿不到可提余额时不拦', () => {
        expect(withdrawAmountIssue({ amount: 20, minAmount: 10 })).toBeUndefined();
        expect(withdrawAmountIssue({ amount: 20, minAmount: 10, available: Number.NaN })).toBeUndefined();
    });

    it('可提余额为 0 是已知事实, 照样能拦(不能当成缺值放过)', () => {
        expect(withdrawAmountIssue({ amount: 20, minAmount: 10, available: 0 })?.kind).toBe('belowAvailable');
    });
});

describe('withdrawAmountIssue 手续费口径', () => {
    it('按原始申请额比较, 不按到手额', () => {
        // 若实现改成用到手额比较, 这条会挂 —— 而那种实现会让「填 51」被无辜拦住。
        expect(withdrawAmountIssue({ amount: 51, minAmount: 50, available: 100 })).toBeUndefined();
    });
});

describe('canSubmitWithdraw', () => {
    const ready = { canApply: true, amount: 50, issue: undefined, accountName: '张三', accountNo: '6222', submitting: false };

    it('各项都齐时放行', () => {
        expect(canSubmitWithdraw(ready)).toBe(true);
    });

    it('金额低于最低额时拦住(修前这里放行)', () => {
        // 修前 canSubmit 只看 value > 0, 于是填 10 也能点提交, 输完 step-up 密码才被后端拒。
        expect(canSubmitWithdraw({ ...ready, amount: 10, issue: { kind: 'belowMin', min: 50, short: 40 } })).toBe(false);
    });

    it('超过可提余额时拦住', () => {
        expect(canSubmitWithdraw({ ...ready, amount: 150, issue: { kind: 'belowAvailable', available: 100 } })).toBe(false);
    });

    it('输入非法时拦住', () => {
        expect(canSubmitWithdraw({ ...ready, amount: 50.001, issue: { kind: 'invalid', reason: 'tooManyDecimals' } })).toBe(false);
    });

    it('不满足提现资格时拦住(哪怕金额没问题)', () => {
        expect(canSubmitWithdraw({ ...ready, canApply: false })).toBe(false);
        expect(canSubmitWithdraw({ ...ready, canApply: undefined })).toBe(false);
    });

    it('收款信息缺失时拦住, 纯空白不算填了', () => {
        expect(canSubmitWithdraw({ ...ready, accountNo: '   ' })).toBe(false);
    });

    it('提交中拦住(防连点重复提单)', () => {
        expect(canSubmitWithdraw({ ...ready, submitting: true })).toBe(false);
    });

    it('口径拿不到(issue 为 undefined)时**不**因为校验而拦住', () => {
        // 这条与「不该拦的绝不拦」对齐: 拿不到 min_amount 时 issue 是 undefined,
        // 此时只有资格/金额/收款信息这几条硬条件在起作用。
        expect(canSubmitWithdraw({ ...ready, amount: 20 })).toBe(true);
    });
});
