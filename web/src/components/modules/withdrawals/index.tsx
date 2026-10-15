import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Landmark, Loader2, RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    markWithdrawalPaid,
    reviewWithdrawal,
    rollbackWithdrawal,
    useWithdrawals,
    type Withdrawal,
} from '@/api/withdrawal';

// money 金额千分位展示, 余额口径截到 4 位。
function money(value: number): string {
    return Number(value || 0).toLocaleString(undefined, { maximumFractionDigits: 4 });
}

// formatTime 后端 RFC3339 转本地时间。
function formatTime(value: string | null, fallback: string): string {
    if (!value) return fallback;
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return fallback;
    return date.toLocaleString();
}

// STATUSES 状态筛选项; 空串表示全部。
const STATUSES = ['', 'pending', 'approved', 'paid', 'rejected', 'cancelled'];

// Withdrawals 提现管理: 审核队列 + 打款登记; v1 全部人工(D3), 打款失败可回滚。
export default function Withdrawals() {
    const t = useTranslations('withdrawals');
    const [status, setStatus] = useState('pending');
    const [notes, setNotes] = useState<Record<number, string>>({});
    const [payingId, setPayingId] = useState(0);
    const [paidRef, setPaidRef] = useState('');
    const list = useWithdrawals(status, 0);
    const items = list.data?.items ?? [];

    // handleReview 审核: 通过进 approved, 拒绝解冻回可提。
    const handleReview = (order: Withdrawal, approve: boolean) => {
        reviewWithdrawal(order.id, approve, (notes[order.id] ?? '').trim())
            .then(() => {
                toast.success(approve ? t('review.approved', { id: order.id }) : t('review.rejected', { id: order.id }));
                void list.refetch();
            })
            .catch((error) => toast.error(t('actionFailed'), { description: (error as Error).message }));
    };

    // handlePay 打款登记: 凭证号必填, 标记后资金出池。
    const handlePay = (order: Withdrawal) => {
        markWithdrawalPaid(order.id, paidRef.trim())
            .then(() => {
                toast.success(t('paid.done', { id: order.id }));
                setPayingId(0);
                setPaidRef('');
                void list.refetch();
            })
            .catch((error) => toast.error(t('actionFailed'), { description: (error as Error).message }));
    };

    // handleRollback 打款回滚: 已提转回冻结。
    const handleRollback = (order: Withdrawal) => {
        rollbackWithdrawal(order.id)
            .then(() => {
                toast.success(t('rollback.done', { id: order.id }));
                void list.refetch();
            })
            .catch((error) => toast.error(t('actionFailed'), { description: (error as Error).message }));
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Landmark className="size-5" />
                    {t('title')}
                </h2>
                <div className="flex items-center gap-2">
                    <Select value={status || 'all'} onValueChange={(value) => setStatus(value === 'all' ? '' : value)}>
                        <SelectTrigger className="w-36 rounded-xl"><SelectValue /></SelectTrigger>
                        <SelectContent>
                            {STATUSES.map((value) => (
                                <SelectItem key={value} value={value || 'all'}>{value === '' ? t('filterAll') : t('status.' + value)}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isRefetching} className="rounded-xl">
                        <RefreshCw className={'size-4' + (list.isRefetching ? ' animate-spin' : '')} />
                        {t('refresh')}
                    </Button>
                </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {list.isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                ) : list.isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void list.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                ) : items.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[1000px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                <th className="px-4 py-3 font-medium">{t('table.id')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.user')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.amount')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.fee')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.actual')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.account')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.status')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.time')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.actions')}</th>
                            </tr></thead>
                            <tbody>
                                {items.map((order) => (
                                    <tr key={order.id} className="border-t border-border align-middle">
                                        <td className="px-4 py-3 font-mono text-xs">#{order.id}</td>
                                        <td className="px-4 py-3">
                                            <div className="font-medium">{order.username || ('#' + order.user_id)}</div>
                                            <div className="text-xs text-muted-foreground">{order.account_name} · {t('method.' + order.method)}</div>
                                        </td>
                                        <td className="px-4 py-3 tabular-nums">{money(order.amount)}</td>
                                        <td className="px-4 py-3 tabular-nums">{money(order.fee)}</td>
                                        <td className="px-4 py-3 tabular-nums font-medium">¥{money(order.actual_cents / 100)}</td>
                                        <td className="px-4 py-3 font-mono text-xs">{order.account_no}</td>
                                        <td className="px-4 py-3">{t('status.' + order.status)}</td>
                                        <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatTime(order.applied_at, '-')}</td>
                                        <td className="px-4 py-3">
                                            {order.status === 'pending' ? (
                                                <div className="flex flex-col gap-1.5">
                                                    <div className="flex gap-1.5">
                                                        <Button size="sm" className="h-8 rounded-xl" onClick={() => handleReview(order, true)}>{t('action.approve')}</Button>
                                                        <Button size="sm" variant="outline" className="h-8 rounded-xl" onClick={() => handleReview(order, false)}>{t('action.reject')}</Button>
                                                    </div>
                                                    <Input
                                                        className="h-8 w-40 rounded-xl text-xs"
                                                        placeholder={t('review.notePlaceholder')}
                                                        value={notes[order.id] ?? ''}
                                                        onChange={(event) => setNotes((prev) => ({ ...prev, [order.id]: event.target.value }))}
                                                    />
                                                </div>
                                            ) : order.status === 'approved' ? (
                                                payingId === order.id ? (
                                                    <div className="flex flex-col gap-1.5">
                                                        <Input
                                                            className="h-8 w-40 rounded-xl text-xs"
                                                            placeholder={t('paid.refPlaceholder')}
                                                            value={paidRef}
                                                            onChange={(event) => setPaidRef(event.target.value)}
                                                            autoFocus
                                                        />
                                                        <div className="flex gap-1.5">
                                                            <Button size="sm" className="h-8 rounded-xl" disabled={paidRef.trim() === ''} onClick={() => handlePay(order)}>{t('paid.confirm')}</Button>
                                                            <Button size="sm" variant="outline" className="h-8 rounded-xl" onClick={() => { setPayingId(0); setPaidRef(''); }}>{t('action.cancel')}</Button>
                                                        </div>
                                                    </div>
                                                ) : (
                                                    <Button size="sm" variant="outline" className="h-8 rounded-xl" onClick={() => { setPayingId(order.id); setPaidRef(''); }}>{t('action.markPaid')}</Button>
                                                )
                                            ) : order.status === 'paid' ? (
                                                <div className="flex flex-col gap-1.5">
                                                    <span className="font-mono text-xs text-muted-foreground">{order.paid_ref}</span>
                                                    <Button size="sm" variant="outline" className="h-8 rounded-xl" onClick={() => handleRollback(order)}>{t('action.rollback')}</Button>
                                                </div>
                                            ) : (
                                                <span className="text-xs text-muted-foreground">{order.review_note || '-'}</span>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>
        </div>
    );
}
