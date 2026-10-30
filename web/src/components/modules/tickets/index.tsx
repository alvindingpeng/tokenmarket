import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronLeft, ChevronRight, Eye, Loader2, Plus, RefreshCw, Ticket } from 'lucide-react';
import { toast } from 'sonner';
import { useQuery } from '@tanstack/react-query';
import {
    assignableAdmins,
    useCreateTicket,
    useTicketList,
    TICKET_CATEGORIES,
    TICKET_CONTENT_MAX,
    TICKET_PAGE_SIZE,
    TICKET_PRIORITIES,
    TICKET_STATUSES,
    TICKET_TITLE_MAX,
    type TicketListFilter,
} from '@/api/ticket';
import { useAuth } from '@/api/user';
import { userManageListQueryOptions } from '@/api/usermanage';
import { formatDateTime } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { CategoryBadge, PriorityBadge, StatusBadge } from './badges';
import { TicketDetail } from './TicketDetail';
import { useTicketFocusStore } from './store';

// Pager 通用分页控件: 上一页/下一页 + 页码与总数(与 billing 模块同款交互)。
// 翻页按钮是纯图标, 补 aria-label 供读屏识别。
function Pager({ page, total, onChange }: { page: number; total: number; onChange: (page: number) => void }) {
    const t = useTranslations('tickets');
    const pages = Math.max(1, Math.ceil(total / TICKET_PAGE_SIZE));

    return (
        <nav aria-label={t('pagination.label')} className="flex shrink-0 items-center justify-between gap-2 px-1 text-xs text-muted-foreground">
            <span className="tabular-nums">{t('pagination.summary', { current: page, pages, count: total })}</span>
            <div className="flex items-center gap-1.5">
                <Button
                    variant="outline"
                    size="sm"
                    className="size-7 rounded-lg p-0"
                    disabled={page <= 1}
                    onClick={() => onChange(page - 1)}
                    aria-label={t('pagination.prev')}
                >
                    <ChevronLeft className="size-4" aria-hidden="true" />
                </Button>
                <Button
                    variant="outline"
                    size="sm"
                    className="size-7 rounded-lg p-0"
                    disabled={page >= pages}
                    onClick={() => onChange(page + 1)}
                    aria-label={t('pagination.next')}
                >
                    <ChevronRight className="size-4" aria-hidden="true" />
                </Button>
            </div>
        </nav>
    );
}

// CreateTicketDialogContent 创建工单表单: 标题/正文必填, 分类与优先级可选。
// 本期仅支持文本: 附件上传缺安全底座, 不做任何上传入口(提示文案明示)。
function CreateTicketDialogContent() {
    const t = useTranslations('tickets');
    const { setIsOpen } = useMorphingDialog();
    const createTicket = useCreateTicket();
    const [title, setTitle] = useState('');
    const [content, setContent] = useState('');
    const [priority, setPriority] = useState<string>('normal');
    const [category, setCategory] = useState<string>('other');

    const canSubmit = title.trim() !== '' && content.trim() !== '';

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit || createTicket.isPending) return;

        createTicket.mutate(
            { title: title.trim(), content: content.trim(), priority, category },
            {
                onSuccess: () => {
                    toast.success(t('create.toast.created'));
                    setIsOpen(false);
                },
                onError: (error) => toast.error(t('create.toast.createFailed'), { description: error.message }),
            },
        );
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <div className="space-y-2">
                        <Label htmlFor="ticket-create-title">{t('create.titleLabel')}</Label>
                        <Input
                            id="ticket-create-title"
                            value={title}
                            onChange={(event) => setTitle(event.target.value)}
                            className="rounded-xl"
                            placeholder={t('create.titlePlaceholder')}
                            maxLength={TICKET_TITLE_MAX}
                            autoComplete="off"
                        />
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="ticket-create-category">{t('create.categoryLabel')}</Label>
                            <Select value={category} onValueChange={setCategory}>
                                <SelectTrigger id="ticket-create-category" className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {TICKET_CATEGORIES.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('category.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="ticket-create-priority">{t('create.priorityLabel')}</Label>
                            <Select value={priority} onValueChange={setPriority}>
                                <SelectTrigger id="ticket-create-priority" className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {TICKET_PRIORITIES.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('priority.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="ticket-create-content">{t('create.contentLabel')}</Label>
                        <Textarea
                            id="ticket-create-content"
                            value={content}
                            onChange={(event) => setContent(event.target.value)}
                            rows={5}
                            className="rounded-xl"
                            maxLength={TICKET_CONTENT_MAX}
                            placeholder={t('create.contentPlaceholder')}
                        />
                        <p className="text-xs text-muted-foreground">{t('create.textOnlyHint')}</p>
                    </div>
                    <div className="flex justify-end gap-2 pt-2">
                        <Button type="button" variant="secondary" onClick={() => setIsOpen(false)} className="h-9 rounded-xl px-4">
                            {t('create.cancel')}
                        </Button>
                        <Button type="submit" disabled={createTicket.isPending || !canSubmit} className="h-9 rounded-xl px-4">
                            {createTicket.isPending ? t('create.submitting') : t('create.submit')}
                        </Button>
                    </div>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}

// CreateTicketButton 工单列表的创建入口(用户端; 管理端走分配/处理, 不代建单)。
function CreateTicketButton() {
    const t = useTranslations('tickets');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90">
                <Plus className="size-4" aria-hidden="true" />
                {t('create.button')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <CreateTicketDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// Tickets 工单模块入口: 列表(筛选 + 分页)与详情(对话)同层切换, 沿用 store 路由, 不引 react-router。
// 管理员看全部工单并可分配/改状态; 其他角色看自己的工单并可创建/回复/关闭/重开。
export function Tickets() {
    const t = useTranslations('tickets');
    const { role } = useAuth();
    const admin = role === 'admin';
    const [selectedId, setSelectedId] = useState<number | null>(null);
    const [status, setStatus] = useState('');
    const [priority, setPriority] = useState('');
    const [category, setCategory] = useState('');
    const [assignedTo, setAssignedTo] = useState(0);
    const [page, setPage] = useState(1);
    // 通知跳转带来的聚焦请求: 优先于手动选择直接开详情; 离开详情时清掉, 下一次跳转才会再触发。
    // 不走 effect 消费 —— 渲染期直接派生, 状态调整都在事件里, 避免 effect 里 setState。
    const focusId = useTicketFocusStore((state) => state.focusId);
    const clearFocus = useTicketFocusStore((state) => state.clearFocus);
    const openId = focusId ?? selectedId;

    // 分配人筛选下拉复用用户列表接口, 只留有效管理员; 仅管理端请求。
    const users = useQuery({ ...userManageListQueryOptions, enabled: admin });
    const filter: TicketListFilter = { status, priority, category, assigned_to: admin ? assignedTo : 0 };
    const list = useTicketList(filter, page, TICKET_PAGE_SIZE, admin);
    const items = list.data?.items ?? [];

    if (openId !== null) {
        return (
            <TicketDetail
                ticketId={openId}
                admin={admin}
                onBack={() => {
                    clearFocus();
                    setSelectedId(null);
                }}
            />
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Ticket className="size-5" aria-hidden="true" />
                    {t('title')}
                </h2>
                <div className="flex flex-wrap items-center gap-2">
                    {!admin && <CreateTicketButton />}
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

            {/* 筛选行: 状态/优先级/分类全角色可用, 分配人仅管理端; 窄屏自动换行。 */}
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Select value={status || 'all'} onValueChange={(value) => { setStatus(value === 'all' ? '' : value); setPage(1); }}>
                    <SelectTrigger className="w-32 rounded-xl" aria-label={t('filter.status')}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('filterAll')}</SelectItem>
                        {TICKET_STATUSES.map((option) => (
                            <SelectItem key={option} value={option}>{t('status.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={priority || 'all'} onValueChange={(value) => { setPriority(value === 'all' ? '' : value); setPage(1); }}>
                    <SelectTrigger className="w-32 rounded-xl" aria-label={t('filter.priority')}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('filterAll')}</SelectItem>
                        {TICKET_PRIORITIES.map((option) => (
                            <SelectItem key={option} value={option}>{t('priority.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={category || 'all'} onValueChange={(value) => { setCategory(value === 'all' ? '' : value); setPage(1); }}>
                    <SelectTrigger className="w-32 rounded-xl" aria-label={t('filter.category')}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('filterAll')}</SelectItem>
                        {TICKET_CATEGORIES.map((option) => (
                            <SelectItem key={option} value={option}>{t('category.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                {admin && (
                    <Select value={assignedTo ? String(assignedTo) : 'all'} onValueChange={(value) => { setAssignedTo(value === 'all' ? 0 : Number(value)); setPage(1); }}>
                        <SelectTrigger className="w-36 rounded-xl" aria-label={t('filter.assignee')}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t('filter.assigneeAll')}</SelectItem>
                            {assignableAdmins(users.data ?? []).map((adminUser) => (
                                <SelectItem key={adminUser.id} value={String(adminUser.id)}>{adminUser.username}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            </div>

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
                        <table className="w-full min-w-[860px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
                                <tr>
                                    <th className="px-4 py-3 font-medium">{t('table.id')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.title')}</th>
                                    {admin && <th className="px-4 py-3 font-medium">{t('table.user')}</th>}
                                    <th className="px-4 py-3 font-medium">{t('table.status')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.priority')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.category')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.messages')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.updated')}</th>
                                    <th className="px-4 py-3 font-medium">{t('table.actions')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {items.map((item) => (
                                    <tr key={item.id} className="border-t border-border align-middle">
                                        <td className="px-4 py-3 font-mono text-xs">#{item.id}</td>
                                        <td className="max-w-[22rem] px-4 py-3">
                                            <div className="truncate font-medium" title={item.title}>{item.title}</div>
                                            {admin && item.assigned_name && (
                                                <div className="text-xs text-muted-foreground">
                                                    {t('detail.assignee')}: {item.assigned_name}
                                                </div>
                                            )}
                                        </td>
                                        {admin && (
                                            <td className="px-4 py-3">
                                                <div className="truncate">{item.username || '#' + item.user_id}</div>
                                            </td>
                                        )}
                                        <td className="px-4 py-3"><StatusBadge status={item.status} /></td>
                                        <td className="px-4 py-3"><PriorityBadge priority={item.priority} /></td>
                                        <td className="px-4 py-3"><CategoryBadge category={item.category} /></td>
                                        <td className="px-4 py-3 tabular-nums">{item.message_count}</td>
                                        <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatDateTime(item.updated_at, '-')}</td>
                                        <td className="px-4 py-3">
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                className="h-8 rounded-xl"
                                                onClick={() => setSelectedId(item.id)}
                                                aria-label={t('table.view') + ' #' + item.id}
                                            >
                                                <Eye className="size-4" aria-hidden="true" />
                                                {t('table.view')}
                                            </Button>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            <Pager page={page} total={list.data?.total ?? 0} onChange={setPage} />
        </div>
    );
}
