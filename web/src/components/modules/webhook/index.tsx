import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Loader2, Pencil, Plus, RefreshCw, ScrollText, Trash2, Webhook, Zap } from 'lucide-react';
import { toast } from 'sonner';
import { redactWebhookError, useDeleteWebhook, useTestWebhook, useWebhookList, WEBHOOK_PAGE_SIZE, type WebhookView } from '@/api/webhook';
import { useAuthStore } from '@/api/user';
import { formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { DeliveryStatusBadge, EnabledBadge, EventBadge } from './badges';
import { Pager } from './Pager';
import { WebhookForm } from './WebhookForm';
import { WebhookLogs } from './WebhookLogs';

// Webhooks Webhook 订阅管理(P0-5): 订阅列表 + 创建/编辑 + 删除确认 + 测试入队 + 投递日志。
// 沿用 store 页面路由, 不引 react-router; 全部角色可见(个人级订阅, 与工单同权限面)。
// 安全红线: 密钥不回显(has_secret 只答「设没设」), URL/密钥不进 console/toast,
// 投递日志不渲染任何响应体(HTML)。
export function Webhooks() {
    const t = useTranslations('webhook');
    const userId = useAuthStore((state) => state.userId);
    const [page, setPage] = useState(1);
    const [logsId, setLogsId] = useState<number | null>(null);
    const list = useWebhookList(userId, page);
    const items = list.data?.items ?? [];

    // 投递日志是同层视图(与工单详情同款切换), 不做二级路由。
    if (logsId !== null) {
        return <WebhookLogs userId={userId} id={logsId} onBack={() => setLogsId(null)} />;
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Webhook className="size-5" aria-hidden="true" />
                    {t('title')}
                </h2>
                <div className="flex flex-wrap items-center gap-2">
                    <CreateWebhookButton />
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void list.refetch()}
                        disabled={list.isRefetching}
                        className="rounded-xl"
                    >
                        <RefreshCw className={'size-4' + (list.isRefetching ? ' animate-spin' : '')} aria-hidden="true" />
                        {t('refresh')}
                    </Button>
                </div>
            </div>

            {/* 投递约定摘要(表单里有完整五条): 仅通用 JSON 签名、只收 HTTPS 公网 443、至少一次按 delivery id 去重。 */}
            <ul className="shrink-0 space-y-1 rounded-3xl border border-border bg-card px-4 py-3 text-xs text-muted-foreground">
                <li>{t('hints.jsonOnly')}</li>
                <li>{t('hints.httpsOnly')}</li>
                <li>{t('hints.atLeastOnce')}</li>
            </ul>

            {/* 列表区: 表格窄屏横向滚动, 三态(加载/失败/空)齐全。 */}
            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {list.isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                        {t('loading')}
                    </div>
                ) : list.isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <p>{t('error')}</p>
                        <Button variant="outline" size="sm" onClick={() => void list.refetch()} className="rounded-xl">
                            {t('retry')}
                        </Button>
                    </div>
                ) : items.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-muted-foreground">
                        <p>{t('empty')}</p>
                        <p className="text-xs">{t('emptyHint')}</p>
                    </div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[920px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                                <tr>
                                    <th className="px-4 py-3 font-medium">{t('table.url')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.events')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.enabled')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.retryCount')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.lastStatus')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.updated')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.actions')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {items.map((item) => (
                                    <WebhookRow key={item.id} item={item} onShowLogs={() => setLogsId(item.id)} />
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            <Pager page={page} total={list.data?.total ?? 0} pageSize={WEBHOOK_PAGE_SIZE} onChange={setPage} scope="pagination" />
        </div>
    );
}

// WebhookRow 单行订阅: 编辑(对话框) / 测试(异步入队) / 投递日志 / 删除(二次确认)。
function WebhookRow({ item, onShowLogs }: { item: WebhookView; onShowLogs: () => void }) {
    const t = useTranslations('webhook');
    const test = useTestWebhook();

    // 测试只入队: 接口回 queued 就只说「已排队」, 结果在投递日志里看 —— 绝不声称发送成功。
    const handleTest = () => {
        if (test.isPending) return;
        test.mutate({ id: item.id }, {
            onSuccess: () => toast.success(t('test.toastQueued'), { description: t('test.toastQueuedHint') }),
            onError: (error) => toast.error(t('test.toastFailed'), {
                description: redactWebhookError(error.message, [item.url]),
            }),
        });
    };

    return (
        <tr className="border-t border-border align-middle">
            <td className="max-w-[24rem] px-4 py-3">
                <div className="truncate font-mono text-xs" title={item.url}>{item.url}</div>
                {item.has_secret && <div className="mt-0.5 text-xs text-muted-foreground">{t('form.secretSet')}</div>}
            </td>
            <td className="px-4 py-3">
                <div className="flex max-w-[18rem] flex-wrap gap-1">
                    {item.events.map((event) => <EventBadge key={event} event={event} />)}
                </div>
            </td>
            <td className="px-4 py-3"><EnabledBadge enabled={item.enabled} /></td>
            <td className="px-4 py-3 tabular-nums">{item.retry_count}</td>
            <td className="px-4 py-3"><DeliveryStatusBadge status={item.last_status} /></td>
            <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(item.updated_at, '\u2014')}</td>
            <td className="px-4 py-3">
                <div className="flex items-center gap-1.5">
                    <MorphingDialog>
                        <MorphingDialogTrigger className="flex h-8 items-center gap-1.5 rounded-xl border border-border bg-background px-2.5 text-xs font-medium shadow-xs transition-colors hover:bg-accent">
                            <Pencil className="size-3.5" aria-hidden="true" />
                            {t('table.edit')}
                        </MorphingDialogTrigger>
                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-y-auto rounded-3xl bg-card px-6 py-5 text-card-foreground"
                            >
                                <EditWebhookDialogContent item={item} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-8 rounded-xl"
                        onClick={handleTest}
                        disabled={test.isPending}
                        aria-label={t('table.test') + ' #' + item.id}
                    >
                        <Zap className="size-3.5" aria-hidden="true" />
                        {test.isPending ? t('test.pending') : t('table.test')}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-8 rounded-xl"
                        onClick={onShowLogs}
                        aria-label={t('table.logs') + ' #' + item.id}
                    >
                        <ScrollText className="size-3.5" aria-hidden="true" />
                        {t('table.logs')}
                    </Button>
                    <MorphingDialog>
                        <MorphingDialogTrigger className="flex h-8 items-center gap-1.5 rounded-xl border border-border bg-background px-2.5 text-xs font-medium text-destructive shadow-xs transition-colors hover:bg-destructive/10">
                            <Trash2 className="size-3.5" aria-hidden="true" />
                            {t('table.delete')}
                        </MorphingDialogTrigger>
                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                            >
                                <DeleteConfirmContent item={item} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>
                </div>
            </td>
        </tr>
    );
}

// CreateWebhookButton 新建订阅入口(列表右上角)。
function CreateWebhookButton() {
    const t = useTranslations('webhook');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90">
                <Plus className="size-4" aria-hidden="true" />
                {t('form.createButton')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-y-auto rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <CreateWebhookDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// CreateWebhookDialogContent 创建表单的对话框绑定(关闭时机交给表单回调)。
function CreateWebhookDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    return (
        <MorphingDialogDescription>
            <WebhookForm mode="create" onSaved={() => setIsOpen(false)} onCancel={() => setIsOpen(false)} />
        </MorphingDialogDescription>
    );
}

// EditWebhookDialogContent 编辑表单的对话框绑定; initial 不含密钥, 表单也不会试图回显。
function EditWebhookDialogContent({ item }: { item: WebhookView }) {
    const { setIsOpen } = useMorphingDialog();
    return (
        <MorphingDialogDescription>
            <WebhookForm mode="edit" initial={item} onSaved={() => setIsOpen(false)} onCancel={() => setIsOpen(false)} />
        </MorphingDialogDescription>
    );
}

// DeleteConfirmContent 删除二次确认: 破坏性操作不走「点了就删」, 明确告知不可恢复。
function DeleteConfirmContent({ item }: { item: WebhookView }) {
    const t = useTranslations('webhook');
    const { setIsOpen } = useMorphingDialog();
    const remove = useDeleteWebhook();

    const handleDelete = () => {
        if (remove.isPending) return;
        remove.mutate({ id: item.id }, {
            onSuccess: () => {
                toast.success(t('delete.toastDeleted'));
                setIsOpen(false);
            },
            onError: (error) => toast.error(t('delete.toastFailed'), {
                description: redactWebhookError(error.message, [item.url]),
            }),
        });
    };

    return (
        <div className="w-screen max-w-full md:max-w-sm">
            <MorphingDialogDescription>
                <h3 className="text-lg font-bold text-card-foreground">{t('delete.confirmTitle')}</h3>
                <p className="mt-2 text-sm text-muted-foreground">{t('delete.confirmBody')}</p>
                <div className="mt-4 flex justify-end gap-2">
                    <Button variant="secondary" onClick={() => setIsOpen(false)} className="h-9 rounded-xl px-4">
                        {t('delete.cancelButton')}
                    </Button>
                    <Button variant="destructive" onClick={handleDelete} disabled={remove.isPending} className="h-9 rounded-xl px-4">
                        {remove.isPending ? t('delete.pending') : t('delete.confirmButton')}
                    </Button>
                </div>
            </MorphingDialogDescription>
        </div>
    );
}
