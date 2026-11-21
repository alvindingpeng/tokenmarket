import { useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { CircleAlert, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { useChannelStats } from '@/api/channel';
import { useUpstreamBills, useImportUpstreamBills, useExplainUpstreamBill, useRecomputeUpstreamBills, type UpstreamBillRow } from '@/api/upstream-bill';

const fmt = new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 });

// 差异徽标配色: mismatch 红, matched 绿, explained 灰蓝, pending 黄。
const STATUS_CLASS: Record<UpstreamBillRow['status'], string> = {
    pending: 'border-amber-500/30 bg-amber-500/10 text-amber-600',
    matched: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600',
    mismatch: 'border-red-500/30 bg-red-500/10 text-red-600',
    explained: 'border-sky-500/30 bg-sky-500/10 text-sky-600',
};

// 上游对单页签: 渠道商把供应商账单(CSV: model,currency,cost)导进来, 与平台计费逐模型核对。
export function UpstreamBillsSection() {
    const t = useTranslations('ops.upstream');
    // 失败/加载这两个词复用已有的 ops.loadFailed 之外的通用键, 不新造:
    // 「重试」在 common 里, 「加载中」在 ops 顶层 —— 两处都是本页已有的文案,
    // 为它们各在 ops.upstream 下再开一份, 只会让同一个词在不同页签说法不一。
    const tOps = useTranslations('ops');
    const tCommon = useTranslations('common');
    const channels = useChannelStats();
    const [channelID, setChannelID] = useState(0);
    const [period, setPeriod] = useState(() => new Date().toISOString().slice(0, 7));
    const bills = useUpstreamBills(channelID, period.replaceAll('-', ''));
    const importBills = useImportUpstreamBills();
    const explain = useExplainUpstreamBill();
    const recompute = useRecomputeUpstreamBills();
    const fileRef = useRef<HTMLInputElement>(null);

    const onImport = () => {
        const file = fileRef.current?.files?.[0];
        if (!channelID || !file) return;
        importBills.mutate({ channel_id: channelID, period, file }, {
            onSuccess: (result) => toast.success(t('imported', { count: result.imported })),
            onError: (error) => toast.error(error.message),
        });
    };

    return (
        <div className='space-y-3'>
            <div className='flex flex-wrap items-center gap-2 text-sm'>
            <select className='h-8 rounded-xl border border-border bg-background px-2 text-xs' value={channelID} onChange={(e) => setChannelID(Number(e.target.value))}>
                <option value={0}>{t('pickChannel')}</option>
                {(channels.data ?? []).map((channel) => (
                    <option key={channel.channel_id} value={channel.channel_id}>{channel.channel_name}</option>
                ))}
            </select>
            <Input type='month' className='h-8 w-36 text-xs' value={period} onChange={(e) => setPeriod(e.target.value)} />
            <input ref={fileRef} type='file' accept='.csv,text/csv' className='text-xs' />
            <Button size='sm' className='h-8 rounded-xl' onClick={onImport} disabled={importBills.isPending}>{t('import')}</Button>
            <Button size='sm' variant='outline' className='h-8 rounded-xl' disabled={!channelID || recompute.isPending}
                onClick={() => recompute.mutate({ channel_id: channelID, period: period.replaceAll('-', '') }, {
                    onSuccess: (result) => toast.success(t('recomputed', { count: result.recomputed })),
                    onError: (error) => toast.error(error.message),
                })}>
                <RefreshCw className='size-3.5' /> {t('recompute')}
            </Button>
            </div>
            {/* 三态显式分开: 失败 / 还没导入(空) / 有账单。
                此前只有「有数据且非空」一个分支, 于是请求失败时什么都不渲染 —— 与"该期间还没导入过账单"
                长得一模一样。渠道商据此以为自己没导成功, 于是重导一遍, 把同一批账单又写进去一次。 */}
            {bills.isError ? (
                <div role='alert' className='flex flex-wrap items-center gap-2 rounded-xl border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive'>
                    <CircleAlert className='size-4 shrink-0' />
                    <span className='min-w-0 flex-1'>{t('loadFailed')}</span>
                    <Button size='sm' variant='outline' className='h-7 rounded-lg px-2 text-xs' onClick={() => void bills.refetch()} disabled={bills.isRefetching}>
                        {tCommon('retry')}
                    </Button>
                </div>
            ) : bills.isLoading ? (
                <p className='rounded-xl border border-border/60 px-3 py-6 text-center text-xs text-muted-foreground'>{tOps('loading')}</p>
            ) : !bills.data || bills.data.items.length === 0 ? (
                <p className='rounded-xl border border-border/60 px-3 py-6 text-center text-xs text-muted-foreground'>{t('empty')}</p>
            ) : (
            <div className='overflow-x-auto rounded-xl border border-border/60'>
                <table className='w-full text-xs'>
                <thead className='bg-muted/40 text-muted-foreground'>
                    <tr>
                    <th className='px-2 py-1.5 text-left'>{t('col.model')}</th>
                    <th className='px-2 py-1.5 text-right'>{t('col.bill')}</th>
                    <th className='px-2 py-1.5 text-right'>{t('col.platform')}</th>
                    <th className='px-2 py-1.5 text-right'>{t('col.diff')}</th>
                    <th className='px-2 py-1.5 text-left'>{t('col.status')}</th>
                    <th className='px-2 py-1.5' />
                    </tr>
                </thead>
                <tbody>
                    {bills.data.items.map((row) => (
                    <tr key={row.id} className='border-t border-border/40'>
                        <td className='px-2 py-1.5 font-medium'>{row.model_name}</td>
                        <td className='px-2 py-1.5 text-right tabular-nums'>{fmt.format(row.cost)} {row.currency}{row.fx_rate > 0 && <span className='ml-1 text-muted-foreground'>({fmt.format(row.cost_usd)} USD)</span>}</td>
                        <td className='px-2 py-1.5 text-right tabular-nums'>{fmt.format(row.owner_rev)} USD</td>
                        <td className={'px-2 py-1.5 text-right tabular-nums ' + (Math.abs(row.diff_ratio) > 0.01 ? 'text-red-500' : 'text-muted-foreground')}>{(row.diff_ratio * 100).toFixed(1)}%</td>
                        <td className='px-2 py-1.5'>
                            <Badge variant='outline' className={'px-1.5 py-0 text-[10px] ' + STATUS_CLASS[row.status]}>{t('status.' + row.status)}</Badge>
                            {row.note && <p className='mt-0.5 text-[10px] text-muted-foreground'>{row.note}</p>}
                        </td>
                        <td className='px-2 py-1.5 text-right'>
                            {row.status === 'mismatch' && (
                            <ExplainButton onExplain={(note) => explain.mutate({ id: row.id, note }, {
                                onSuccess: () => toast.success(t('explained')),
                                onError: (error) => toast.error(error.message),
                            })} />
                            )}
                        </td>
                    </tr>
                    ))}
                </tbody>
                </table>
            </div>
            )}
        </div>
    );
}

function ExplainButton({ onExplain }: { onExplain: (note: string) => void }) {
    const t = useTranslations('ops.upstream');
    const [open, setOpen] = useState(false);
    const [note, setNote] = useState('');
    if (!open) {
        return (
            <Button size='sm' variant='ghost' className='h-7 rounded-lg text-xs' onClick={() => setOpen(true)}>{t('explain')}</Button>
        );
    }
    return (
        <div className='flex items-center gap-1'>
            <Input className='h-7 w-40 text-xs' placeholder={t('notePlaceholder')} value={note} onChange={(e) => setNote(e.target.value)} />
            <Button size='sm' className='h-7 rounded-lg text-xs' disabled={!note.trim()} onClick={() => { onExplain(note.trim()); setOpen(false); }}>{t('save')}</Button>
        </div>
    );
}
