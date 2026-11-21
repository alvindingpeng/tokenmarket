// 池币种(pool_currency)的读取与拼接。
//
// 背景: 提现口径与钱包摘要里的金额是**收益池记账币种**(后端 model.LedgerCurrency, 恒 USD),
// 而提现单/口径上的 currency、rate 是**汇率的目标币种**(恒 CNY, 语义是「1 美元折多少人民币」)。
// 两者长得像但完全不同: 拿 currency 去标金额, 就是把美元数字标成人民币 —— 用户会以为
// 自己能提出 7 倍的量。所以金额单位只能取 pool_currency 这一个来源。
//
// 该字段由后端任务(task-50)加到 WithdrawSummary / WalletSummary 的 JSON 里, 合入前
// 前端拿到的是 undefined。缺字段时的取舍是**什么都不标**: 既不猜 CNY 也不猜 USD。
// 错标一个单位比少标一个单位危险得多 —— 前者把钱的量级说错, 后者只是没说。

// PoolCurrencyCarrier 只声明本组件需要的那个字段。
// WithdrawSummary / WalletSummary 里现已声明 pool_currency?: string(web/src/api/withdrawal.ts
// 与 web/src/api/wallet.ts), 但这里仍然保留"只有自己需要的字段"这一最小形状 + unknown 入参:
// 字段是**可选**的(后端未升级时整键不存在), 运行时校验比类型断言更靠得住 —— 后端若回 null
// 或数字(脏数据/旧版本), 类型说 string 也拦不住, 而猜出来的单位会被当成事实显示在资金页上。
// 不靠 as any 绕过, 也不把可选字段硬塞进别人的类型定义。
export type PoolCurrencyCarrier = { pool_currency?: string };

// poolCurrencyOf 从任意响应体里取池币种代码: 去空白 + 转大写, 非法/缺失一律返回空串。
// 只认字符串: 后端若回 null 或数字(脏数据/旧版本), 当作「没下发」而不是猜一个出来 ——
// 猜错的单位会被当成事实展示在资金页上。调用方据空串走「不标注」分支, **不要**在这里兜底。
export function poolCurrencyOf(payload: unknown): string {
    if (typeof payload !== 'object' || payload === null) return '';
    const raw = (payload as PoolCurrencyCarrier).pool_currency;
    if (typeof raw !== 'string') return '';
    return raw.trim().toUpperCase();
}

// amountWithPoolCurrency 把金额与池币种拼成用户直接看到的那串文字: "100 USD"。
// 币种未知时只留金额本身: 金额仍是可靠事实, 不能因为单位缺失而不显示(更不能显 0)。
// 用空格分隔而不是货币符号: "$"/"¥" 在多语言环境下渲染不同且与具体币种不必对应,
// 而 ISO 代码在哪都一样, 且 USD 本来就没有专属符号。
export function amountWithPoolCurrency(amount: string, currency: string): string {
    return currency === '' ? amount : amount + ' ' + currency;
}
