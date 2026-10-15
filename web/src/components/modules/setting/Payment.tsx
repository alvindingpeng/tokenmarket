import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { CreditCard, Loader2, Save } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSetSetting, useSettingList } from '@/api/setting';

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

    // valueOf 未编辑时直接取已存值, 编辑后取草稿; 避免用 effect 回灌状态。
    const valueOf = (key: string) => draft[key] ?? (settings.data ?? []).find((item) => item.key === key)?.value ?? '';
    const setValue = (key: string, value: string) => setDraft((prev) => ({ ...prev, [key]: value }));

    // saveAll 逐项提交; 每一项都是独立设置键, 保存后立即生效, 不需要重启。
    const saveAll = async () => {
        setSaving(true);
        try {
            for (const key of PAYMENT_FIELDS) {
                if (draft[key] === undefined) continue;
                await setSetting.mutateAsync({ key, value: draft[key] });
            }
            await setSetting.mutateAsync({ key: 'recharge_bonus_enabled', value: draft['recharge_bonus_enabled'] ?? (valueOf('recharge_bonus_enabled') || 'false') });
            setDraft({});
            toast.success(t('payment.saved'));
        } catch (error) {
            toast.error(t('payment.saveFailed'), { description: (error as Error).message });
        } finally {
            setSaving(false);
        }
    };

    return (
        <div className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <CreditCard className="size-5" />
                {t('payment.title')}
            </h2>
            <p className="text-xs text-muted-foreground">{t('payment.desc')}</p>

            <div className="space-y-2">
                <Label htmlFor="pay-gateway">{t('payment.gatewayURL')}</Label>
                <Input id="pay-gateway" className="rounded-xl" placeholder="https://pay.example.com" value={valueOf('epay_gateway_url')} onChange={(event) => setValue('epay_gateway_url', event.target.value)} autoComplete="off" />
            </div>
            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-pid">{t('payment.pid')}</Label>
                    <Input id="pay-pid" className="rounded-xl" value={valueOf('epay_pid')} onChange={(event) => setValue('epay_pid', event.target.value)} autoComplete="off" />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-key">{t('payment.key')}</Label>
                    <Input id="pay-key" type="password" className="rounded-xl" placeholder={t('payment.keyPlaceholder')} value={valueOf('epay_key')} onChange={(event) => setValue('epay_key', event.target.value)} autoComplete="new-password" />
                </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-types">{t('payment.payTypes')}</Label>
                    <Input id="pay-types" className="rounded-xl" placeholder="alipay,wechat,qq" value={valueOf('epay_pay_types')} onChange={(event) => setValue('epay_pay_types', event.target.value)} autoComplete="off" />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-notify">{t('payment.notifyBase')}</Label>
                    <Input id="pay-notify" className="rounded-xl" placeholder="https://api.example.com" value={valueOf('recharge_notify_base')} onChange={(event) => setValue('recharge_notify_base', event.target.value)} autoComplete="off" />
                </div>
            </div>
            <p className="text-xs text-muted-foreground">{t('payment.notifyHint')}</p>

            {/* 支付宝官方直连: RSA2 密钥对, 留空不启用该渠道 */}
            <div className="space-y-3 rounded-2xl border border-border p-3">
                <p className="text-sm font-medium text-card-foreground">{t('payment.alipaySection')}</p>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="alipay-gateway">{t('payment.alipayGateway')}</Label>
                        <Input id="alipay-gateway" className="rounded-xl" placeholder="https://openapi.alipay.com/gateway.do" value={valueOf('alipay_gateway_url')} onChange={(event) => setValue('alipay_gateway_url', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="alipay-appid">{t('payment.alipayAppID')}</Label>
                        <Input id="alipay-appid" className="rounded-xl" value={valueOf('alipay_app_id')} onChange={(event) => setValue('alipay_app_id', event.target.value)} autoComplete="off" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="alipay-private">{t('payment.alipayPrivateKey')}</Label>
                        <Input id="alipay-private" type="password" className="rounded-xl" placeholder={t('payment.keyPlaceholder')} value={valueOf('alipay_private_key')} onChange={(event) => setValue('alipay_private_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="alipay-public">{t('payment.alipayPublicKey')}</Label>
                        <Input id="alipay-public" type="password" className="rounded-xl" value={valueOf('alipay_public_key')} onChange={(event) => setValue('alipay_public_key', event.target.value)} autoComplete="new-password" />
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
                        <Input id="stripe-key" type="password" className="rounded-xl" placeholder="sk_..." value={valueOf('stripe_secret_key')} onChange={(event) => setValue('stripe_secret_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="stripe-webhook">{t('payment.stripeWebhook')}</Label>
                        <Input id="stripe-webhook" type="password" className="rounded-xl" placeholder="whsec_..." value={valueOf('stripe_webhook_secret')} onChange={(event) => setValue('stripe_webhook_secret', event.target.value)} autoComplete="new-password" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="stripe-base">{t('payment.stripeAPIBase')}</Label>
                        <Input id="stripe-base" className="rounded-xl" placeholder="https://api.stripe.com" value={valueOf('stripe_api_base')} onChange={(event) => setValue('stripe_api_base', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="stripe-currency">{t('payment.stripeCurrency')}</Label>
                        <Input id="stripe-currency" className="rounded-xl" placeholder="usd" value={valueOf('stripe_currency')} onChange={(event) => setValue('stripe_currency', event.target.value)} autoComplete="off" />
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
                        <Input id="creem-key" type="password" className="rounded-xl" value={valueOf('creem_api_key')} onChange={(event) => setValue('creem_api_key', event.target.value)} autoComplete="new-password" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="creem-webhook">{t('payment.creemWebhook')}</Label>
                        <Input id="creem-webhook" type="password" className="rounded-xl" value={valueOf('creem_webhook_secret')} onChange={(event) => setValue('creem_webhook_secret', event.target.value)} autoComplete="new-password" />
                    </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="creem-base">{t('payment.creemAPIBase')}</Label>
                        <Input id="creem-base" className="rounded-xl" placeholder="https://api.creem.io" value={valueOf('creem_api_base')} onChange={(event) => setValue('creem_api_base', event.target.value)} autoComplete="off" />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="creem-products">{t('payment.creemProducts')}</Label>
                        <Input id="creem-products" className="rounded-xl" placeholder='[{"product_id":"prod_x","name":"充值","amount_cents":7000,"credits":10}]' value={valueOf('creem_products')} onChange={(event) => setValue('creem_products', event.target.value)} autoComplete="off" />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('payment.creemHint')}</p>
            </div>

            <div className="grid grid-cols-3 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="pay-rate">{t('payment.rate')}</Label>
                    <Input id="pay-rate" type="number" min="0.1" step="0.1" className="rounded-xl" value={valueOf('recharge_rate')} onChange={(event) => setValue('recharge_rate', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-min">{t('payment.minCNY')}</Label>
                    <Input id="pay-min" type="number" min="0" step="0.01" className="rounded-xl" value={valueOf('recharge_min_cny')} onChange={(event) => setValue('recharge_min_cny', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="pay-tiers">{t('payment.tiers')}</Label>
                    <Input id="pay-tiers" className="rounded-xl" placeholder="10,50,100,500" value={valueOf('recharge_amount_tiers')} onChange={(event) => setValue('recharge_amount_tiers', event.target.value)} autoComplete="off" />
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
                <Input id="pay-bonus" className="rounded-xl" placeholder="100:5,500:10" value={valueOf('recharge_bonus_rules')} onChange={(event) => setValue('recharge_bonus_rules', event.target.value)} autoComplete="off" />
                <p className="text-xs text-muted-foreground">{t('payment.bonusRulesHint')}</p>
            </div>


            <div className="space-y-3 rounded-2xl border border-border px-3 py-3">
                <div className="text-sm font-medium">{t('payment.withdrawTitle')}</div>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                        <Label htmlFor="wd-min">{t('payment.withdrawMinAmount')}</Label>
                        <Input id="wd-min" type="number" min="0" step="0.01" className="rounded-xl" value={valueOf('withdrawal_min_amount')} onChange={(event) => setValue('withdrawal_min_amount', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-fee-rate">{t('payment.withdrawFeeRate')}</Label>
                        <Input id="wd-fee-rate" type="number" min="0" max="1" step="0.001" className="rounded-xl" value={valueOf('withdrawal_fee_rate')} onChange={(event) => setValue('withdrawal_fee_rate', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-fee-min">{t('payment.withdrawFeeMin')}</Label>
                        <Input id="wd-fee-min" type="number" min="0" step="0.01" className="rounded-xl" value={valueOf('withdrawal_fee_min')} onChange={(event) => setValue('withdrawal_fee_min', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-cooldown">{t('payment.withdrawCooldownDays')}</Label>
                        <Input id="wd-cooldown" type="number" min="0" step="1" className="rounded-xl" value={valueOf('withdrawal_cooldown_days')} onChange={(event) => setValue('withdrawal_cooldown_days', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-count">{t('payment.withdrawDailyCountLimit')}</Label>
                        <Input id="wd-count" type="number" min="0" step="1" className="rounded-xl" value={valueOf('withdrawal_daily_count_limit')} onChange={(event) => setValue('withdrawal_daily_count_limit', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-amount">{t('payment.withdrawDailyAmountLimit')}</Label>
                        <Input id="wd-amount" type="number" min="0" step="0.01" className="rounded-xl" value={valueOf('withdrawal_daily_amount_limit')} onChange={(event) => setValue('withdrawal_daily_amount_limit', event.target.value)} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="wd-lock">{t('payment.withdrawAccountLockDays')}</Label>
                        <Input id="wd-lock" type="number" min="0" step="1" className="rounded-xl" value={valueOf('withdrawal_account_lock_days')} onChange={(event) => setValue('withdrawal_account_lock_days', event.target.value)} />
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
