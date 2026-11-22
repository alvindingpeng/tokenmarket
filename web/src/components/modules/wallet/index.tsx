import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { AlertCircle, Coins, CreditCard, Landmark, Loader2, RefreshCw, Ticket } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useWalletLedger, useWalletSummary } from '@/api/wallet';
import { useRedeem } from '@/api/redemption';
import { createRecharge, useRechargeOrders, useRechargeQuote, useRechargeView, type PayForm } from '@/api/recharge';
import { cashflowFxRate, CASHFLOW_CURRENCIES, formatFxSnapshotDay, useFxRates } from '@/api/fx';
import { formatCashflowAmount } from '@/api/currency';
import {
    applyWithdrawal,
    cancelWithdrawal,
    useWithdrawals,
    useWithdrawSummary,
    withdrawalPayoutOf,
    withdrawalSnapshotOf,
    type Withdrawal,
} from '@/api/withdrawal';
import { quoteSnapshotOf } from '@/api/recharge';
import { useAuthStore } from '@/api/user';
import { formatAmount, formatDateTime } from '@/lib/format';
import { canSubmitWithdraw, withdrawAmountIssue } from '@/lib/withdraw-min';
import { ledgerKindOptions } from '@/lib/ledger-kinds';
import { BalanceAlertSettings } from './BalanceAlertSettings';
import { BalanceAlertHistory } from './BalanceAlertHistory';
import { MyBudgetsCard } from './MyBudgetsCard';
import { amountWithPoolCurrency, poolCurrencyOf } from './poolCurrency';

// KINDS 流水筛选项; 空串表示全部。
// 类型全集收口在 lib/ledger-kinds: 曾在这里写死短名单, 结果退款与提现四态根本选不到,
// 列表里却会渲染出它们(缺文案时 use-intl 回退成 wallet.kind.refund 这种路径串)。
const KINDS = ledgerKindOptions;

// submitToGateway 完成收银台跳转: 表单渠道(易支付/支付宝)自动提交表单,
// 链接渠道(Stripe/Creem)直接跳转; 都是浏览器跳转而非接口调用。
function submitToGateway(form: PayForm) {
    if (!form.fields || Object.keys(form.fields).length === 0) {
        window.location.href = form.redirect_url;
        return;
    }
    const element = document.createElement('form');
    element.method = 'POST';
    element.action = form.action;
    element.style.display = 'none';
    for (const [name, value] of Object.entries(form.fields)) {
        const input = document.createElement('input');
        input.type = 'hidden';
        input.name = name;
        input.value = value;
        element.appendChild(input);
    }
    document.body.appendChild(element);
    element.submit();
}

// RechargeDialogContent 充值表单: 金额快捷档 + 实时报价 + 一键跳收银台。
function RechargeDialogContent() {
    const t = useTranslations('wallet');
    const { setIsOpen } = useMorphingDialog();
    const view = useRechargeView();
    const [amount, setAmount] = useState('10');
    const [provider, setProvider] = useState('');
    const [method, setMethod] = useState('');
    const [submitting, setSubmitting] = useState(false);
    // payCurrency 支付币种(可选): 空 = 跟随渠道默认(旧行为); 用户显式选择后随报价单带给服务端,
    // 旧服务端忽略该字段仍按金额报价, 新服务端按所选币种回快照语义 —— 两种后端都不需要改调用方。
    const [payCurrency, setPayCurrency] = useState('');
    const amountCents = Math.round(Number(amount) * 100) || 0;
    const quote = useRechargeQuote(amountCents, payCurrency || undefined);
    const fxRates = useFxRates();
    const configured = view.data?.configured ?? false;
    // 只列已配置渠道; 默认选第一个可用的(未显式选择时)。
    const channels = (view.data?.channels ?? []).filter((channel) => channel.configured);
    const activeChannel = channels.find((channel) => channel.id === provider) ?? channels[0];
    // 方法选项按后台白名单出; 未配置白名单时给全量默认项, "auto" 永远可用(收银台自选)。
    const methodOptions = (activeChannel?.pay_types ?? view.data?.pay_types ?? []).length > 0
        ? (activeChannel?.pay_types ?? view.data?.pay_types ?? [])
        : ["alipay", "wechat", "qq"];
    const showMethod = (activeChannel?.id ?? 'epay') === 'epay';
    // 非人民币渠道按汇率快照折算渠道币种面额, 下单前先看到实付。
    // FX 快照优先(带日/来源), 服务端静态汇率兜底; 两者都缺时显示 — 而不是猜数。
    const channelCurrency = activeChannel?.currency ?? 'CNY';
    const fxSnapshot = cashflowFxRate(fxRates.data?.items, channelCurrency, view.data?.rate);
    const foreignAmount = fxSnapshot
        ? amountCents / 100 / fxSnapshot.rate
        : null;
    // 用户显式选择支付币种时, 给出该币种对 USD 的折算口径(快照优先, 静态汇率兜底)。
    const payFx = payCurrency ? cashflowFxRate(fxRates.data?.items, payCurrency, view.data?.rate) : undefined;

    // handlePay 下单后自动提交收银台表单; 失败留在弹窗内透出原因。
    const handlePay = async () => {
        if (amountCents <= 0 || submitting) return;
        setSubmitting(true);
        try {
            const { form } = await createRecharge({ amount_cents: amountCents, provider: activeChannel?.id ?? 'epay', method });
            submitToGateway(form);
        } catch (error) {
            setSubmitting(false);
            toast.error(t('recharge.failed'), { description: (error as Error).message });
        }
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <div className="space-y-4">
                    {!configured && (
                        <p className="rounded-2xl border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">{t('recharge.notConfigured')}</p>
                    )}
                    {channels.length > 1 && (
                        <div className="space-y-2">
                            <Label>{t('recharge.channel')}</Label>
                            <div className="flex flex-wrap gap-2">
                                {channels.map((channel) => (
                                    <Button
                                        key={channel.id}
                                        type="button"
                                        variant={activeChannel?.id === channel.id ? 'default' : 'outline'}
                                        size="sm"
                                        className="h-8 rounded-xl"
                                        onClick={() => { setProvider(channel.id); setMethod(''); }}
                                    >
                                        {channel.name}
                                    </Button>
                                ))}
                            </div>
                        </div>
                    )}
                    <div className="space-y-2">
                        <Label htmlFor="recharge-amount">{t('recharge.amount')}</Label>
                        <Input
                            id="recharge-amount"
                            type="number"
                            min="0"
                            step="0.01"
                            value={amount}
                            onChange={(event) => setAmount(event.target.value)}
                            className="rounded-xl"
                        />
                        {(view.data?.amount_tiers ?? []).length > 0 && (
                            <div className="flex flex-wrap gap-2">
                                {(view.data?.amount_tiers ?? []).map((tier) => (
                                    <Button key={tier} type="button" variant="outline" size="sm" className="h-8 rounded-xl" onClick={() => setAmount(String(tier))}>
                                        {t('recharge.tier', { amount: tier })}
                                    </Button>
                                ))}
                            </div>
                        )}
                    </div>
                    {showMethod && (
                        <div className="space-y-2">
                            <Label htmlFor="recharge-method">{t('recharge.method')}</Label>
                            <Select value={method || 'auto'} onValueChange={(value) => setMethod(value === 'auto' ? '' : value)}>
                                <SelectTrigger className="rounded-xl"><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="auto">{t('recharge.methodAuto')}</SelectItem>
                                    {methodOptions.map((option) => (
                                        <SelectItem key={option} value={option}>{t('recharge.' + option)}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    )}
                    <div className="space-y-2">
                        <Label htmlFor="recharge-currency">{t('recharge.currency')}</Label>
                        <Select value={payCurrency || 'auto'} onValueChange={(value) => setPayCurrency(value === 'auto' ? '' : value)}>
                            <SelectTrigger id="recharge-currency" className="rounded-xl"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                <SelectItem value="auto">{t('recharge.currencyAuto')}</SelectItem>
                                {CASHFLOW_CURRENCIES.map((code) => (
                                    <SelectItem key={code} value={code}>{code}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {payCurrency && payFx && (
                            <p className="text-xs text-muted-foreground">
                                {t('recharge.fxTip', { rate: payFx.rate, day: formatFxSnapshotDay(payFx.day) || '-', source: payFx.source })}
                            </p>
                        )}
                    </div>
                    <div className="rounded-2xl border border-border bg-muted/40 px-3 py-2 text-sm">
                        <div className="flex items-center justify-between">
                            <span className="text-muted-foreground">{t('recharge.credits')}</span>
                            <span className="tabular-nums font-medium">{formatAmount(quote.data?.credits ?? 0)}</span>
                        </div>
                        {quote.data?.bonus ? (
                            <div className="flex items-center justify-between">
                                <span className="text-muted-foreground">{t('recharge.bonus')}</span>
                                <span className="tabular-nums font-medium text-emerald-500">+{formatAmount(quote.data.bonus)}</span>
                            </div>
                        ) : null}
                        <div className="flex items-center justify-between text-xs text-muted-foreground">
                            <span>{t('recharge.rate')}</span>
                            <span className="tabular-nums">{t('recharge.rateValue', { rate: view.data?.rate ?? 7, currency: (view.data?.currency ?? 'CNY').toUpperCase() })}</span>
                        </div>
                        {(() => {
                            const snapshot = quoteSnapshotOf(quote.data);
                            return snapshot ? (
                                <div className="flex items-center justify-between text-xs text-muted-foreground">
                                    <span>{t('recharge.snapshot')}</span>
                                    <span className="tabular-nums">{t('recharge.snapshotValue', { day: formatFxSnapshotDay(snapshot.day) || '-', source: snapshot.source || '-' })}</span>
                                </div>
                            ) : null;
                        })()}
                        {quote.data?.source_amount !== undefined && (
                            <div className="flex items-center justify-between text-xs text-muted-foreground">
                                <span>{t('recharge.sourceAmount')}</span>
                                <span className="tabular-nums">{formatAmount(quote.data.source_amount)} {(quote.data.source_currency || quote.data.credited_currency || 'CNY').toUpperCase()}</span>
                            </div>
                        )}
                        {foreignAmount !== null && amountCents > 0 ? (
                            <div className="flex items-center justify-between text-xs">
                                <span className="text-muted-foreground">{t('recharge.payForeign')}</span>
                                <span className="tabular-nums font-medium">{channelCurrency.toLowerCase() === 'usd' ? '$' : ''}{foreignAmount.toFixed(2)} {channelCurrency.toUpperCase()}</span>
                            </div>
                        ) : null}
                    </div>
                    <div className="flex justify-end gap-2">
                        <Button type="button" variant="outline" size="sm" className="rounded-xl" onClick={() => setIsOpen(false)}>{t('recharge.cancel')}</Button>
                        <Button type="button" size="sm" className="rounded-xl" disabled={!configured || amountCents <= 0 || submitting} onClick={() => void handlePay()}>
                            {submitting && <Loader2 className="size-4 animate-spin" />}
                            {t('recharge.submit')}
                        </Button>
                    </div>
                </div>
            </MorphingDialogDescription>
        </div>
    );
}

// RechargeButton 钱包页充值入口; 未配置支付时按钮置灰但仍可点开看提示。
function RechargeButton() {
    const t = useTranslations('wallet');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90">
                <CreditCard className="size-4" />
                {t('recharge.open')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <RechargeDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}


// WithdrawDialogContent 提现表单: 可提余额 + 费用预览 + 收款账户; 仅渠道商可提(D4)。
//
// 导出是为了单测能渲染弹窗正文: 真实页面里它挂在 MorphingDialogContainer 的 portal 上,
// 页面级 mock SSR 拿不到 portal 里的内容(见 poolCurrency.test.tsx)。
// initialAmount / initialPayoutCurrency 只给 SSR 用例当受控输入的初值 —— 服务端渲染
// 触发不了 onChange, 不注入初值就永远只能渲染「金额 0」那一支, 也就测不到真实金额的标注。
// 页面调用这两个 prop 时一律不传, 走下面的默认值, 行为与修前一致。
export function WithdrawDialogContent({
    onApplied,
    initialAmount = '',
    initialPayoutCurrency = 'CNY',
}: {
    onApplied: () => void;
    initialAmount?: string;
    initialPayoutCurrency?: string;
}) {
    const t = useTranslations('wallet');
    const { setIsOpen } = useMorphingDialog();
    const summary = useWithdrawSummary();
    const fxRates = useFxRates();
    const [amount, setAmount] = useState(initialAmount);
    const [payoutCurrency, setPayoutCurrency] = useState(initialPayoutCurrency);
    const [method, setMethod] = useState('alipay');
    const [accountName, setAccountName] = useState('');
    const [accountNo, setAccountNo] = useState('');
    const [submitting, setSubmitting] = useState(false);
    const value = Number(amount) || 0;
    const rules = summary.data;
    // 池币种: 提现口径里所有金额(可提/手续费/到手)的记账单位, 只能来自 pool_currency。
    // **不要**用 rules.currency —— 那是汇率的目标币种(恒 CNY, 语义是「1 美元折多少人民币」),
    // 拿它标金额等于把美元数字标成人民币。字段未下发时这里是空串, 走「不标注」分支。
    const poolCurrency = poolCurrencyOf(rules);
    // 金额前置校验: 低于最低额/超过可提余额时按钮不可点, 并给出具体差额。
    // 不这样做的话, 用户输完金额、再输一遍 step-up 密码, 才从后端拿到"低于最低额"。
    // 拿不到 min_amount 或 available 时 issue 为 undefined —— **不拦**(见 lib/withdraw-min 的说明)。
    const amountIssue = withdrawAmountIssue({
        amount: value,
        raw: amount,
        minAmount: rules?.min_amount,
        available: rules?.available,
    });
    // 费用预览: fee = max(最低费, 金额×费率), 打款按申请时汇率折算为所选币种。
    // 打款币种只影响展示口径, 申请单本身仍按收益池 USD 记账, 不破坏旧接口兼容。
    const fee = rules ? Math.min(value, Math.max(rules.fee_min, value * rules.fee_rate)) : 0;
    const actual = value - fee;
    const payoutFx = cashflowFxRate(fxRates.data?.items, payoutCurrency, rules?.rate);
    const payoutAmount = payoutFx ? actual * payoutFx.rate : undefined;
    // 组合逻辑在 lib/withdraw-min 里(canSubmitWithdraw): 修前这里漏了 min_amount, 只看 value > 0,
    // 于是「填 10 提现、最低额 50」也能点提交 —— 输完 step-up 密码才被后端拒。
    const canSubmit = canSubmitWithdraw({
        canApply: rules?.can_apply,
        amount: value,
        issue: amountIssue,
        accountName,
        accountNo,
        submitting,
    });

    // handleApply 申请提现; 失败留在弹窗透出原因(低于最低/可提不足/限额)。
    const handleApply = async () => {
        if (!canSubmit) return;
        setSubmitting(true);
        try {
            await applyWithdrawal({ amount: value, method, account_name: accountName.trim(), account_no: accountNo.trim(), payout_currency: payoutCurrency });
            toast.success(t('withdraw.applied'));
            onApplied();
            setIsOpen(false);
        } catch (error) {
            setSubmitting(false);
            toast.error(t('withdraw.failed'), { description: (error as Error).message });
        }
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <div className="space-y-4">
                    {/* 口径取不到时**必须**说清楚, 而不是走"不可提现"那条文案。
                        can_apply 在取数失败时是 undefined, 于是 ?? false 会让界面显示
                        「只有渠道商收益可提现」—— 那是一句关于账号身份的断言, 而真实原因只是这次请求挂了。
                        用户会据此去查自己是不是渠道商, 方向完全错了。 */}
                    {summary.isError && !rules ? (
                        <div role="alert" className="flex flex-wrap items-center gap-2 rounded-2xl border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                            <span className="min-w-0 flex-1">{t('summaryFailed')}</span>
                            <Button variant="outline" size="sm" className="h-7 rounded-lg px-2 text-xs" onClick={() => void summary.refetch()} disabled={summary.isRefetching}>
                                {t('retry')}
                            </Button>
                        </div>
                    ) : !(rules?.can_apply ?? false) && (
                        <p className="rounded-2xl border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">{t('withdraw.notAllowed')}</p>
                    )}
                    <div className="flex items-center justify-between rounded-2xl border border-border bg-muted/40 px-3 py-2 text-sm">
                        <span className="text-muted-foreground">{t('withdraw.available')}</span>
                        {/* 与上面五格卡片同样的口径: 取不到时显「—」, 不显 0。
                            这里是提现弹窗里最显眼的数字, 显 0 等于告诉用户"你没钱可提"。
                            后面跟的币种是池币种(pool_currency): 这个数字能不能提、提的是什么钱,
                            只有带上单位才算说清。币种缺失时只显示数字(见 lib 注释的取舍)。 */}
                        <span className="tabular-nums font-medium">
                            {rules ? amountWithPoolCurrency(formatAmount(rules.available), poolCurrency) : '—'}
                        </span>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="withdraw-amount">{t('withdraw.amount')}</Label>
                        <Input
                            id="withdraw-amount"
                            type="number"
                            min="0"
                            step="0.01"
                            value={amount}
                            onChange={(event) => setAmount(event.target.value)}
                            className="rounded-xl"
                        />
                        <p className="text-xs text-muted-foreground">
                            {/* 规则里的最低费也是池币种金额: 不标单位会让人以为"最低 1 元人民币"。
                                这里只往已有的 {min} 占位符里塞「金额+币种」, 不改文案, 所以不需要新键。 */}
                            {t('withdraw.feeHint', { min: amountWithPoolCurrency(formatAmount(rules?.fee_min ?? 1), poolCurrency), rate: Math.round((rules?.fee_rate ?? 0.01) * 1000) / 10 })}
                        </p>
                        {/* 最低额常驻显示(不只在填错时才出现): 让人在输入之前就知道门槛。
                            拿不到 min_amount 时不显示 —— 写死一个数字会与后台可配值分叉。 */}
                        {rules?.min_amount !== undefined && rules.min_amount > 0 && (
                            <p className="text-xs text-muted-foreground">{t('withdraw.minAmountHint', { min: amountWithPoolCurrency(formatAmount(rules.min_amount), poolCurrency) })}</p>
                        )}
                        {/* 三档原因分别给句子, 不合并成一句「金额不合法」:
                            输入非法要改输入, 低于最低额要加钱, 超过余额要减钱 —— 三种下一步完全不同。
                            都带 role="alert": 这几行不是静态说明, 会随输入变化而出现。 */}
                        {amountIssue?.kind === 'invalid' && (
                            <p role="alert" className="text-xs text-destructive">
                                {amountIssue.reason === 'tooManyDecimals' ? t('withdraw.tooManyDecimals') : t('withdraw.amountInvalid')}
                            </p>
                        )}
                        {amountIssue?.kind === 'belowMin' && (
                            <p role="alert" className="text-xs text-destructive">
                                {/* 只传文案里真有的占位符 {short}: 多传的参数会被忽略, 但留着会让人以为 |
                                    那个键里有 {min}, 下次改文案就会照着不存在的占位符写。最低额本身
                                    在上面的 minAmountHint 里已经常驻显示了, 这里只补"还差多少"。 */}
                                {t('withdraw.belowMin', { short: amountWithPoolCurrency(formatAmount(amountIssue.short), poolCurrency) })}
                            </p>
                        )}
                        {amountIssue?.kind === 'belowAvailable' && (
                            <p role="alert" className="text-xs text-destructive">
                                {t('withdraw.belowAvailable', { available: amountWithPoolCurrency(formatAmount(amountIssue.available), poolCurrency) })}
                            </p>
                        )}
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="withdraw-method">{t('withdraw.method')}</Label>
                        <Select value={method} onValueChange={setMethod}>
                            <SelectTrigger className="rounded-xl"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                <SelectItem value="alipay">{t('recharge.alipay')}</SelectItem>
                                <SelectItem value="wechat">{t('recharge.wechat')}</SelectItem>
                                <SelectItem value="bank">{t('withdraw.bank')}</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-2">
                            <Label htmlFor="withdraw-name">{t('withdraw.accountName')}</Label>
                            <Input id="withdraw-name" className="rounded-xl" value={accountName} onChange={(event) => setAccountName(event.target.value)} autoComplete="off" />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="withdraw-account">{t('withdraw.accountNo')}</Label>
                            <Input id="withdraw-account" className="rounded-xl" value={accountNo} onChange={(event) => setAccountNo(event.target.value)} autoComplete="off" />
                        </div>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="withdraw-currency">{t('withdraw.payoutCurrency')}</Label>
                        <Select value={payoutCurrency} onValueChange={setPayoutCurrency}>
                            <SelectTrigger id="withdraw-currency" className="rounded-xl"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                {(rules?.supported_currencies?.length ? rules.supported_currencies : [...CASHFLOW_CURRENCIES]).map((code) => (
                                    <SelectItem key={code} value={code}>{code.toUpperCase()}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {payoutFx && (
                            <p className="text-xs text-muted-foreground">
                                {t('withdraw.fxSnapshot', { rate: payoutFx.rate, day: formatFxSnapshotDay(payoutFx.day) || '-', source: payoutFx.source })}
                            </p>
                        )}
                    </div>
                    <div className="rounded-2xl border border-border bg-muted/40 px-3 py-2 text-sm">
                        <div className="flex items-center justify-between">
                            <span className="text-muted-foreground">{t('withdraw.fee')}</span>
                            <span className="tabular-nums">{amountWithPoolCurrency(formatAmount(fee), poolCurrency)}</span>
                        </div>
                        {/* 一行里并排两个币种, 必须各带各的代码, 否则用户分不清哪个数是哪种钱:
                            左边「要提多少」是**池币种**(收益池记账单位),
                            右边「≈ 折算」是**打款币种**(用户选的到账币种)。
                            修前两处都是裸数字, 只能靠旁边那句汇率提示猜, 而那句讲的是打款币种 ——
                            于是把美元的可提额当成人民币读。 */}
                        <div className="flex items-center justify-between">
                            <span className="text-muted-foreground">{t('withdraw.actual')}</span>
                            <span className="tabular-nums font-medium">
                                {amountWithPoolCurrency(formatAmount(actual), poolCurrency)} (≈{payoutAmount === undefined ? '—' : formatAmount(payoutAmount) + ' ' + payoutCurrency})
                            </span>
                        </div>
                    </div>
                    <div className="flex justify-end gap-2">
                        <Button type="button" variant="outline" size="sm" className="rounded-xl" onClick={() => setIsOpen(false)}>{t('recharge.cancel')}</Button>
                        <Button type="button" size="sm" className="rounded-xl" disabled={!canSubmit} onClick={() => void handleApply()}>
                            {submitting && <Loader2 className="size-4 animate-spin" />}
                            {t('withdraw.submit')}
                        </Button>
                    </div>
                </div>
            </MorphingDialogDescription>
        </div>
    );
}

// WithdrawButton 钱包页提现入口; 提现功能按角色屏蔽, 普通用户不渲染此入口(canWithdraw 门)。
function WithdrawButton({ onApplied }: { onApplied: () => void }) {
    const t = useTranslations('wallet');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl border border-border bg-card px-4 text-sm font-medium text-card-foreground transition-colors hover:bg-muted">
                <Landmark className="size-4" />
                {t('withdraw.open')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <WithdrawDialogContent onApplied={onApplied} />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// WithdrawalRow 提现单行: 打款额优先按服务端 payout_currency/payout_amount 展示到账币种;
// 无新字段的历史旧行按旧口径(¥ + actual_cents)显示并附 legacy 提示。
function WithdrawalRow({ order, onInvalidated }: { order: Withdrawal; onInvalidated: () => void }) {
    const t = useTranslations('wallet');
    const payout = withdrawalPayoutOf(order);
    const snapshot = withdrawalSnapshotOf(order);
    return (
        <tr className="border-t border-border">
            <td className="px-4 py-3 font-mono text-xs">#{order.id}</td>
            <td className="px-4 py-3 tabular-nums">{formatAmount(order.amount)}</td>
            <td className="px-4 py-3 tabular-nums">{formatAmount(order.fee)}</td>
            <td className="px-4 py-3 tabular-nums font-medium">
                {payout
                    ? formatCashflowAmount(payout.amount, payout.currency) + ' ' + payout.currency.toUpperCase()
                    : '¥' + formatCashflowAmount(order.actual_cents / 100, 'CNY')}
                {(() => {
                    const actual = (order.actual_currency ?? '').trim().toUpperCase();
                    const requested = (order.payout_currency ?? snapshot?.currency ?? 'CNY').trim().toUpperCase();
                    return actual && actual !== requested ? (
                        <div className="text-xs text-muted-foreground">
                            {t('withdraw.actualCurrency', { requested, actual })}
                        </div>
                    ) : null;
                })()}
                {payout && snapshot && (
                    <span className="ml-1 text-xs text-muted-foreground">
                        ({t('withdraw.snapshotShort', { day: formatFxSnapshotDay(snapshot.day) || '-', source: snapshot.source || '-' })})
                    </span>
                )}
                {!payout && (
                    <span className="ml-1 text-xs text-muted-foreground">({t('withdraw.legacy')})</span>
                )}
            </td>
            <td className="px-4 py-3">{t('wstatus.' + order.status)}</td>
            <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(order.applied_at, '-')}</td>
            <td className="px-4 py-3">
                {order.status === 'pending' ? (
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-8 rounded-xl"
                        onClick={() => cancelWithdrawal(order.id).then(() => { toast.success(t('withdraw.cancelled')); onInvalidated(); }).catch((error) => toast.error(t('withdraw.failed'), { description: (error as Error).message }))}
                    >
                        {t('withdraw.cancelOrder')}
                    </Button>
                ) : order.status === 'paid' ? (
                    <span className="text-xs text-muted-foreground">{order.paid_ref}</span>
                ) : (
                    <span className="text-xs text-muted-foreground">{order.review_note || '-'}</span>
                )}
            </td>
        </tr>
    );
}

// Wallet 钱包页: 余额卡片 + 充值 + 兑换码 + 流水/充值记录/提现记录。
export default function Wallet() {
    const t = useTranslations('wallet');
    const role = useAuthStore((state) => state.role);
    const uid = useAuthStore((state) => state.userId);
    const [tab, setTab] = useState('ledger');
    const [kind, setKind] = useState('');
    const [code, setCode] = useState('');
    const summary = useWalletSummary();
    const ledger = useWalletLedger(kind, role === 'admin' ? uid : 0);
    const orders = useRechargeOrders(role === 'admin' ? uid : 0);
    // canWithdraw 提现功能按角色屏蔽: 普通用户无提现入口与记录页签, 查询同步停发(路由层亦 403)。
    const canWithdraw = role === 'admin' || role === 'reseller';
    const withdrawals = useWithdrawals('', role === 'admin' ? uid : 0, canWithdraw);
    const queryClient = useQueryClient();
    // invalidateWithdraw 提现资金状态每变一次就作废提现缓存, 列表与口径都回服务端。
    const invalidateWithdraw = () => {
        void queryClient.invalidateQueries({ queryKey: ['withdraw'] });
    };
    const redeem = useRedeem();

    // 支付回跳只决定提示语; 入账以服务端回调为准, 回来立刻拉一次余额与订单。
    useEffect(() => {
        const params = new URLSearchParams(window.location.search);
        const result = params.get('recharge_result');
        if (!result) return;
        if (result === 'paid') {
            toast.success(t('recharge.returnPaid', { order: params.get('order_no') ?? '' }));
        } else {
            toast.error(t('recharge.returnFailed'));
        }
        window.history.replaceState({}, '', window.location.pathname);
        void summary.refetch();
        void orders.refetch();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    const items = ledger.data?.items ?? [];
    const orderItems = orders.data?.items ?? [];
    const withdrawItems = withdrawals.data?.items ?? [];
    // 五格余额卡片的取值一律是 number | undefined —— **不给 0 兜底**。
    // 兜底会把"这一次没取到"渲染成"你的余额是 0", 而资金页上这是最危险的一种假象:
    // 用户会以为钱被清零了。undefined 走下面的占位符分支, 显示「—」。
    // 五格与提现弹窗是同一份账面余额口径, 所以币种也只认 pool_currency 这一个来源;
    // 字段未下发时是空串 → 只显数字, 不猜 CNY(把美元当人民币展示比不显示更糟)。
    const poolCurrency = poolCurrencyOf(summary.data);
    const cards = [
        { key: 'balance', value: summary.data?.balance },
        { key: 'frozen', value: summary.data?.frozen },
        { key: 'recharged', value: summary.data?.total_recharged },
        { key: 'spent', value: summary.data?.total_spent },
        { key: 'revenue', value: summary.data?.total_revenue },
    ] as const;

    // handleRedeem 兑换并即时回执到账额与新余额; 失败原因透出(错码/已兑/锁定)。
    const handleRedeem = () => {
        const value = code.trim();
        if (value === '' || redeem.isPending) return;
        redeem.mutate(value, {
            onSuccess: (data) => {
                toast.success(t('redeem.ok', { credits: formatAmount(data.credits), balance: formatAmount(data.balance) }));
                setCode('');
            },
            onError: (error) => toast.error(t('redeem.failed'), { description: error.message }),
        });
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Coins className="size-5" />
                    {t('title')}
                </h2>
                <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => { void summary.refetch(); void ledger.refetch(); void orders.refetch(); }} disabled={summary.isRefetching} className="rounded-xl">
                        <RefreshCw className={'size-4' + (summary.isRefetching ? ' animate-spin' : '')} />
                        {t('refresh')}
                    </Button>
                    <RechargeButton />
                    {canWithdraw && <WithdrawButton onApplied={invalidateWithdraw} />}
                </div>
            </div>

            {/* 摘要取数失败时: 顶部一条错误条 + 重试, 五格全部显示「—」。
                只做前者不够 —— 用户可能只扫一眼数字就走, 看到 0 就以为账户空了。 */}
            {summary.isError && (
                <div role="alert" className="flex flex-wrap items-center gap-2 rounded-2xl border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                    <AlertCircle className="size-4 shrink-0" aria-hidden="true" />
                    <span className="min-w-0 flex-1">{t('summaryFailed')}</span>
                    <Button variant="outline" size="sm" className="h-7 rounded-lg px-2 text-xs" onClick={() => void summary.refetch()} disabled={summary.isRefetching}>
                        {t('retry')}
                    </Button>
                </div>
            )}
            <div className="grid shrink-0 grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
                {cards.map((card) => (
                    <div key={card.key} className="rounded-3xl border border-border bg-card px-4 py-3">
                        <div className="text-xs text-muted-foreground">{t('card.' + card.key)}</div>
                        {/* undefined 显占位符, 0 显真实的 0: 两者在数据上是不同的事实, 在界面上也必须不同。
                            金额后面跟池币种: 用户看到的是一笔具体的钱, 没有单位就只能猜是人民币。 */}
                        <div className="mt-1 truncate text-xl font-semibold tabular-nums" title={card.value === undefined ? '—' : amountWithPoolCurrency(formatAmount(card.value), poolCurrency)}>
                            {card.value === undefined ? '—' : amountWithPoolCurrency(formatAmount(card.value), poolCurrency)}
                        </div>
                    </div>
                ))}
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2 rounded-3xl border border-border bg-card px-4 py-3">
                <Ticket className="size-5 shrink-0 text-primary" />
                <div className="min-w-0 flex-1 basis-52">
                    <div className="text-sm font-medium">{t('redeem.title')}</div>
                    <div className="text-xs text-muted-foreground">{t('redeem.desc')}</div>
                </div>
                <Input
                    className="h-9 w-full rounded-xl sm:w-64"
                    placeholder={t('redeem.placeholder')}
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                    onKeyDown={(event) => { if (event.key === 'Enter') handleRedeem(); }}
                    autoComplete="off"
                />
                <Button size="sm" className="h-9 rounded-xl" onClick={handleRedeem} disabled={code.trim() === '' || redeem.isPending}>
                    {redeem.isPending && <Loader2 className="size-4 animate-spin" />}
                    {t('redeem.submit')}
                </Button>
            </div>

            <BalanceAlertSettings />
            <BalanceAlertHistory />

            <MyBudgetsCard />

            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Tabs value={tab} onValueChange={setTab}>
                    <TabsList className="rounded-2xl bg-muted p-1">
                        <TabsTrigger value="ledger" className="rounded-xl">{t('tab.ledger')}</TabsTrigger>
                        <TabsTrigger value="orders" className="rounded-xl">{t('tab.orders')}</TabsTrigger>
                        {canWithdraw && <TabsTrigger value="withdrawals" className="rounded-xl">{t('tab.withdrawals')}</TabsTrigger>}
                    </TabsList>
                </Tabs>
                {tab === 'ledger' && (
                    <Select value={kind || 'all'} onValueChange={(value) => setKind(value === 'all' ? '' : value)}>
                        <SelectTrigger className="w-40 rounded-xl"><SelectValue placeholder={t('ledger.kindAll')} /></SelectTrigger>
                        <SelectContent>
                            {KINDS.map((value) => (
                                <SelectItem key={value} value={value || 'all'}>{value === '' ? t('ledger.kindAll') : t('kind.' + value)}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            </div>

            {tab === 'ledger' ? (
                <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                    {ledger.isLoading ? (
                        <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                    ) : ledger.isError ? (
                        <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void ledger.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                    ) : items.length === 0 ? (
                        <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full min-w-[640px] text-sm">
                                <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                    <th className="px-4 py-3 font-medium">{t('table.time')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.kind')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.amount')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.balance')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.note')}</th>
                                </tr></thead>
                                <tbody>
                                    {items.map((item) => (
                                        <tr key={item.id} className="border-t border-border">
                                            <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(item.created_at, '-')}</td>
                                            <td className="px-4 py-3">{t('kind.' + item.kind)}</td>
                                            <td className={'px-4 py-3 tabular-nums font-medium ' + (item.amount >= 0 ? 'text-emerald-500' : 'text-destructive')}>{formatAmount(item.amount)}</td>
                                            <td className="px-4 py-3 tabular-nums">{formatAmount(item.balance_after)}</td>
                                            <td className="px-4 py-3 text-muted-foreground">{item.note || (item.model_name ? item.model_name : '-')}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </div>
            ) : tab === 'orders' ? (
                <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                    {orders.isLoading ? (
                        <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                    ) : orders.isError ? (
                        <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void orders.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                    ) : orderItems.length === 0 ? (
                        <div className="flex h-full items-center justify-center text-muted-foreground">{t('orders.empty')}</div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full min-w-[720px] text-sm">
                                <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                    <th className="px-4 py-3 font-medium">{t('orders.no')}</th>
                                    <th className="px-4 py-3 font-medium">{t('orders.amount')}</th>
                                    <th className="px-4 py-3 font-medium">{t('orders.credits')}</th>
                                    <th className="px-4 py-3 font-medium">{t('orders.status')}</th>
                                    <th className="px-4 py-3 font-medium">{t('orders.time')}</th>
                                </tr></thead>
                                <tbody>
                                    {orderItems.map((order) => (
                                        <tr key={order.id} className="border-t border-border">
                                            <td className="px-4 py-3 font-mono text-xs">{order.order_no}</td>
                                            <td className="px-4 py-3 tabular-nums">
                                                {formatAmount(order.amount_cents / 100)} {order.currency}
                                                {order.snapshot_day && (
                                                    <div className="text-xs text-muted-foreground">
                                                        {t('orders.snapshotShort', { day: formatFxSnapshotDay(order.snapshot_day) || '-', source: order.rate_source || '-' })}
                                                    </div>
                                                )}
                                            </td>
                                            <td className="px-4 py-3 tabular-nums">{formatAmount(order.credits + order.bonus)}</td>
                                            <td className="px-4 py-3">{t('status.' + order.status)}</td>
                                            <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(order.paid_at ?? order.created_at, '-')}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </div>
            ) : (
                <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                    {withdrawals.isLoading ? (
                        <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                    ) : withdrawals.isError ? (
                        <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void withdrawals.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                    ) : withdrawItems.length === 0 ? (
                        <div className="flex h-full items-center justify-center text-muted-foreground">{t('withdraw.empty')}</div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full min-w-[760px] text-sm">
                                <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                    <th className="px-4 py-3 font-medium">{t('wtable.id')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.amount')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.fee')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.actual')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.status')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.time')}</th>
                                    <th className="px-4 py-3 font-medium">{t('wtable.actions')}</th>
                                </tr></thead>
                                <tbody>
                                    {withdrawItems.map((order) => (
                                        <WithdrawalRow key={order.id} order={order} onInvalidated={invalidateWithdraw} />
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}


