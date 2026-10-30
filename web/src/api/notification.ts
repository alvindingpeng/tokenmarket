import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { Page } from '@/stores/app';

/**
 * NotificationType 通知类型; 与后端 model.NotificationType 一一对应。
 */
export type NotificationType =
    | 'system'
    | 'balance_low'
    | 'ratelimit_hit'
    | 'channel_down'
    | 'channel_review'
    | 'withdrawal'
    | 'recharge'
    | 'dispute'
    | 'ticket';

/**
 * Notification 一条通知记录。
 */
export type Notification = {
    id: number;
    user_id: number;
    type: NotificationType;
    title: string;
    content: string;
    link?: string;
    related_id?: number;
    status: 'unread' | 'read';
    created_at: string;
    read_at?: string;
};

/**
 * NotificationNavigation 通知的站内跳转目标: 落地页面 + 可选的关联工单聚焦。
 */
export type NotificationNavigation = {
    page: Page;
    focusTicketId?: number;
};

/**
 * notificationNavigation 计算通知的站内跳转目标。
 *
 * 后端工单通知的 link 为空串、只带 related_id=工单 ID(见 op.NotifyTicket),
 * 因此跳转不能解析 link, 只能按类型落地: 工单通知 -> 工单页, 并尽量带上要打开的工单号。
 * 其余类型暂无稳定落地页, 返回 null(不渲染跳转入口), 后续类型接了页面再补映射。
 */
export function notificationNavigation(type: NotificationType, relatedId?: number): NotificationNavigation | null {
    if (type !== 'ticket') return null;
    return { page: 'tickets', focusTicketId: relatedId && relatedId > 0 ? relatedId : undefined };
}

/**
 * notificationTypeKey 通知类型徽标的文案键; 类型名先清洗掉非法字符再拼键,
 * 避免后端新增怪异类型名时 t() 因缺键直接抛错整页白屏。
 */
export function notificationTypeKey(type: string): string {
    return 'type_' + type.replace(/[^a-z_]/g, '');
}

/**
 * NotificationListResponse 通知列表分页响应。
 */
export type NotificationListResponse = {
    items: Notification[];
    total: number;
    page: number;
    page_size: number;
};

export const notificationListQueryOptions = (params: {
    status?: 'unread' | 'read';
    type?: NotificationType;
    page?: number;
    page_size?: number;
}) => ({
    queryKey: ['notification', 'list', params] as const,
    queryFn: () => {
        const qs = new URLSearchParams();
        if (params.status) qs.set('status', params.status);
        if (params.type) qs.set('type', params.type);
        if (params.page) qs.set('page', String(params.page));
        if (params.page_size) qs.set('page_size', String(params.page_size));
        const suffix = qs.toString();
        return apiRequest<NotificationListResponse>('/api/v1/notification/list' + (suffix ? '?' + suffix : ''));
    },
});

// 未读数量走独立 query key: 铃铛高频轮询, 不该因为列表失效而一起重取。
export const notificationUnreadQueryOptions = () => ({
    queryKey: ['notification', 'unread-count'] as const,
    queryFn: () => apiRequest<{ unread_count: number }>('/api/v1/notification/unread-count'),
});

export function useNotificationList(params: {
    status?: 'unread' | 'read';
    type?: NotificationType;
    page?: number;
    page_size?: number;
}) {
    return useQuery(notificationListQueryOptions(params));
}

export function useNotificationUnreadCount() {
    return useQuery(notificationUnreadQueryOptions());
}

// useNotificationMutations 返回通知的写操作; 变更后同时失效列表与未读数两个 query。
export function useNotificationMutations() {
    const queryClient = useQueryClient();

    const invalidate = () => {
        void queryClient.invalidateQueries({ queryKey: ['notification'] });
    };

    const markRead = useMutation({
        mutationFn: (ids: number[]) =>
            apiRequest<{ marked: number }>('/api/v1/notification/mark-read', {
                method: 'POST',
                body: { ids },
            }),
        onSuccess: invalidate,
    });

    const markAllRead = useMutation({
        mutationFn: () => apiRequest('/api/v1/notification/mark-all-read', { method: 'POST' }),
        onSuccess: invalidate,
    });

    const remove = useMutation({
        mutationFn: (id: number) => apiRequest('/api/v1/notification/' + id, { method: 'DELETE' }),
        onSuccess: invalidate,
    });

    return { markRead, markAllRead, remove };
}
