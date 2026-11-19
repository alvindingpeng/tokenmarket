import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { UserView } from './user';

// 工单状态机(open -> in_progress -> resolved -> closed -> open), 与后端 model.TicketStatus 对齐。
export type TicketStatus = 'open' | 'in_progress' | 'resolved' | 'closed';
export const TICKET_STATUSES: TicketStatus[] = ['open', 'in_progress', 'resolved', 'closed'];

// 工单优先级与分类, 与后端 model.TicketPriority/TicketCategory 对齐。
export type TicketPriority = 'low' | 'normal' | 'high' | 'urgent';
export const TICKET_PRIORITIES: TicketPriority[] = ['low', 'normal', 'high', 'urgent'];

export type TicketCategory = 'billing' | 'channel' | 'account' | 'other';
export const TICKET_CATEGORIES: TicketCategory[] = ['billing', 'channel', 'account', 'other'];

// TICKET_STATUS_TRANSITIONS 合法状态流转表(不含自环), 与后端 model.TicketStatusTransitions 同步:
// open → in_progress/closed; in_progress → open/resolved/closed; resolved → in_progress/closed; closed → open。
// 不在表里的组合后端按 409 拒绝, 前端把可选项裁到这张表内, 不给用户点必然失败的按钮。
export const TICKET_STATUS_TRANSITIONS: Record<TicketStatus, TicketStatus[]> = {
    open: ['in_progress', 'closed'],
    in_progress: ['open', 'resolved', 'closed'],
    resolved: ['in_progress', 'closed'],
    closed: ['open'],
};

/**
 * ticketStatusOptions 状态管理下拉的可选项: 当前状态 + 允许流转到的状态。
 */
export function ticketStatusOptions(current: TicketStatus): TicketStatus[] {
    return [current, ...TICKET_STATUS_TRANSITIONS[current]];
}

/**
 * TicketView 工单视图(列表行, 与后端 model.TicketView 对齐)。
 * assigned_to 为空表示未分配; message_count 只计回复(后端只数 ticket_messages 行, 不含首正文, 见 docs/TICKETS.md)。
 */
export type TicketView = {
    id: number;
    user_id: number;
    username: string;
    title: string;
    content: string;
    status: TicketStatus;
    priority: TicketPriority;
    category: string;
    assigned_to?: number;
    assigned_name?: string;
    message_count: number;
    created_at: string;
    updated_at: string;
    closed_at?: string | null;
};

/**
 * TicketMessageView 工单消息(对话记录, 与后端 model.TicketMessageView 对齐)。
 * 本期只支持文本消息: attachments 有底座之前不读不传, 界面也不做附件入口。
 */
export type TicketMessageView = {
    id: number;
    ticket_id: number;
    user_id: number;
    username: string;
    content: string;
    is_admin: boolean;
    created_at: string;
};

/**
 * TicketDetailView 工单详情(与后端 model.TicketDetailView 对齐): 工单视图 + 消息列表。
 */
export type TicketDetailView = TicketView & {
    messages: TicketMessageView[];
};

// TicketListFilter 列表筛选; 空值表示不过滤。assigned_to 仅管理端有意义。
export type TicketListFilter = {
    status?: string;
    priority?: string;
    category?: string;
    assigned_to?: number;
};

// TICKET_PAGE_SIZE 列表分页大小; 后端上限 100, 前端统一 20 一页。
export const TICKET_PAGE_SIZE = 20;

// TICKET_TITLE_MAX/TICKET_CONTENT_MAX 输入长度上限, 与后端 model.TicketTitleMaxLen/TicketContentMaxLen 一致
// (op.ticketTitle/ticketContent 按 utf8.RuneCountInString 计数)。HTML maxLength 按 UTF-16 码元计,
// 对 emoji 之类会比后端略严, 方向是保守的; 服务端校验才是权威。
export const TICKET_TITLE_MAX = 255;
export const TICKET_CONTENT_MAX = 10000;

/**
 * ticketCanClose/ticketCanReopen/ticketCanReply 前端只渲染合法操作, 口径与后端对齐:
 * 关单/重开看 TICKET_STATUS_TRANSITIONS, 回复则对 closed 一律禁止 ——
 * 后端 TicketReply 对任何角色都回 ErrTicketClosed, 必须先 reopen 才能继续说话。
 */
export function ticketCanClose(status: TicketStatus): boolean {
    return TICKET_STATUS_TRANSITIONS[status].includes('closed');
}

export function ticketCanReopen(status: TicketStatus): boolean {
    // in_progress → open 是管理端「退回待处理」, 走状态下拉;
    // 用户端「重新打开」只挂在已关闭工单上(closed → open 的重开语义)。
    return status === 'closed';
}

export function ticketCanReply(status: TicketStatus): boolean {
    return status !== 'closed';
}

/**
 * ticketOpeningContent 计算需要单独渲染的工单首正文。
 *
 * 后端 TicketCreate 不落消息行, TicketGet 的 messages 永远不含首正文(见 op.TicketCreate/TicketGet),
 * 首正文只存在于 ticket.content, 必须单独渲染; 若后端某天把首正文并进消息列表,
 * 这里要认出它, 免得正文被渲染两遍 —— 判定口径: 存在「工单主人发的、与首正文相同的」消息。
 */
export function ticketOpeningContent(
    ticket: { user_id: number; content: string },
    messages: { user_id: number; content: string; is_admin: boolean }[],
): string | null {
    const included = messages.some(
        (message) => !message.is_admin && message.user_id === ticket.user_id && message.content === ticket.content,
    );
    return included ? null : ticket.content;
}

/**
 * ticketListQuery 把筛选条件与分页参数序列化为查询串, 用户端与管理端列表共用同一口径。
 * 空值不落查询串(与 billing/audit 的筛选序列化一致), page/page_size 恒定带上。
 */
export function ticketListQuery(filter: TicketListFilter, page: number, pageSize: number): string {
    const query = new URLSearchParams({ page: String(Math.max(1, page)), page_size: String(pageSize) });
    if (filter.status) query.set('status', filter.status);
    if (filter.priority) query.set('priority', filter.priority);
    if (filter.category) query.set('category', filter.category);
    if (filter.assigned_to) query.set('assigned_to', String(filter.assigned_to));
    return query.toString();
}

/**
 * assignableAdmins 从用户列表里挑出可分配的有效管理员: 角色是 admin 且账号仍在用。
 * 停用/待审的管理员不进分配下拉 —— 分给一个登不进来的账号等于把工单丢进黑洞。
 */
export function assignableAdmins(users: UserView[]): UserView[] {
    return users.filter((user) => user.role === 'admin' && user.status === 'active');
}

// ticketListQueryOptions 工单列表查询定义(用户端或管理端按 admin 切换路径)。
export function ticketListQueryOptions(filter: TicketListFilter, page: number, pageSize: number, admin = false) {
    return queryOptions({
        queryKey: ['ticket', 'list', admin ? 'admin' : 'user', filter, page, pageSize],
        queryFn: () =>
            apiRequest<{ items: TicketView[]; total: number }>(
                (admin ? '/api/v1/admin/ticket/list?' : '/api/v1/ticket/list?') + ticketListQuery(filter, page, pageSize),
            ),
        // 列表响应 {items,total,page,page_size}; items/total 即组件要的两个量。
        placeholderData: (previous) => previous,
    });
}

/**
 * useTicketList 工单列表: 用户端看自己的, 管理端看全部并可按分配人筛选。
 */
export function useTicketList(filter: TicketListFilter, page: number, pageSize: number, admin = false) {
    return useQuery(ticketListQueryOptions(filter, page, pageSize, admin));
}

/**
 * useTicketDetail 工单详情(含消息记录)。
 */
export function useTicketDetail(id: number | null, admin = false) {
    return useQuery({
        queryKey: ['ticket', 'detail', admin ? 'admin' : 'user', id],
        // 详情响应包着 {ticket: TicketDetailView}, 这里只交出详情本体。
        queryFn: () =>
            apiRequest<{ ticket: TicketDetailView }>((admin ? '/api/v1/admin/ticket/' : '/api/v1/ticket/') + String(id)).then(
                (data) => data.ticket,
            ),
        enabled: id !== null,
    });
}

// useInvalidateTickets 写操作后统一失效工单缓存: 列表(计数/状态变了)与详情(消息多了)都要刷。
function useInvalidateTickets() {
    const queryClient = useQueryClient();
    return () => void queryClient.invalidateQueries({ queryKey: ['ticket'] });
}

/**
 * useCreateTicket 创建工单(用户端); 返回工单视图。
 */
export function useCreateTicket() {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (data: { title: string; content: string; priority?: string; category?: string }) =>
            apiRequest<{ ticket: TicketView }>('/api/v1/ticket', { method: 'POST', body: data }).then((payload) => payload.ticket),
        onSuccess: invalidate,
    });
}

/**
 * useReplyTicket 回复工单; admin 决定走管理端还是用户端路径。
 */
export function useReplyTicket(admin = false) {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (input: { id: number; content: string }) =>
            apiRequest<{ message: TicketMessageView }>((admin ? '/api/v1/admin/ticket/' : '/api/v1/ticket/') + input.id + '/reply', {
                method: 'POST',
                body: { content: input.content },
            }).then((payload) => payload.message),
        onSuccess: invalidate,
    });
}

/**
 * useCloseTicket 关闭工单(用户端)。
 */
export function useCloseTicket() {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (id: number) => apiRequest<{ id: number }>('/api/v1/ticket/' + id + '/close', { method: 'POST' }),
        onSuccess: invalidate,
    });
}

/**
 * useReopenTicket 重新打开工单(用户端)。
 */
export function useReopenTicket() {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (id: number) => apiRequest<{ id: number }>('/api/v1/ticket/' + id + '/reopen', { method: 'POST' }),
        onSuccess: invalidate,
    });
}

/**
 * useAssignTicket 管理端分配工单; assigned_to 必须是有效管理员(见 assignableAdmins)。
 */
export function useAssignTicket() {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (input: { id: number; assigned_to: number }) =>
            apiRequest<{ id: number }>('/api/v1/admin/ticket/' + input.id + '/assign', {
                method: 'POST',
                body: { assigned_to: input.assigned_to },
            }),
        onSuccess: invalidate,
    });
}

/**
 * useUpdateTicketStatus 管理端变更工单状态。
 */
export function useUpdateTicketStatus() {
    const invalidate = useInvalidateTickets();
    return useMutation({
        mutationFn: (input: { id: number; status: TicketStatus }) =>
            apiRequest<{ id: number }>('/api/v1/admin/ticket/' + input.id + '/status', {
                method: 'POST',
                body: { status: input.status },
            }),
        onSuccess: invalidate,
    });
}
