import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useAuth } from '@/api/user';
import {
    useChannelReviewList,
    useSubmitChannelReview,
    useApproveChannelReview,
    useRejectChannelReview,
    type ChannelReviewItem,
    type ChannelReviewStatus,
} from '@/api/channel';

// formatTime 后端 RFC3339 转本地时间。
function formatTime(value: string | null | undefined, fallback: string): string {
    if (!value) return fallback;
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return fallback;
    return date.toLocaleString();
}

// STATUSES 状态筛选项; 空串表示全部。
const STATUSES = ['', 'pending', 'draft', 'rejected', 'stale', 'approved'];

// statusTone 五个审核态的徽章配色: draft 静默, pending 琥珀, approved 绿, rejected 红, stale 橙。
const statusTone: Record<ChannelReviewStatus, string> = {
    draft: 'bg-muted text-muted-foreground',
    pending: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
    approved: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
    rejected: 'bg-destructive/15 text-destructive',
    stale: 'bg-orange-500/15 text-orange-600 dark:text-orange-400',
};

// ReviewBadge 审核状态徽章: 渠道卡片与队列共用同一配色语义; approved 也照常显示, 全量可见状态。
export function ReviewBadge({ status }: { status: ChannelReviewStatus }) {
    const t = useTranslations('channel.review');
    return (
        <span className={`inline-flex shrink-0 items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium ${statusTone[status]}`}>
            {t('status.' + status)}
        </span>
    );
}

// ChannelReviewQueue 渠道准入审核队列(P1): 管理员审批 pending 并代提交 draft/rejected/stale;
// 驳回必填原因(op 层二次校验), 提交说明可留空。列表按作用域过滤, 渠道商只看自有渠道。
// 渠道商只读: 审核功能(提交/审批)已收归管理员, 渠道商仅查看审核状态与审核意见, 无任何操作入口(后端 /submit 同步收紧)。
export function ChannelReviewQueue() {
    const t = useTranslations('channel.review');
    const { role } = useAuth();
    const isAdmin = role === 'admin';
    const [status, setStatus] = useState('');
    const [notes, setNotes] = useState<Record<number, string>>({});
    const list = useChannelReviewList(status === '' ? undefined : (status as ChannelReviewStatus));
    const submitReview = useSubmitChannelReview();
    const approveReview = useApproveChannelReview();
    const rejectReview = useRejectChannelReview();

    const items = list.data?.items ?? [];
    const noteOf = (item: ChannelReviewItem) => notes[item.channel_id] ?? '';
    const pending = submitReview.isPending || approveReview.isPending || rejectReview.isPending;

    // handleSubmit 提交审核: draft|rejected|stale → pending; 管理员免审渠道后端直接置 approved。
    const handleSubmit = (item: ChannelReviewItem) => {
        submitReview.mutate(
            { id: item.channel_id, note: noteOf(item) },
            {
                onSuccess: (result) => {
                    toast.success(t('toast.submitted', { name: item.channel_name, status: t('status.' + result.review_status) }));
                    setNotes((prev) => ({ ...prev, [item.channel_id]: '' }));
                },
                onError: (error) => toast.error(error.message),
            }
        );
    };

    // handleApprove 批准: pending → approved 并写指纹基线, 之后渠道才能发布。
    const handleApprove = (item: ChannelReviewItem) => {
        approveReview.mutate(
            { id: item.channel_id, note: noteOf(item) },
            {
                onSuccess: () => {
                    toast.success(t('toast.approved', { name: item.channel_name }));
                    setNotes((prev) => ({ ...prev, [item.channel_id]: '' }));
                },
                onError: (error) => toast.error(error.message),
            }
        );
    };

    // handleReject 驳回: 原因必填, 按钮在原因为空时禁用。
    const handleReject = (item: ChannelReviewItem) => {
        rejectReview.mutate(
            { id: item.channel_id, note: noteOf(item).trim() },
            {
                onSuccess: () => {
                    toast.success(t('toast.rejected', { name: item.channel_name }));
                    setNotes((prev) => ({ ...prev, [item.channel_id]: '' }));
                },
                onError: (error) => toast.error(error.message),
            }
        );
    };

    return (
        <section className="flex h-full min-h-0 flex-col gap-3">
            <header className="flex shrink-0 items-center justify-between gap-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                    <ShieldCheck className="size-4 text-primary" />
                    {t('title')}
                    <span className="text-xs font-normal text-muted-foreground">{isAdmin ? t('subtitle') : t('subtitleReadonly')}</span>
                </h3>
                <Select value={status} onValueChange={(value) => setStatus(value)}>
                    <SelectTrigger className="h-8 w-32 rounded-xl text-xs">
                        <SelectValue placeholder={t('filterAll')} />
                    </SelectTrigger>
                    <SelectContent>
                        {STATUSES.map((value) => (
                            <SelectItem key={value} value={value}>
                                {value === '' ? t('filterAll') : t('status.' + value)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </header>

            {list.isLoading ? (
                <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">{t('loading')}</div>
            ) : list.isError ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 text-muted-foreground">
                    <p>{t('error')}</p>
                    <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void list.refetch()}>{t('retry')}</Button>
                </div>
            ) : items.length === 0 ? (
                <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">{t('empty')}</div>
            ) : (
                <div className="min-h-0 flex-1 overflow-x-auto rounded-2xl border border-border">
                    <table className="w-full min-w-[900px] text-sm">
                        <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                            <tr>
                                <th className="px-4 py-3 font-medium">{t('table.channel')}</th>
                                {isAdmin && <th className="px-4 py-3 font-medium">{t('table.owner')}</th>}
                                <th className="px-4 py-3 font-medium">{t('table.models')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.status')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.time')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.note')}</th>
                                {isAdmin && <th className="px-4 py-3 font-medium">{t('table.actions')}</th>}
                            </tr>
                        </thead>
                        <tbody>
                            {items.map((item) => (
                                <tr key={item.channel_id} className="border-t border-border align-middle">
                                    <td className="px-4 py-3">
                                        <div className="font-medium">{item.channel_name}</div>
                                        <div className="text-xs text-muted-foreground">
                                            #{item.channel_id}
                                            {item.shared && t('shared')}
                                        </div>
                                    </td>
                                    {isAdmin && <td className="px-4 py-3">{item.owner_name}</td>}
                                    <td className="px-4 py-3 tabular-nums">{item.model_count}</td>
                                    <td className="px-4 py-3">
                                        <ReviewBadge status={item.review_status} />
                                    </td>
                                    <td className="px-4 py-3 text-xs tabular-nums text-muted-foreground">
                                        <div>{t('table.submittedAt')}: {formatTime(item.submitted_at, '-')}</div>
                                        {item.reviewed_at && (
                                            <div>
                                                {t('table.reviewedAt')}: {formatTime(item.reviewed_at, '-')}
                                                {item.reviewer_name && ` · ${item.reviewer_name}`}
                                            </div>
                                        )}
                                    </td>
                                    <td className="max-w-48 px-4 py-3 text-xs text-muted-foreground">
                                        <span className="block truncate" title={item.review_note}>{item.review_note || '-'}</span>
                                    </td>
                                    {/* 渠道商只读: 审核操作已屏蔽, 操作列整列不渲染, 仅展示状态与审核意见。 */}
                                    {isAdmin && <td className="px-4 py-3">
                                        {item.review_status === 'pending' ? (
                                            <div className="flex flex-col gap-1.5">
                                                <div className="flex gap-1.5">
                                                    <Button size="sm" className="h-8 rounded-xl" disabled={pending} onClick={() => handleApprove(item)}>
                                                        {t('action.approve')}
                                                    </Button>
                                                    <Button
                                                        size="sm"
                                                        variant="outline"
                                                        className="h-8 rounded-xl"
                                                        disabled={pending || noteOf(item).trim() === ''}
                                                        onClick={() => handleReject(item)}
                                                    >
                                                        {t('action.reject')}
                                                    </Button>
                                                </div>
                                                <Input
                                                    className="h-8 w-44 rounded-xl text-xs"
                                                    placeholder={t('notePlaceholder')}
                                                    value={noteOf(item)}
                                                    onChange={(event) => setNotes((prev) => ({ ...prev, [item.channel_id]: event.target.value }))}
                                                />
                                            </div>
                                        ) : (item.review_status === 'draft' || item.review_status === 'rejected' || item.review_status === 'stale') ? (
                                            <div className="flex flex-col gap-1.5">
                                                <Button size="sm" variant="outline" className="h-8 rounded-xl" disabled={pending} onClick={() => handleSubmit(item)}>
                                                    {t('action.submit')}
                                                </Button>
                                                <Input
                                                    className="h-8 w-44 rounded-xl text-xs"
                                                    placeholder={t('submitNotePlaceholder')}
                                                    value={noteOf(item)}
                                                    onChange={(event) => setNotes((prev) => ({ ...prev, [item.channel_id]: event.target.value }))}
                                                />
                                            </div>
                                        ) : (
                                            <span className="text-xs text-muted-foreground">-</span>
                                        )}
                                    </td>}
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
        </section>
    );
}
