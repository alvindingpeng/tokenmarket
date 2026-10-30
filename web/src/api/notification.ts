import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

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
