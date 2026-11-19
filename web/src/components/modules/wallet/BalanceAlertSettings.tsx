import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, Bell, BellOff, Check, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useBalanceAlert, useBalanceAlertMutations } from '@/api/balance-alert';
import { formatAmount } from '@/lib/format';

// BalanceAlertSettings 余额预警配置卡: 用户设置阈值与开关。
export function BalanceAlertSettings() {
    const t = useTranslations('wallet.alert');
    const alert = useBalanceAlert();
    const mutations = useBalanceAlertMutations();
    const [threshold, setThreshold] = useState('');

    const current = alert.data?.threshold_cny ?? 0;
    const enabled = alert.data?.enabled ?? false;
    const lastTriggered = alert.data?.last_triggered_at;

    const isDirty = threshold !== '' && parseFloat(threshold) !== current;

    const handleSave = () => {
        const value = parseFloat(threshold);
        if (isNaN(value) || value < 0) {
            toast.error(t('invalidAmount'));
            return;
        }
        mutations.save.mutate(
            { threshold_cny: value, enabled: true },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    setThreshold('');
                },
                onError: (error) => toast.error(t('saveFailed'), { description: error.message }),
            }
        );
    };

    const handleToggle = () => {
        mutations.save.mutate(
            { threshold_cny: current, enabled: !enabled },
            {
                onSuccess: () => toast.success(enabled ? t('disabled') : t('enabled')),
                onError: (error) => toast.error(t('saveFailed'), { description: error.message }),
            }
        );
    };

    const handleDelete = () => {
        mutations.remove.mutate(undefined, {
            onSuccess: () => toast.success(t('deleted')),
            onError: (error) => toast.error(t('deleteFailed'), { description: error.message }),
        });
    };

    const saving = mutations.save.isPending || mutations.remove.isPending;

    return (
        <div className="flex shrink-0 flex-wrap items-center gap-3 rounded-3xl border border-border bg-card px-4 py-3">
            <Bell className="size-5 shrink-0 text-yellow-500" />
            <div className="min-w-0 flex-1 basis-52">
                <div className="text-sm font-medium">{t('title')}</div>
                <div className="text-xs text-muted-foreground">
                    {current > 0 ? (
                        <>
                            {t('status.set', { amount: formatAmount(current) })}
                            {enabled ? (
                                <span className="ml-1 text-green-600">{t('status.enabled')}</span>
                            ) : (
                                <span className="ml-1 text-muted-foreground">{t('status.disabled')}</span>
                            )}
                            {lastTriggered && <> · {t('lastTriggered', { time: new Date(lastTriggered).toLocaleDateString() })}</>}
                        </>
                    ) : (
                        t('status.notSet')
                    )}
                </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
                <Input
                    type="number"
                    className="h-9 w-28 rounded-xl"
                    placeholder={current > 0 ? formatAmount(current) : t('placeholder')}
                    value={threshold}
                    onChange={(e) => setThreshold(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter' && isDirty) handleSave(); }}
                    disabled={saving}
                />
                <Button
                    size="sm"
                    className="h-9 rounded-xl"
                    onClick={handleSave}
                    disabled={!isDirty || saving}
                >
                    {mutations.save.isPending && <Loader2 className="size-4 animate-spin" />}
                    <Check className="size-4" />
                    {t('button.save')}
                </Button>
                {current > 0 && (
                    <>
                        <Button
                            size="sm"
                            variant="outline"
                            className="h-9 rounded-xl"
                            onClick={handleToggle}
                            disabled={saving}
                        >
                            {enabled ? <BellOff className="size-4" /> : <Bell className="size-4" />}
                            {enabled ? t('button.disable') : t('button.enable')}
                        </Button>
                        <Button
                            size="sm"
                            variant="ghost"
                            className="h-9 rounded-xl text-destructive"
                            onClick={handleDelete}
                            disabled={saving}
                        >
                            <AlertCircle className="size-4" />
                            {t('button.delete')}
                        </Button>
                    </>
                )}
            </div>
        </div>
    );
}
