import request from '@/utils/request';

export interface Notification {
  id: number;
  user_id: number;
  type: string;
  title: string;
  content: string;
  link?: string;
  related_id?: number;
  status: 'unread' | 'read';
  created_at: string;
  read_at?: string;
}

export interface NotificationListResponse {
  items: Notification[];
  total: number;
  page: number;
  page_size: number;
}

export interface UnreadCountResponse {
  unread_count: number;
}

// 获取通知列表
export const getNotificationList = (params: {
  status?: 'unread' | 'read';
  type?: string;
  page?: number;
  page_size?: number;
}) => {
  return request.get<NotificationListResponse>('/api/v1/notification/list', { params });
};

// 获取未读数量
export const getUnreadCount = () => {
  return request.get<UnreadCountResponse>('/api/v1/notification/unread-count');
};

// 标记已读
export const markAsRead = (ids: number[]) => {
  return request.post('/api/v1/notification/mark-read', { ids });
};

// 全部标记已读
export const markAllAsRead = () => {
  return request.post('/api/v1/notification/mark-all-read');
};

// 删除通知
export const deleteNotification = (id: number) => {
  return request.delete(`/api/v1/notification/${id}`);
};
