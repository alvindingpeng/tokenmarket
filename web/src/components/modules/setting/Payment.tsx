import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { CreditCard, Loader2, Save } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSetSetting, useSettingList } from '@/api/setting';
import { isFieldUnsaved, summarizeBatchSave, unsavedKeys, type BatchSaveOutcome } from './paymentSave';
import { PaymentUnsavedNotice } from './PaymentUnsavedNotice';

// PAYMENT_FIELDS 支付配置字段: 全部为后台可配参数, 商户信息后配即可用。
const PAYMENT_FIELDS = [
    'epay_gateway_url',
    'epay_pid',
    'epay_key',
    'epay_pay_types',
    'alipay_gateway_url',
    'alipay_app_id',
    'alipay_private_key',
    'alipay_public_key',
    'stripe_secret_key',
    'stripe_webhook_secret',
    'stripe_api_base',
    'stripe_currency',
    'creem_api_key',
    'creem_webhook_secret',
    'creem_api_base',
    'creem_products',
    'recharge_notify_base',
    'recharge_rate',
    'recharge_min_cny',
    'recharge_amount_tiers',
    'recharge_bonus_rules',
    'withdrawal_min_amount',
    'withdrawal_fee_rate',
    'withdrawal_fee_min',
    'withdrawal_cooldown_days',
    'withdrawal_daily_count_limit',
    'withdrawal_daily_amount_limit',
    'withdrawal_account_lock_days',
] as const;

// SettingPayment 充值与支付配置: 易支付商户参数、汇率、满赠规则, 全部参数化后配即生效。
export function SettingPayment() {
    const t = useTranslations('setting');
    const settings = useSettingList();
    const setSetting = useSetSetting();
    const [draft, setDraft] = useState<Record<string, string>>({});
    const [saving, setSaving] = useState(false);
    // 上一次批量保存的部分成功结果: 哪几项没落库。
    // 保留成 state 而不是只弹一次 toast —— toast 会自己消失, 而这几个输入框在用户重开页面之前
    // 一直是"看着像已保存"的。标记必须常驻到下次保存成功为止。
    const [unsaved, setUnsaved] = useState<BatchSaveOutcome | null>(null);

    // valueOf 未编辑时直接取已存值, 编辑后取草稿; 避免用 effect 回灌状态。
    const valueOf = (key: string) => draft[key] ?? (settings.data ?? []).find((item) => item.key === key)?.value ?? '';
    const setValue = (key: string, value: string) => setDraft((prev) => ({ ...prev, [key]: value }));

    // saveAll 逐项提交; 每一项都是独立设置键, 保存后立即生效, 不需要重启。
    //
    // 关键改动: 只提交**真正改动过**的键, 且失败后不再假装整体成功。
    // 之前的写法有两处问题:
    //   1) 把整张表全部重提交一遍(包括没改的项), 一次失败会连带把没改的项也报成"没保存";
    //   2) 失败只弹一句 toast, 界面停在"全部新值"的样子 —— 用户不知道到底是哪几项没成,
    //      而配置是"一半新一半旧"的状态, 比全旧更难排查(比如满赠开关开了、规则没存)。
    const saveAll = async () => {
        // 只提交改动过的键: 没改的项重发一次没有意义, 还会把无关的失败算到本次头上。
        // 显式标 string[]: PAYMENT_FIELDS 是 as const 元组, filter 之后仍是那个字面量联合类型,
        // 而满赠开关不在这个元组里(它有自己的 Switch 控件) —— 不拓宽就 push 不进去。
        const pending: string[] = PAYMENT_FIELDS.filter((key) => draft[key] !== undefined);
        if (draft['recharge_bonus_enabled'] !== undefined) pending.push('recharge_bonus_enabled');
        if (pending.length === 0) {
            // 没有任何改动时明确说"没有需要保存的内容", 而不是弹一个"已保存"让人以为执行了什么。
            toast.info(t('payment.nothingToSave'));
            return;
        }
        setSaving(true);
        let savedCount = 0;
        let failedKey = '';
        for (const key of pending) {
            try {
                await setSetting.mutateAsync({ key, value: draft[key] });
                savedCount += 1;
            } catch {
                failedKey = key;
                break;
            }
        }
        const outcome = summarizeBatchSave(pending, savedCount, failedKey);
        if (outcome.done) {
            setDraft({});
            setUnsaved(null);
            toast.success(t('payment.saved'));
        } else {
            // 已成功的项从草稿里划掉, 未成功的原样保留 —— 用户再点一次保存就能只重试剩下那些。
            setDraft((prev) => {
                const next = { ...prev };
                for (const key of outcome.saved) delete next[key];
                return next;
            });
            setUnsaved(outcome);
            const missing = unsavedKeys(outcome);
            toast.error(t('payment.partialFailed', {
                saved: outcome.saved.length,
                total: pending.length,
                keys: missing.join(', '),
            }));
        }
        setSaving(false);
    };

    // fieldUnsaved 该输入框是否属于「上次没保存成功」的项。
    // 按 key 精确到格子: 整表标红等于没标 —— 用户据此判断"我改了哪几项还要重试",
    // 整表标红只会让他把整张表从头再确认一遍, 而那正是这段代码想省掉的事。
    const fieldUnsaved = (key: string) => isFieldUnsaved(unsaved, key);

    // fieldClass 输入框样式; 未保存的项加一圈红色描边, 让人一眼看到改哪里。
    // 只加描边不加禁用: 用户要能继续改(改完再保存), 锁住输入框等于把错误变成死路。
    const fieldClass = (key: string) => (fieldUnsaved(key)
        ? 'rounded-xl border-destructive ring-1 ring-destructive/40'
        : 'rounded-xl');

    return (
        <div className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <CreditCard className="size-5" />
                {t('payment.title')}
            </h2>
            <p className="text-xs text-muted-foreground">{t('payment.desc')}</p>

            {/* 部分失败时常驻的错误条(组件在 PaymentUnsavedNotice): 列出没保存的键。
                与 toast 的区别是它不会自己消失 —— 用户可能滚到页面另一头才发现, 那时 toast 早没了。 */}
            {unsaved && <PaymentUnsavedNotice outcome={unsaved} onDismiss={() => setUnsaved(null)} />}

            <div className="space-y-2">
                <Label htmlFor="pay-gateway">{t('payment.gatewayURL')}</Label>
                <Input id="pay-gateway" className={fieldClass('epay_gateway_url')} aria-invalid={fieldUnsaved('epay_gateway_url')} title={fieldUnsaved('epay_gateway_url') ? t('payment.unsavedMark') : undefined} placeholder="https://pay.example.com" value={valueOf('epay_gateway_url')} onChange={(event) => setValue('epay_gateway_url', event.target.value)} autoComplete="off" />
            </div>
            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-pid">{t('payment.pid')}</Label>
                    <Input id="pay-pid" className={fieldClass('epay_pid')} aria-invalid={fieldUnsaved('epay_pid')} title={fieldUnsaved('epay_pid') ? t('payment.unsavedMark') : undefined} value={valueOf('epay_pid')} onChange={(event) => setValue('epay_pid', event.target.value)} autoComplete="off" />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-key">{t('payment.key')}</Label>
                    <Input id="pay-key" type="password" className={fieldClass('epay_key')} aria-invalid={fieldUnsaved('epay_key')} title={fieldUnsaved('epay_key') ? t('payment.unsavedMark') : undefined} placeholder={t('payment.keyPlaceholder')} value={valueOf('epay_key')} onChange={(event) => setValue('epay_key', event.target.value)} autoComplete="new-password" />
                </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-types">{t('payment.payTypes')}</Label>
                    <Input id="pay-types" className={fieldClass('epay_pay_types')} aria-invalid={fieldUnsaved('epay_pay_types')} title={fieldUnsaved('epay_pay_types') ? t('payment.unsavedMark') : undefined} placeholder="alipay,wechat,qq" value={valueOf('epay_pay_types')} onChange={(event) => setValue('epay_pay_types', event.target.value)} autoComplete="off" />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-notify">{t('payment.notifyBase')}</Label>
                    <Input id="pay-notify" className={fieldClass('recharge_notify_base')} aria-invalid={fieldUnsaved('recharge_notify_base')} title={fieldUnsaved('recharge_notify_base') ? t('payment.unsavedMark') : undefined} placeholder="https://api.example.com" value={valueOf('recharge_notify_base')} onChange={(event) => setValue('recharge_notify_base', event.target.value)} autoComplete="off" />
                </div>
            </div>
            <p className="text-xs text-muted-foreground">{t('payment.notifyHint')}</p>

            {/* 支付宝官方直连: RSA2 密钥对, 留空不启用该渠道 */}
            <div className="space-y-3 rounded-2xl border border-border p-3">
                <p className="text-sm font-medium text-card-foreground">{t('payment.alipaySection')}</p>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="alipay-gateway">{t('payment.alipayGateway')}</Label>
                        <Input id="alipay-gateway" className={fieldClass('alipay_gateway_url')} aria-invalid={fieldUnsaved('alipay_gateway_url')} title={fieldUnsaved('alipay_gateway_url') ? t('payment.unsavedMark') : undefined} placeholder="https://openapi.alipay.com/gateway.do" value={valueOf('alipay_gateway_url')} onChange={(event) => setValue('alipay_gateway_url', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="alipay-appid">{t('payment.alipayAppID')}</Label>
                        <Input id="alipay-appid" className={fieldClass('alipay_app_id')} aria-invalid={fieldUnsaved('alipay_app_id')} title={fieldUnsaved('alipay_app_id') ? t('payment.unsavedMark') : undefined} value={valueOf('alipay_app_id')} onChange={(event) => setValue('alipay_app_id', event.target.value)} autoComplete="off" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="alipay-private">{t('payment.alipayPrivateKey')}</Label>
                        <Input id="alipay-private" type="password" className={fieldClass('alipay_private_key')} aria-invalid={fieldUnsaved('alipay_private_key')} title={fieldUnsaved('alipay_private_key') ? t('payment.unsavedMark') : undefined} placeholder={t('payment.keyPlaceholder')} value={valueOf('alipay_private_key')} onChange={(event) => setValue('alipay_private_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="alipay-public">{t('payment.alipayPublicKey')}</Label>
                        <Input id="alipay-public" type="password" className={fieldClass('alipay_public_key')} aria-invalid={fieldUnsaved('alipay_public_key')} title={fieldUnsaved('alipay_public_key') ? t('payment.unsavedMark') : undefined} value={valueOf('alipay_public_key')} onChange={(event) => setValue('alipay_public_key', event.target.value)} autoComplete="new-password" />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('payment.alipayHint')}</p>
            </div>

            {/* Stripe: 卡收款, 币种可配(默认 usd, 按汇率快照折算) */}
            <div className="space-y-3 rounded-2xl border border-border p-3">
                <p className="text-sm font-medium text-card-foreground">{t('payment.stripeSection')}</p>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="stripe-key">{t('payment.stripeSecretKey')}</Label>
                        <Input id="stripe-key" type="password" className={fieldClass('stripe_secret_key')} aria-invalid={fieldUnsaved('stripe_secret_key')} title={fieldUnsaved('stripe_secret_key') ? t('payment.unsavedMark') : undefined} placeholder="sk_..." value={valueOf('stripe_secret_key')} onChange={(event) => setValue('stripe_secret_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="stripe-webhook">{t('payment.stripeWebhook')}</Label>
                        <Input id="stripe-webhook" type="password" className={fieldClass('stripe_webhook_secret')} aria-invalid={fieldUnsaved('stripe_webhook_secret')} title={fieldUnsaved('stripe_webhook_secret') ? t('payment.unsavedMark') : undefined} placeholder="whsec_..." value={valueOf('stripe_webhook_secret')} onChange={(event) => setValue('stripe_webhook_secret', event.target.value)} autoComplete="new-password" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="stripe-base">{t('payment.stripeAPIBase')}</Label>
                        <Input id="stripe-base" className={fieldClass('stripe_api_base')} aria-invalid={fieldUnsaved('stripe_api_base')} title={fieldUnsaved('stripe_api_base') ? t('payment.unsavedMark') : undefined} placeholder="https://api.stripe.com" value={valueOf('stripe_api_base')} onChange={(event) => setValue('stripe_api_base', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="stripe-currency">{t('payment.stripeCurrency')}</Label>
                        <Input id="stripe-currency" className={fieldClass('stripe_currency')} aria-invalid={fieldUnsaved('stripe_currency')} title={fieldUnsaved('stripe_currency') ? t('payment.unsavedMark') : undefined} placeholder="usd" value={valueOf('stripe_currency')} onChange={(event) => setValue('stripe_currency', event.target.value)} autoComplete="off" />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('payment.stripeHint')}</p>
            </div>

            {/* Creem: 商品化档位, 金额档对应 Creem 后台商品 */}
            <div className="space-y-3 rounded-2xl border border-border p-3">
                <p className="text-sm font-medium text-card-foreground">{t('payment.creemSection')}</p>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="creem-key">{t('payment.creemAPIKey')}</Label>
                        <Input id="creem-key" type="password" className={fieldClass('creem_api_key')} aria-invalid={fieldUnsaved('creem_api_key')} title={fieldUnsaved('creem_api_key') ? t('payment.unsavedMark') : undefined} value={valueOf('creem_api_key')} onChange={(event) => setValue('creem_api_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="creem-webhook">{t('payment.creemWebhook')}</Label>
                        <Input id="creem-webhook" type="password" className={fieldClass('creem_webhook_secret')} aria-invalid={fieldUnsaved('creem_webhook_secret')} title={fieldUnsaved('creem_webhook_secret') ? t('payment.unsavedMark') : undefined} value={valueOf('creem_webhook_secret')} onChange={(event) => setValue('creem_webhook_secret', event.target.value)} autoComplete="new-password" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="creem-base">{t('payment.creemAPIBase')}</Label>
                        <Input id="creem-base" className={fieldClass('creem_api_base')} aria-invalid={fieldUnsaved('creem_api_base')} title={fieldUnsaved('creem_api_base') ? t('payment.unsavedMark') : undefined} placeholder="https://api.creem.io" value={valueOf('creem_api_base')} onChange={(event) => setValue('creem_api_base', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="creem-products">{t('payment.creemProducts')}</Label>
                        <Input id="creem-products" className={fieldClass('creem_products')} aria-invalid={fieldUnsaved('creem_products')} title={fieldUnsaved('creem_products') ? t('payment.unsavedMark') : undefined} placeholder='[{"product_id":"prod_x","name":"充值","amount_cents":7000,"credits":10}]' value={valueOf('creem_products')} onChange={(event) => setValue('creem_products', event.target.value)} autoComplete="off" />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('payment.creemHint')}</p>
            </div>

            <div className="grid grid-cols-3 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-rate">{t('payment.rate')}</Label>
                    <Input id="pay-rate" type="number" min="0.1" step="0.1" className={fieldClass('recharge_rate')} aria-invalid={fieldUnsaved('recharge_rate')} title={fieldUnsaved('recharge_rate') ? t('payment.unsavedMark') : undefined} value={valueOf('recharge_rate')} onChange={(event) => setValue('recharge_rate', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-min">{t('payment.minCNY')}</Label>
                    <Input id="pay-min" type="number" min="0" step="0.01" className={fieldClass('recharge_min_cny')} aria-invalid={fieldUnsaved('recharge_min_cny')} title={fieldUnsaved('recharge_min_cny') ? t('payment.unsavedMark') : undefined} value={valueOf('recharge_min_cny')} onChange={(event) => setValue('recharge_min_cny', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-tiers">{t('payment.tiers')}</Label>
                    <Input id="pay-tiers" className={fieldClass('recharge_amount_tiers')} aria-invalid={fieldUnsaved('recharge_amount_tiers')} title={fieldUnsaved('recharge_amount_tiers') ? t('payment.unsavedMark') : undefined} placeholder="10,50,100,500" value={valueOf('recharge_amount_tiers')} onChange={(event) => setValue('recharge_amount_tiers', event.target.value)} autoComplete="off" />
                </div>
            </div>

            <div className="flex items-center justify-between gap-3 rounded-2xl border border-border px-3 py-2">
                <div>
                    <div className="text-sm font-medium">{t('payment.bonusEnabled')}</div>
                    <div className="text-xs text-muted-foreground">{t('payment.bonusHint')}</div>
                </div>
                <Switch
                    checked={(draft['recharge_bonus_enabled'] ?? (valueOf('recharge_bonus_enabled') || 'false')) === 'true'}
                    onCheckedChange={(checked) => setValue('recharge_bonus_enabled', checked ? 'true' : 'false')}
                />
            </div>
            <div className="space-y-2">
                <Label htmlFor="pay-bonus">{t('payment.bonusRules')}</Label>
                <Input id="pay-bonus" className={fieldClass('recharge_bonus_rules')} aria-invalid={fieldUnsaved('recharge_bonus_rules')} title={fieldUnsaved('recharge_bonus_rules') ? t('payment.unsavedMark') : undefined} placeholder="100:5,500:10" value={valueOf('recharge_bonus_rules')} onChange={(event) => setValue('recharge_bonus_rules', event.target.value)} autoComplete="off" />
                <p className="text-xs text-muted-foreground">{t('payment.bonusRulesHint')}</p>
            </div>


            <div className="space-y-3 rounded-2xl border border-border px-3 py-3">
                <div className="text-sm font-medium">{t('payment.withdrawTitle')}</div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="wd-min">{t('payment.withdrawMinAmount')}</Label>
                        <Input id="wd-min" type="number" min="0" step="0.01" className={fieldClass('withdrawal_min_amount')} aria-invalid={fieldUnsaved('withdrawal_min_amount')} title={fieldUnsaved('withdrawal_min_amount') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_min_amount')} onChange={(event) => setValue('withdrawal_min_amount', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-fee-rate">{t('payment.withdrawFeeRate')}</Label>
                        <Input id="wd-fee-rate" type="number" min="0" max="1" step="0.001" className={fieldClass('withdrawal_fee_rate')} aria-invalid={fieldUnsaved('withdrawal_fee_rate')} title={fieldUnsaved('withdrawal_fee_rate') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_fee_rate')} onChange={(event) => setValue('withdrawal_fee_rate', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-fee-min">{t('payment.withdrawFeeMin')}</Label>
                        <Input id="wd-fee-min" type="number" min="0" step="0.01" className={fieldClass('withdrawal_fee_min')} aria-invalid={fieldUnsaved('withdrawal_fee_min')} title={fieldUnsaved('withdrawal_fee_min') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_fee_min')} onChange={(event) => setValue('withdrawal_fee_min', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-cooldown">{t('payment.withdrawCooldownDays')}</Label>
                        <Input id="wd-cooldown" type="number" min="0" step="1" className={fieldClass('withdrawal_cooldown_days')} aria-invalid={fieldUnsaved('withdrawal_cooldown_days')} title={fieldUnsaved('withdrawal_cooldown_days') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_cooldown_days')} onChange={(event) => setValue('withdrawal_cooldown_days', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-count">{t('payment.withdrawDailyCountLimit')}</Label>
                        <Input id="wd-count" type="number" min="0" step="1" className={fieldClass('withdrawal_daily_count_limit')} aria-invalid={fieldUnsaved('withdrawal_daily_count_limit')} title={fieldUnsaved('withdrawal_daily_count_limit') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_daily_count_limit')} onChange={(event) => setValue('withdrawal_daily_count_limit', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-amount">{t('payment.withdrawDailyAmountLimit')}</Label>
                        <Input id="wd-amount" type="number" min="0" step="0.01" className={fieldClass('withdrawal_daily_amount_limit')} aria-invalid={fieldUnsaved('withdrawal_daily_amount_limit')} title={fieldUnsaved('withdrawal_daily_amount_limit') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_daily_amount_limit')} onChange={(event) => setValue('withdrawal_daily_amount_limit', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-lock">{t('payment.withdrawAccountLockDays')}</Label>
                        <Input id="wd-lock" type="number" min="0" step="1" className={fieldClass('withdrawal_account_lock_days')} aria-invalid={fieldUnsaved('withdrawal_account_lock_days')} title={fieldUnsaved('withdrawal_account_lock_days') ? t('payment.unsavedMark') : undefined} value={valueOf('withdrawal_account_lock_days')} onChange={(event) => setValue('withdrawal_account_lock_days', event.target.value)} />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('payment.withdrawHint')}</p>
            </div>

            <Button type="button" className="w-full rounded-xl" onClick={() => void saveAll()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                {t('payment.save')}
            </Button>
        </div>
    );
}
