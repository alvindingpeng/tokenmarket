import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronLeft, Loader2 } from 'lucide-react';
import { useWebhookLogs, WEBHOOK_LOG_PAGE_SIZE, type WebhookDeliveryLog } from '@/api/webhook';
import { formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { DeliveryStatusBadge, EventBadge } from './badges';
import { Pager } from './Pager';

// WebhookLogs 投递日志: 状态/HTTP 码/脱敏错误摘要/尝试次数分页列表。
// 只渲染投递元数据 —— 响应体(含 HTML)一概不读不渲染, 即使后端将来带回也不显示。
// 字段以后端对齐为准, 全部按可缺省渲染, 缺了显示占位符而不是猜值。
export function WebhookLogs({ userId, id, onBack }: { userId: number; id: number | null; onBack: () => void }) {
    const t = useTranslations('webhook');
    const [page, setPage] = useState(1);
    const logs = useWebhookLogs(userId, id, page);
    const items = logs.data?.items ?? [];

    // backButton 加载态/失败态共用: 请求卡住或报错也必须能退回列表, 否则用户被困死在日志页。
    const backButton = (
        <Button variant="outline" size="sm" className="rounded-xl" onClick={onBack} aria-label={t('logs.back')}>
            <ChevronLeft className="size-4" aria-hidden="true" />
            {t('logs.back')}
        </Button>
    );

    if (logs.isLoading) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                <div className="flex items-center gap-2">
                    <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                    {t('loading')}
                </div>
                {backButton}
            </div>
        );
    }

    if (logs.isError || !logs.data) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                <p>{t('error')}</p>
                <div className="flex items-center gap-2">
                    {backButton}
                    <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void logs.refetch()}>
                        {t('retry')}
                    </Button>
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="text-lg font-bold text-card-foreground">{t('logs.title')}</h2>
                {backButton}
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {items.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
                        <p>{t('logs.empty')}</p>
                        <p className="text-xs">{t('logs.emptyHint')}</p>
                    </div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[720px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                                <tr>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.time')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.deliveryId')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.event')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.status')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.httpCode')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.duration')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.attempts')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {items.map((item, index) => <LogRow key={item.delivery_id || index} item={item} />)}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            <Pager page={page} total={logs.data.total} pageSize={WEBHOOK_LOG_PAGE_SIZE} onChange={setPage} scope="logs.pagination" />
        </div>
    );
}

// LogRow 单条投递记录; 字段名与 model.WebhookDeliveryLogView 对齐, 缺省显示占位符, 不猜、不补。
// 响应正文(含 HTML)一概不渲染 —— 后端也永不落库响应正文, error 只是脱敏摘要。
// response_code 0 表示未获得响应(网络失败/超时), 与「响应 0 码」不是一回事, 按占位符显示。
function LogRow({ item }: { item: WebhookDeliveryLog }) {
    return (
        <tr className="border-t border-border align-middle">
            <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(item.created_at, '\u2014')}</td>
            <td className="px-4 py-3 font-mono text-xs">{item.delivery_id || '\u2014'}</td>
            <td className="px-4 py-3">{item.event ? <EventBadge event={item.event} /> : <span className="text-muted-foreground">{'\u2014'}</span>}</td>
            <td className="px-4 py-3"><DeliveryStatusBadge status={item.status} /></td>
            <td className="px-4 py-3 tabular-nums">{item.response_code ? item.response_code : '\u2014'}</td>
            <td className="px-4 py-3 tabular-nums">{item.duration_ms ?? 0} ms</td>
            <td className="max-w-[20rem] truncate px-4 py-3 text-muted-foreground" title={item.error || undefined}>{item.error || '\u2014'}</td>
            <td className="px-4 py-3 tabular-nums">{item.attempts ?? '\u2014'}</td>
        </tr>
    );
}
