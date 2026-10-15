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
    'recharge_notify_base',
    'recharge_rate',
    'recharge_min_cny',
    'recharge_amount_tiers',
    'recharge_bonus_rules',
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

            <Button type="button" className="w-full rounded-xl" onClick={() => void saveAll()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                {t('payment.save')}
            </Button>
        </div>
    );
}
