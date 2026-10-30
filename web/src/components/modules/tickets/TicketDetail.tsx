import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronLeft, Loader2, RotateCcw, Send, Square } from 'lucide-react';
import { toast } from 'sonner';
import { useQuery } from '@tanstack/react-query';
import {
    assignableAdmins,
    ticketCanClose,
    ticketCanReply,
    ticketCanReopen,
    ticketOpeningContent,
    useAssignTicket,
    useCloseTicket,
    useReopenTicket,
    useReplyTicket,
    useTicketDetail,
    useUpdateTicketStatus,
    ticketStatusOptions,
    TICKET_CONTENT_MAX,
    type TicketStatus,
} from '@/api/ticket';
import { userManageListQueryOptions } from '@/api/usermanage';
import { formatDateTime } from '@/lib/format';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { CategoryBadge, PriorityBadge, StatusBadge } from './badges';

// TicketDetailProps 详情视图参数: admin 决定走管理端接口与侧边管理操作。
type TicketDetailProps = {
    ticketId: number;
    admin: boolean;
    onBack: () => void;
};

// TicketDetail 工单详情: 对话记录 + 回复框 + 状态/分配管理(管理端) + 关闭/重开(用户端)。
// 本期只支持文本消息: 附件上传没有安全底座, 不做入口也不显示占位。
export function TicketDetail({ ticketId, admin, onBack }: TicketDetailProps) {
    const t = useTranslations('tickets');
    const detail = useTicketDetail(ticketId, admin);
    const reply = useReplyTicket(admin);
    const closeTicket = useCloseTicket();
    const reopenTicket = useReopenTicket();
    const assignTicket = useAssignTicket();
    const updateStatus = useUpdateTicketStatus();
    // 管理端才拉用户列表(分配下拉); 普通用户没有该接口权限, 不发无谓请求。
    const users = useQuery({ ...userManageListQueryOptions, enabled: admin });
    const [draft, setDraft] = useState('');
    const bottomRef = useRef<HTMLDivElement>(null);

    const ticket = detail.data;
    const messages = ticket?.messages ?? [];
    // 新消息到达后把对话滚到底部, 用户不必手动翻。
    useEffect(() => {
        bottomRef.current?.scrollIntoView({ block: 'nearest' });
    }, [messages.length]);

    // backButton 标题行/加载态/失败态共用一份: 加载或失败的详情也必须能退回列表,
    // 否则请求卡住或报错时这一页只有一个「重试」, 用户被困死在详情里。
    const backButton = (
        <Button variant="outline" size="sm" className="rounded-xl" onClick={onBack} aria-label={t('detail.back')}>
            <ChevronLeft className="size-4" aria-hidden="true" />
            {t('detail.back')}
        </Button>
    );

    if (detail.isLoading) {
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

    if (detail.isError || !ticket) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                <p>{t('error')}</p>
                <div className="flex items-center gap-2">
                    {backButton}
                    <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void detail.refetch()}>
                        {t('retry')}
                    </Button>
                </div>
            </div>
        );
    }

    // 已关闭工单对任何人都禁止回复(后端 TicketReply 回 ErrTicketClosed, 没有角色例外), 先重开再说。
    const replyDisabled = !ticketCanReply(ticket.status);
    // 首正文不在消息列表里(后端建单不落消息行), 需要单独渲染在对话开头。
    const opening = ticketOpeningContent(ticket, messages);

    const handleReply = () => {
        const content = draft.trim();
        if (!content || reply.isPending) return;
        reply.mutate({ id: ticket.id, content }, {
            onSuccess: () => {
                setDraft('');
                toast.success(t('detail.toast.replied'));
            },
            onError: (error) => toast.error(t('actionFailed'), { description: error.message }),
        });
    };

    const handleClose = () => {
        closeTicket.mutate(ticket.id, {
            onSuccess: () => toast.success(t('detail.toast.closed')),
            onError: (error) => toast.error(t('actionFailed'), { description: error.message }),
        });
    };

    const handleReopen = () => {
        reopenTicket.mutate(ticket.id, {
            onSuccess: () => toast.success(t('detail.toast.reopened')),
            onError: (error) => toast.error(t('actionFailed'), { description: error.message }),
        });
    };

    const handleStatus = (status: TicketStatus) => {
        if (status === ticket.status) return;
        updateStatus.mutate({ id: ticket.id, status }, {
            onSuccess: () => toast.success(t('detail.toast.statusChanged')),
            onError: (error) => toast.error(t('actionFailed'), { description: error.message }),
        });
    };

    const handleAssign = (value: string) => {
        const assignedTo = Number(value);
        if (!Number.isInteger(assignedTo) || assignedTo <= 0) return;
        assignTicket.mutate({ id: ticket.id, assigned_to: assignedTo }, {
            onSuccess: () => toast.success(t('detail.toast.assigned')),
            onError: (error) => toast.error(t('actionFailed'), { description: error.message }),
        });
    };

    // 可分配对象 = 有效管理员(角色 admin 且启用中); 一个都没有时给出提示而不是空下拉。
    const admins = admin ? assignableAdmins(users.data ?? []) : [];

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                {backButton}
                <h2 className="min-w-0 flex-1 truncate text-lg font-bold text-card-foreground" title={ticket.title}>
                    {ticket.title}
                </h2>
                <div className="flex flex-wrap items-center gap-1.5">
                    <StatusBadge status={ticket.status} />
                    <PriorityBadge priority={ticket.priority} />
                    <CategoryBadge category={ticket.category} />
                </div>
            </div>

            {/* 元信息 + 管理操作: 移动端纵向堆叠, 宽屏侧栏式横排。 */}
            <div className="grid shrink-0 gap-3 rounded-3xl border border-border bg-card p-4 text-sm md:grid-cols-2">
                <div className="space-y-1.5 text-muted-foreground">
                    {admin && (
                        <p>
                            {t('detail.user')}: <span className="text-foreground">{ticket.username || '#' + ticket.user_id}</span>
                        </p>
                    )}
                    <p>
                        {t('detail.created')}: <span className="tabular-nums">{formatDateTime(ticket.created_at, '-')}</span>
                    </p>
                    <p>
                        {t('detail.updated')}: <span className="tabular-nums">{formatDateTime(ticket.updated_at, '-')}</span>
                    </p>
                    <p>
                        {t('detail.assignee')}:{' '}
                        <span className="text-foreground">
                            {ticket.assigned_name || (ticket.assigned_to ? '#' + ticket.assigned_to : t('detail.unassigned'))}
                        </span>
                    </p>
                    {ticket.closed_at && (
                        <p>
                            {t('detail.closedAt')}: <span className="tabular-nums">{formatDateTime(ticket.closed_at, '-')}</span>
                        </p>
                    )}
                </div>

                {admin && (
                    <div className="flex flex-wrap items-end gap-3">
                        <div className="space-y-1.5">
                            <Label htmlFor="ticket-status">{t('detail.statusLabel')}</Label>
                            <Select value={ticket.status} onValueChange={(value) => handleStatus(value as TicketStatus)}>
                                <SelectTrigger id="ticket-status" className="w-36 rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {/* 可选项裁到后端流转表内(见 TICKET_STATUS_TRANSITIONS), 不给必然 409 的选项。 */}
                                    {ticketStatusOptions(ticket.status).map((status) => (
                                        <SelectItem key={status} value={status} className="rounded-xl">
                                            {t('status.' + status)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="ticket-assign">{t('detail.assignLabel')}</Label>
                            <Select value={ticket.assigned_to ? String(ticket.assigned_to) : 'unassigned'} onValueChange={handleAssign}>
                                <SelectTrigger id="ticket-assign" className="w-40 rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    <SelectItem value="unassigned" className="rounded-xl" disabled>
                                        {t('detail.unassigned')}
                                    </SelectItem>
                                    {admins.map((adminUser) => (
                                        <SelectItem key={adminUser.id} value={String(adminUser.id)} className="rounded-xl">
                                            {adminUser.username}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        {admins.length === 0 && (
                            <p className="w-full text-xs text-muted-foreground">{t('detail.noAdmins')}</p>
                        )}
                    </div>
                )}

                {!admin && (
                    <div className="flex flex-wrap items-center gap-2 md:justify-end">
                        {/* 按钮按流转表裁剪: 只显示合法操作, 不给必然 409 的按钮。 */}
                        {ticketCanReopen(ticket.status) && (
                            <Button variant="outline" size="sm" className="rounded-xl" onClick={handleReopen} disabled={reopenTicket.isPending}>
                                <RotateCcw className="size-4" aria-hidden="true" />
                                {t('detail.actions.reopen')}
                            </Button>
                        )}
                        {ticketCanClose(ticket.status) && (
                            <Button variant="outline" size="sm" className="rounded-xl" onClick={handleClose} disabled={closeTicket.isPending}>
                                <Square className="size-4" aria-hidden="true" />
                                {t('detail.actions.close')}
                            </Button>
                        )}
                    </div>
                )}
            </div>

            {/* 对话记录: 首条是工单正文, 其后是往来回复; 管理员回复带徽标区分。 */}
            <section
                aria-label={t('detail.conversation')}
                className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain rounded-3xl border border-border bg-card p-4"
            >
                {opening !== null && (
                    <article className="max-w-[min(42rem,100%)] self-start rounded-2xl border border-border bg-muted/40 p-3">
                        <header className="mb-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                            <span className="font-medium text-foreground">{ticket.username || '#' + ticket.user_id}</span>
                            <span className="tabular-nums">{formatDateTime(ticket.created_at, '-')}</span>
                        </header>
                        <p className="whitespace-pre-wrap break-words text-sm">{opening}</p>
                    </article>
                )}
                {messages.map((message) => (
                    <article
                        key={message.id}
                        className={cn(
                            'max-w-[min(42rem,100%)] rounded-2xl border p-3',
                            message.is_admin
                                ? 'self-end border-primary/20 bg-primary/5'
                                : 'self-start border-border bg-muted/40',
                        )}
                    >
                        <header className="mb-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                            <span className="font-medium text-foreground">{message.username || '#' + message.user_id}</span>
                            {message.is_admin && (
                                <Badge variant="outline" className="bg-primary/10 px-1.5 py-0 text-[10px]">
                                    {t('detail.adminBadge')}
                                </Badge>
                            )}
                            <span className="tabular-nums">{formatDateTime(message.created_at, '-')}</span>
                        </header>
                        <p className="whitespace-pre-wrap break-words text-sm">{message.content}</p>
                    </article>
                ))}
                <div ref={bottomRef} />
            </section>

            {/* 回复区: 本期文本回复, 附件无安全底座不做入口(见提示文案)。 */}
            <div className="shrink-0 rounded-3xl border border-border bg-card p-4">
                <Label htmlFor="ticket-reply">{t('detail.replyLabel')}</Label>
                <Textarea
                    id="ticket-reply"
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    placeholder={replyDisabled ? t('detail.replyDisabledPlaceholder') : t('detail.replyPlaceholder')}
                    disabled={replyDisabled}
                    maxLength={TICKET_CONTENT_MAX}
                    rows={3}
                    className="mt-1.5 rounded-xl"
                />
                <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
                    <p className="text-xs text-muted-foreground">
                        {replyDisabled ? t('detail.closedHint') : t('detail.textOnlyHint')}
                    </p>
                    <Button
                        size="sm"
                        className="rounded-xl"
                        onClick={handleReply}
                        disabled={replyDisabled || draft.trim() === '' || reply.isPending}
                    >
                        {reply.isPending ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <Send className="size-4" aria-hidden="true" />}
                        {t('detail.replySend')}
                    </Button>
                </div>
            </div>
        </div>
    );
}
