import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Loader2, Trash2, Wallet } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Progress } from '@/components/ui/progress';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { useMyBudgets, useMyBudgetMutations, type MyBudget } from '@/api/my-budget';

// MyBudgetsCard 用户自管花费预算卡: 周期上限, warn/block 两档, 实时用量进度条。
// block 由服务端在请求预扣前拦截; warn 达 warn_ratio 时发去重通知。
export function MyBudgetsCard() {
    const t = useTranslations('wallet.budget');
    const { data, isLoading } = useMyBudgets();
    const { create, update, remove } = useMyBudgetMutations();
    const [period, setPeriod] = useState('day');
    const [action, setAction] = useState('warn');
    const [limit, setLimit] = useState('');
    const [warnRatio, setWarnRatio] = useState('80');

    const submit = () => {
        const amount = Number(limit);
        const ratio = Number(warnRatio) || 80;
        if (!amount || amount <= 0) {
            toast.error(t('inputInvalid'));
            return;
        }
        create.mutate({ period, action, limit_amount: amount, warn_ratio: ratio }, {
            onSuccess: () => { toast.success(t('created')); setLimit(''); },
            onError: (error) => toast.error(t('createFailed'), { description: error.message }),
        });
    };

    const ratioLabel = (b: MyBudget) => Math.min(100, b.ratio_pct) + '%';

    return (
        <section className='rounded-3xl border border-border bg-card p-5 space-y-4'>
            <div className='flex items-center gap-2'>
                <Wallet className='size-4 text-muted-foreground' />
                <h3 className='text-sm font-semibold'>{t('title')}</h3>
            </div>
            <p className='text-xs text-muted-foreground'>{t('desc')}</p>

            <div className='flex flex-wrap items-end gap-2'>
                <label className='grid gap-1 text-xs text-muted-foreground'>
                    {t('period')}
                    <Select value={period} onValueChange={setPeriod}>
                        <SelectTrigger className='h-9 w-28 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='day'>{t('day')}</SelectItem>
                            <SelectItem value='month'>{t('month')}</SelectItem>
                        </SelectContent>
                    </Select>
                </label>
                <label className='grid gap-1 text-xs text-muted-foreground'>
                    {t('action')}
                    <Select value={action} onValueChange={setAction}>
                        <SelectTrigger className='h-9 w-28 rounded-xl'><SelectValue /></SelectTrigger>
                        <SelectContent>
                            <SelectItem value='warn'>{t('warn')}</SelectItem>
                            <SelectItem value='block'>{t('block')}</SelectItem>
                        </SelectContent>
                    </Select>
                </label>
                <label className='grid gap-1 text-xs text-muted-foreground'>
                    {t('limit')}
                    <Input value={limit} onChange={(e) => setLimit(e.target.value)} className='h-9 w-28 rounded-xl' inputMode='decimal' placeholder='10.00' />
                </label>
                <label className='grid gap-1 text-xs text-muted-foreground'>
                    {t('warnRatio')}
                    <Input value={warnRatio} onChange={(e) => setWarnRatio(e.target.value)} className='h-9 w-16 rounded-xl' inputMode='numeric' />
                </label>
                <Button size='sm' className='rounded-xl' disabled={create.isPending} onClick={submit}>
                    {create.isPending ? <Loader2 className='size-4 animate-spin' /> : t('create')}
                </Button>
            </div>

            {isLoading ? (
                <div className='flex justify-center py-4'><Loader2 className='size-4 animate-spin text-muted-foreground' /></div>
            ) : (data ?? []).length === 0 ? (
                <p className='text-xs text-muted-foreground'>{t('none')}</p>
            ) : (
                <ul className='space-y-3'>
                    {(data ?? []).map((b) => (
                        <li key={b.id} className='rounded-2xl border border-border p-3 space-y-2'>
                            <div className='flex items-center justify-between gap-2'>
                                <span className='text-sm font-medium'>
                                    {b.period === 'month' ? t('month') : t('day')} · {b.action === 'block' ? t('block') : t('warn')} · ${'$' + b.limit_amount.toFixed(2)}
                                </span>
                                <span className='flex items-center gap-2'>
                                    <Switch checked={b.enabled} onCheckedChange={(checked) => update.mutate({ id: b.id, period: b.period, action: b.action, limit_amount: b.limit_amount, warn_ratio: b.warn_ratio, enabled: checked }, {
                                        onError: (error) => toast.error(t('updateFailed'), { description: error.message }),
                                    })} />
                                    <Button size='icon' variant='ghost' className='size-7 text-destructive' disabled={remove.isPending}
                                        onClick={() => remove.mutate(b.id, {
                                            onSuccess: () => toast.success(t('deleted')),
                                            onError: (error) => toast.error(t('deleteFailed'), { description: error.message }),
                                        })}>
                                        <Trash2 className='size-3.5' />
                                    </Button>
                                </span>
                            </div>
                            <Progress value={Math.min(100, b.ratio_pct)} />
                            <div className='flex items-center justify-between text-xs text-muted-foreground'>
                                <span>{'$' + b.spent.toFixed(2)} / {'$' + b.limit_amount.toFixed(2)} ({ratioLabel(b)})</span>
                                <Badge variant='outline' className={b.enabled ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'}>
                                    {b.enabled ? t('enabled') : t('disabled')}
                                </Badge>
                            </div>
                        </li>
                    ))}
                </ul>
            )}
        </section>
    );
}
