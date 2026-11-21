// 提现金额的前置校验。
//
// 为什么放在 lib 而不是组件里: 这段判断要能被单测直接断言(边界、缺值、余额不足、输入非法),
// 而 wallet/index.tsx 是组件文件 —— 往那里加非组件导出会触碰 react-refresh 的
// only-export-components 规则(本仓库不允许改 eslint 配置)。
//
// 口径说明(照后端抄的, 不是猜的):
//   · internal/op/withdrawal.go:171 是 `if spec.Amount < rules.minAmount`, 比较的是
//     **原始申请额**而不是扣手续费后的到手额。前端若按到手额比较, 会把本来合规的申请拦下来
//     (金额 51、手续费 1.5、到手 49.5: 后端判 51 >= 50 通过, 前端判 49.5 < 50 拦住)。
//   · 收益池是 USD 口径(internal/model/setting.go:81 注释「金额为收益池口径(USD)」),
//     所以 amount / min_amount / available 三个数同介质, **不需要也不应该**做汇率换算。
//   · 这三道闸都是**提前告知**, 后端还会再校验一次。因此判不过只禁用提交按钮,
//     绝不锁输入框 —— 锁住之后用户连改都没法改, 只能刷新页面。

export type WithdrawAmountIssue =
    | { kind: 'invalid'; reason: 'notPositive' | 'tooManyDecimals' }  // 输入本身不合法
    | { kind: 'belowMin'; min: number; short: number }                // 低于最低额
    | { kind: 'belowAvailable'; available: number };                  // 超过可提余额

// 收益池最多几位小数: 后端列是 decimal(18,6), 但界面上让人填到分就够了 ——
// 收益池金额本身来自计费结算, 精确到 0.000001 的提现申请在任何打款通道上都执行不了。
const MAX_DECIMALS = 2;

// withdrawAmountIssue 按优先级返回失败原因; undefined 表示通过, 或「没有可靠口径, 不拦」。
//
// 优先级是刻意排的: 输入非法 > 低于最低额 > 超过余额。
//   · 输入非法先判 —— 否则填个负数会得到「还差 N 才到最低额」, 那句提示在说一件不相干的事;
//   · 最低额先于余额 —— 两个都不过时先说更基础的那个: 余额补到 20 仍然提不出来,
//     先说余额会把人带到错误的下一步。
export function withdrawAmountIssue(input: {
    amount: number;
    raw?: string; // 原始输入串, 用来判小数位; 不传则跳过小数位检查
    minAmount?: number;
    available?: number;
}): WithdrawAmountIssue | undefined {
    const { amount, raw, minAmount, available } = input;
    // 空串 = 还没填, 不是错误: 一打开弹窗就顶一行红字, 那是噪音不是提示。
    if (raw !== undefined && raw.trim() === '') return undefined;
    // 金额本身还没填(或非法)时不报下面两道 —— 它们的前提是「有个合法的数额」。
    if (!Number.isFinite(amount) || amount <= 0) return { kind: 'invalid', reason: 'notPositive' };

    // 小数位检查放在这里而不是交给 type=number: 浏览器对 0.001 这类值是否可输入取决于 step,
    // 而 step 只能约束箭头按钮, 手输照样能进。
    if (raw !== undefined) {
        const fraction = raw.trim().split('.')[1];
        if (fraction !== undefined && fraction.length > MAX_DECIMALS) {
            return { kind: 'invalid', reason: 'tooManyDecimals' };
        }
    }

    // 拿不到 min(undefined / NaN / <= 0)时不拦: 后端有默认值兜底, 前端硬编一个数字
    // 只会与后台可配值分叉 —— 管理员把最低额调到 10, 前端还在按 50 拦, 用户就提不出来了。
    const minKnown = typeof minAmount === 'number' && Number.isFinite(minAmount) && minAmount > 0;
    if (minKnown && amount < minAmount) {
        // short 保留到分: 显示「还差 12.300000000000004」比不显示更糟。
        return { kind: 'belowMin', min: minAmount, short: Math.round((minAmount - amount) * 100) / 100 };
    }

    // available: 0 是已知事实(账上确实没钱), 与 undefined(这次没取到)必须区别对待。
    const availableKnown = typeof available === 'number' && Number.isFinite(available) && available >= 0;
    if (availableKnown && amount > available) {
        return { kind: 'belowAvailable', available };
    }

    return undefined;
}

// canSubmitWithdraw 提交按钮是否可用。
//
// 这一段此前漏掉了 min_amount(只看 value > 0), 于是「填 10 提现、最低额 50」也能点提交 ——
// 用户输完金额、再输一遍 step-up 密码, 才从后端拿到「低于最低额」。
// 抽成纯函数是为了让这条组合逻辑(rules 可用 + 金额合法 + 收款信息齐 + 未在提交中)可被直接断言。
export function canSubmitWithdraw(input: {
    canApply: boolean | undefined;
    amount: number;
    issue: WithdrawAmountIssue | undefined;
    accountName: string;
    accountNo: string;
    submitting: boolean;
}): boolean {
    const { canApply, amount, issue, accountName, accountNo, submitting } = input;
    if (!(canApply ?? false)) return false;
    if (submitting) return false;
    if (amount <= 0) return false;
    // issue 非空即不放行 —— 三档(输入非法/低于最低额/超过余额)都在这里统一挡住。
    // 校验不通过只禁用按钮、**不锁输入框**: 锁住之后用户连改都没法改, 只能刷新页面。
    if (issue !== undefined) return false;
    return accountName.trim() !== '' && accountNo.trim() !== '';
}
