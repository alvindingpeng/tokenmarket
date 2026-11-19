import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useBillingRecovery, useRunBillingRecovery } from '@/api/ops';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

function time(value: string | null) {
    if (!value) return '—';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export default function BillingRecovery() {
    const t = useTranslations('ops.recovery');
    const query = useBillingRecovery();
    const run = useRunBillingRecovery();
    const statusKeys: Record<string, string> = { pending: 'pending', retry: 'retry', manual: 'manual', completed: 'completed' };
    return <section className='space-y-3'>
        <p className='text-sm text-muted-foreground'>{t('description')}</p>
        <div className='flex flex-wrap gap-2'>
            <Button variant='outline' onClick={() => query.refetch()} disabled={query.isFetching}>{t('refresh')}</Button>
            <Button onClick={() => run.mutate(undefined, { onSuccess: () => toast.success(t('ran')), onError: () => toast.error(t('runFailed')) })} disabled={run.isPending}>
                {run.isPending ? t('running') : t('run')}
            </Button>
        </div>
        {query.isError && <div role='alert' className='rounded-xl border border-destructive p-3 text-sm text-destructive'>{t('loadFailed')}</div>}
        {query.isPending ? <p role='status'>{t('loading')}</p> : !query.isError && !query.data?.length ? <p className='rounded-xl border border-border p-6 text-center text-muted-foreground'>{t('empty')}</p> : query.data && <div className='overflow-x-auto rounded-2xl border border-border'>
            <table className='w-full min-w-[850px] text-left text-sm'>
                <caption className='sr-only'>{t('caption')}</caption>
                <thead><tr className='border-b border-border'>{['request', 'reservation', 'user', 'status', 'attempts', 'next', 'error'].map(key => <th scope='col' className='px-4 py-3 font-medium' key={key}>{t(key)}</th>)}</tr></thead>
                <tbody>{query.data.map(row => <tr className='border-b border-border last:border-0' key={row.id}>
                    <td className='px-4 py-3 font-mono'>{row.request_id}</td>
                    <td className='px-4 py-3'>{row.reservation_id}</td>
                    <td className='px-4 py-3'>{row.user_id}</td>
                    <td className='px-4 py-3'><Badge variant={row.status === 'manual' ? 'destructive' : 'outline'}>{statusKeys[row.status] ? t(statusKeys[row.status]) : row.status}</Badge></td>
                    <td className='px-4 py-3'>{row.attempts}</td>
                    <td className='whitespace-nowrap px-4 py-3'>{time(row.next_attempt_at)}</td>
                    <td className='max-w-md break-words px-4 py-3'>{row.last_error || '—'}</td>
                </tr>)}</tbody>
            </table>
        </div>}
    </section>;
}
