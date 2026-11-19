import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronLeft, Loader2, RotateCcw } from 'lucide-react';
import { toast } from 'sonner';
import {
    canReplayDelivery,
    useReplayDelivery,
    useWebhookLogs,
    WEBHOOK_DELIVERY_STATUSES,
    WEBHOOK_LOG_EVENT_FILTERS,
    WEBHOOK_LOG_PAGE_SIZE,
    type WebhookDeliveryLog,
} from '@/api/webhook';
import { notificationTypeKey } from '@/api/notification';
import { formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { DeliveryStatusBadge, EventBadge } from './badges';
import { Pager } from './Pager';
import { setLogFilter, type LogView } from './logView';

// WebhookLogs 投递日志: 状态/HTTP 码/脱敏错误摘要/尝试次数分页列表 + 状态/事件筛选 + 单条重放。
// 只渲染投递元数据 —— 响应体(含 HTML)一概不读不渲染, 即使后端将来带回也不显示。
// 字段以后端对齐为准, 全部按可缺省渲染, 缺了显示占位符而不是猜值。
export function WebhookLogs({ userId, id, onBack }: { userId: number; id: number | null; onBack: () => void }) {
    const t = useTranslations('webhook');
    // 事件名文案复用通知类型的三语键(与 EventBadge 同一套词), 不另起一份翻译。
    const tn = useTranslations('notification');
    const [view, setView] = useState<LogView>({ page: 1, status: '', event: '' });
    const logs = useWebhookLogs(userId, id, view.page, { status: view.status, event: view.event });
    const items = logs.data?.items ?? [];

    // eventLabel 事件下拉的选项文案: test 是投递专有事件, 不在通知类型闭集里, 单独给一条。
    const eventLabel = (value: string) => (value === 'test' ? t('logs.filter.eventTest') : tn(notificationTypeKey(value)));

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

    if (logs.isError || !logs.data || id === null) {
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

            {/* 筛选行: 两个下拉都走 setLogFilter —— 换筛选必然回第 1 页;
                queryKey 里带着 status/event, 所以改完就会真的重拉, 不会出现「点了没反应」。 */}
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Select
                    value={view.status || 'all'}
                    onValueChange={(value) => setView((current) => setLogFilter(current, { status: value === 'all' ? '' : value }))}
                >
                    <SelectTrigger className="w-36 rounded-xl" aria-label={t('logs.filter.status')}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('logs.filter.all')}</SelectItem>
                        {WEBHOOK_DELIVERY_STATUSES.map((option) => (
                            <SelectItem key={option} value={option}>{t('delivery.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select
                    value={view.event || 'all'}
                    onValueChange={(value) => setView((current) => setLogFilter(current, { event: value === 'all' ? '' : value }))}
                >
                    <SelectTrigger className="w-40 rounded-xl" aria-label={t('logs.filter.event')}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('logs.filter.all')}</SelectItem>
                        {WEBHOOK_LOG_EVENT_FILTERS.map((option) => (
                            <SelectItem key={option} value={option}>{eventLabel(option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {items.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
                        <p>{t('logs.empty')}</p>
                        <p className="text-xs">{t('logs.emptyHint')}</p>
                    </div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[820px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                                <tr>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.time')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.deliveryId')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.event')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.status')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.httpCode')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.duration')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.attempts')}</th>
                                    <th className="px-4 py-3 font-medium">{t('logs.table.actions')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {items.map((item, index) => (
                                    <LogRow key={item.delivery_id || index} item={item} webhookId={id} />
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            <Pager
                page={view.page}
                total={logs.data.total}
                pageSize={WEBHOOK_LOG_PAGE_SIZE}
                onChange={(page) => setView((current) => ({ ...current, page }))}
                scope="logs.pagination"
            />
        </div>
    );
}

// LogRow 单条投递记录; 字段名与 model.WebhookDeliveryLogView 对齐, 缺省显示占位符, 不猜、不补。
// 响应正文(含 HTML)一概不渲染 —— 后端也永不落库响应正文, error 只是脱敏摘要。
// response_code 0 表示未获得响应(网络失败/超时), 与「响应 0 码」不是一回事, 按占位符显示。
function LogRow({ item, webhookId }: { item: WebhookDeliveryLog; webhookId: number }) {
    const t = useTranslations('webhook');
    const replay = useReplayDelivery();
    const deliveryId = item.delivery_id ?? '';
    // 服务端状态是唯一依据: 只有终点状态(failed/cancelled)可重放, 其余置灰。
    // 在飞的重放会重复投递, 已成功的不需要重放 —— 按钮 disabled, 接口那侧也还有 409 兜底。
    const replayable = canReplayDelivery(item.status);
    const disabled = !replayable || deliveryId === '';

    // handleReplay 重放一次投递。失败只给通用文案: 后端错误串可能带出内部地址/上游细节,
    // 原样展示等于把内部信息漏给终端用户(与 test/delete 的错误处理同口径)。
    const handleReplay = () => {
        if (disabled || replay.isPending) return;
        replay.mutate({ webhookId, deliveryId }, {
            onSuccess: () => toast.success(t('logs.replay.success')),
            onError: () => toast.error(t('logs.replay.failed')),
        });
    };

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
            <td className="px-4 py-3">
                {/* 置灰时用 title 说明原因: 光看一个灰按钮, 用户会以为界面坏了。 */}
                <Button
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-xl"
                    onClick={handleReplay}
                    disabled={disabled || replay.isPending}
                    title={disabled ? t('logs.replay.disabled') : undefined}
                    aria-label={(t('logs.action.replay') + ' ' + deliveryId).trim()}
                >
                    <RotateCcw className="size-3.5" aria-hidden="true" />
                    {replay.isPending ? t('logs.replay.pending') : t('logs.action.replay')}
                </Button>
            </td>
        </tr>
    );
}
