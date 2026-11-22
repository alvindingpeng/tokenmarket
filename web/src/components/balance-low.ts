// 余额不足横幅的判定口径。
//
// 背景: 站内通知与余额预警(op/balance_alert.go 触发 + NotifyBalanceLow 发通知)都已存在,
// 但用户不进通知中心就看不到 —— 通知是"推"的, 页面级常驻提示是"摆在那里"的, 两者互补。
//
// 这份文件只做判定与格式化, 不碰查询、不碰 DOM: 组件文件只导出组件才能保住 Fast Refresh,
// 而这些判据(尤其"余额 <= 0 不该由横幅来表达")恰恰最该被单测钉死。
//
// 数据源两个, 都是只读:
//   useWalletSummary() -> balance / frozen / pool_currency?
//   useBalanceAlert()   -> threshold_cny / enabled
// 可用余额 = balance - frozen(冻结部分是已预扣、动不了的, 拿 balance 直接比会让用户以为还能用)。

import { formatPlainMoney } from '@/lib/money';
import { amountWithPoolCurrency, poolCurrencyOf } from '@/components/modules/wallet/poolCurrency';

// WalletLike / AlertLike 只声明本模块读的字段。
//
// 为什么是"可选 + 运行时校验"而不是直接依赖 WalletSummary / BalanceAlertView:
// 响应体是别的服务给的 JSON, 字段缺失/脏数据(旧版本、中间层)都不该让横幅崩溃或编出数字。
// 类型声明拦不住运行时, 所以这里收窄需要的形状, 再把校验写在同一处。
export type WalletLike = { balance?: number; frozen?: number; pool_currency?: string };
export type AlertLike = { threshold_cny?: number; enabled?: boolean };

// HiddenReason 记录"不提示"的具体原因。
// 这不是给用户看的, 是给测试和下一个改这段代码的人看的: 没有它, "为什么这条分支不显示"
// 只能在注释里靠读, 而注释会腐坏; 有了它, 每条不提示的理由都被用例单独钉住。
export type HiddenReason =
    // 任一响应还没到(加载中)或取失败 —— 取不到就不说, 绝不把"没取到"当成 0。
    | 'no-data'
    // 金额不是有限数(NaN/Infinity): 算不出可用余额就不下结论。
    | 'not-finite'
    // 没有预警配置: 用户没画过线, 我们不替他画。
    | 'no-threshold'
    // 可用余额 <= 0: 已经是硬失败, 由后端 402 兜底, 横幅不抢这个语义。
    | 'not-positive'
    // 可用余额仍高于阈值: 没到用户自己画的线。
    | 'above-threshold';

// BalanceLowState 判定结果: 要么不说, 要么说清"可用多少、阈值多少"。
// 只有 threshold 一种说得出的形态 —— 见 no-threshold 的取舍说明。
export type BalanceLowState =
    | { kind: 'hidden'; reason: HiddenReason }
    | { kind: 'threshold'; available: number; threshold: number };

// availableBalance 可用余额 = 账面余额 - 冻结额。
// 字段缺失/类型不对时返回 NaN(而不是 0): 兜底成 0 会让"没取到"看起来像"余额是 0",
// 那正好是本仓库最禁止的那种假象。调用方据 NaN 走"不说"分支。
export function availableBalance(wallet: WalletLike | undefined): number {
    const balance = wallet?.balance;
    const frozen = wallet?.frozen;
    if (typeof balance !== 'number' || typeof frozen !== 'number') return NaN;
    return balance - frozen;
}

// readableThreshold 预警阈值是否"已配置"。
//
// threshold_cny 这个名字是历史遗留: 它的单位其实是**账面币种**(USD), 见 internal/model/balance_alert.go。
// 名字不改, 但显示时绝不能按 CNY 去理解 —— 这正是本任务要求"标币种时只信 pool_currency"的原因。
//
// threshold_cny <= 0 等价于未配置: 后端本来就会把 enabled 归一成 false, 这里再判一次是防御
// 中间层/旧数据只带 half 状态的情形(threshold=0 而 enabled 仍是 true)。
export function hasThreshold(alert: AlertLike | undefined): boolean {
    const threshold = alert?.threshold_cny;
    if (typeof threshold !== 'number' || !Number.isFinite(threshold)) return false;
    if (alert?.enabled === false) return false;
    return threshold > 0;
}

// balanceLowState 判定顶部横幅要不要出现。
//
// 判据(逐条都有用例钉住):
//   1. 任一响应没数据(加载中/失败/无内容) -> no-data。四态规则: 取不到就不说。
//   2. 可用余额非有限数 -> not-finite。
//   3. 可用余额 <= 0 -> not-positive。**刻意不提示**: 余额 <= 0 是硬失败(请求已经被后端 402 挡下),
//      那条错误本身就在告诉用户"钱不够了"。横幅再喊一遍等于抢 402 的语义, 且用户此时要做的是
//      "处理失败中的请求"而不是"顺手充点钱"; 两个提示同时出现还会让用户以为钱被清零了。
//   4. 没有预警配置 -> no-threshold。**这是本任务最需要说明的一处取舍, 见下。**
//   5. 可用余额 <= 阈值 -> threshold(显示)。
//   6. 其余 -> above-threshold。
//
// 关于第 4 条(没有预警配置时到底显不显示):
//   选择**不显示**: 没有预警配置时一律走 no-threshold, 横幅一次都不出现(不编一条自己的线)。
//   理由: 没有阈值就没有客观判据。要显示就必须挑一个数字(比如"可用不足 1 美元")——
//   在一个资金页上凭空定一个"你的钱太少了"的门槛, 正是本仓库反复禁止的那类编数字行为:
//   用户会拿它去对账, 而这个数字既不是后端算的, 也不是用户自己设的。
//   用户的诉求本来就该由用户自己表达: 没设预警时, 正确做法是引导他去设置页配置, 而不是
//   用一条我们自己发明的线替他报警。因此这里宁可什么都不说 ——
//   "不说"与"说错"之间, 前者只是少一句提示, 后者会让人把假门槛当真规则。
//   落地时已把 locales 里那条 balanceLowNoThreshold 文案删掉: 留一个永远取不到值的键,
//   下一个人会以为"这一档只是没接上", 于是把它接上一个我们编出来的门槛。
export function balanceLowState(wallet: WalletLike | undefined, alert: AlertLike | undefined): BalanceLowState {
    // 先分开"没数据"与"数据算不出有限数": 前者是这次请求没取到(加载中/失败),
    // 后者是取到了但值本身坏了。两者都不说, 但归因不同 —— 混成一个会让排查时看不出是哪一类。
    if (wallet === undefined || wallet === null) return { kind: 'hidden', reason: 'no-data' };
    if (typeof wallet.balance !== 'number' || typeof wallet.frozen !== 'number') {
        return { kind: 'hidden', reason: 'no-data' };
    }
    const available = availableBalance(wallet);
    if (!Number.isFinite(available)) return { kind: 'hidden', reason: 'not-finite' };
    if (available <= 0) return { kind: 'hidden', reason: 'not-positive' };
    if (!hasThreshold(alert)) return { kind: 'hidden', reason: 'no-threshold' };
    const threshold = (alert as { threshold_cny: number }).threshold_cny;
    if (available <= threshold) return { kind: 'threshold', available, threshold };
    return { kind: 'hidden', reason: 'above-threshold' };
}

// formatBannerAmount 横幅里金额的展示形态: "1.234567 USD"。
//
// 精度用 lib/money 的 formatPlainMoney(计费口径: 固定 6 位小数再截尾零), 不自己写 toFixed ——
// 资金页上两套精度会立刻变成"同一笔钱显示成两个数"的工单。
//
// 币种只信 wallet 的 pool_currency。缺失时**什么都不标**: 既不当 CNY(把美元读成人民币,
// 差约 7 倍), 也不当 USD(那是在猜后端)。金额本身照常显示 —— 单位缺失不等于数字缺失。
// 币种名带出来的位置由文案模板决定(模板里的 {available} 是一个完整短语)。
export function formatBannerAmount(value: number, wallet: WalletLike | undefined): string {
    return amountWithPoolCurrency(formatPlainMoney(value), poolCurrencyOf(wallet));
}
