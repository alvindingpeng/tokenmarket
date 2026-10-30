import React, { useEffect, useState } from 'react';
import { Bell, Check, Trash2, CheckCheck } from 'lucide-react';
import {
  getNotificationList,
  markAsRead,
  markAllAsRead,
  deleteNotification,
  Notification,
} from '@/api/notification';
import { formatDistanceToNow } from 'date-fns';
import { zhCN } from 'date-fns/locale';

const NotificationCenter: React.FC = () => {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState<'all' | 'unread'>('all');

  const fetchNotifications = async () => {
    setLoading(true);
    try {
      const res = await getNotificationList({
        status: filter === 'unread' ? 'unread' : undefined,
        page,
        page_size: 20,
      });
      setNotifications(res.items || []);
      setTotal(res.total || 0);
    } catch (error) {
      console.error('Failed to fetch notifications:', error);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchNotifications();
  }, [page, filter]);

  const handleMarkAsRead = async (ids: number[]) => {
    try {
      await markAsRead(ids);
      fetchNotifications();
    } catch (error) {
      console.error('Failed to mark as read:', error);
    }
  };

  const handleMarkAllAsRead = async () => {
    try {
      await markAllAsRead();
      fetchNotifications();
    } catch (error) {
      console.error('Failed to mark all as read:', error);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await deleteNotification(id);
      fetchNotifications();
    } catch (error) {
      console.error('Failed to delete notification:', error);
    }
  };

  const getNotificationIcon = (type: string) => {
    const iconClass = 'w-5 h-5';
    switch (type) {
      case 'balance_low':
        return <span className={iconClass}>💰</span>;
      case 'ratelimit_hit':
        return <span className={iconClass}>⚠️</span>;
      case 'channel_down':
        return <span className={iconClass}>🔴</span>;
      case 'channel_review':
        return <span className={iconClass}>✅</span>;
      case 'withdrawal':
        return <span className={iconClass}>💵</span>;
      case 'recharge':
        return <span className={iconClass}>💳</span>;
      default:
        return <Bell className={iconClass} />;
    }
  };

  const getNotificationTypeLabel = (type: string) => {
    const labels: Record<string, string> = {
      system: '系统通知',
      balance_low: '余额不足',
      ratelimit_hit: '限流触发',
      channel_down: '渠道故障',
      channel_review: '渠道审核',
      withdrawal: '提现通知',
      recharge: '充值成功',
      dispute: '争议处理',
      ticket: '工单回复',
    };
    return labels[type] || '通知';
  };

  return (
    <div className="max-w-5xl mx-auto p-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Bell className="w-6 h-6" />
          通知中心
        </h1>
        <button
          onClick={handleMarkAllAsRead}
          className="px-4 py-2 bg-blue-500 text-white rounded hover:bg-blue-600 flex items-center gap-2"
        >
          <CheckCheck className="w-4 h-4" />
          全部标记已读
        </button>
      </div>

      <div className="flex gap-2 mb-4">
        <button
          onClick={() => setFilter('all')}
          className={`px-4 py-2 rounded ${
            filter === 'all' ? 'bg-blue-500 text-white' : 'bg-gray-200'
          }`}
        >
          全部
        </button>
        <button
          onClick={() => setFilter('unread')}
          className={`px-4 py-2 rounded ${
            filter === 'unread' ? 'bg-blue-500 text-white' : 'bg-gray-200'
          }`}
        >
          未读
        </button>
      </div>

      {loading ? (
        <div className="text-center py-12">加载中...</div>
      ) : notifications.length === 0 ? (
        <div className="text-center py-12 text-gray-500">暂无通知</div>
      ) : (
        <div className="space-y-2">
          {notifications.map((notif) => (
            <div
              key={notif.id}
              className={`border rounded-lg p-4 ${
                notif.status === 'unread' ? 'bg-blue-50 border-blue-200' : 'bg-white'
              }`}
            >
              <div className="flex items-start gap-3">
                <div className="flex-shrink-0 mt-1">
                  {getNotificationIcon(notif.type)}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex-1">
                      <div className="flex items-center gap-2 mb-1">
                        <span className="text-xs px-2 py-0.5 bg-gray-100 rounded">
                          {getNotificationTypeLabel(notif.type)}
                        </span>
                        {notif.status === 'unread' && (
                          <span className="w-2 h-2 bg-blue-500 rounded-full"></span>
                        )}
                      </div>
                      <h3 className="font-semibold text-gray-900 mb-1">
                        {notif.title}
                      </h3>
                      <p className="text-sm text-gray-600 mb-2">{notif.content}</p>
                      <span className="text-xs text-gray-400">
                        {formatDistanceToNow(new Date(notif.created_at), {
                          addSuffix: true,
                          locale: zhCN,
                        })}
                      </span>
                    </div>
                    <div className="flex items-center gap-2">
                      {notif.status === 'unread' && (
                        <button
                          onClick={() => handleMarkAsRead([notif.id])}
                          className="p-1 hover:bg-gray-100 rounded"
                          title="标记已读"
                        >
                          <Check className="w-4 h-4 text-green-600" />
                        </button>
                      )}
                      <button
                        onClick={() => handleDelete(notif.id)}
                        className="p-1 hover:bg-gray-100 rounded"
                        title="删除"
                      >
                        <Trash2 className="w-4 h-4 text-red-600" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {total > 20 && (
        <div className="mt-6 flex justify-center gap-2">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page === 1}
            className="px-4 py-2 border rounded disabled:opacity-50"
          >
            上一页
          </button>
          <span className="px-4 py-2">
            {page} / {Math.ceil(total / 20)}
          </span>
          <button
            onClick={() => setPage((p) => p + 1)}
            disabled={page >= Math.ceil(total / 20)}
            className="px-4 py-2 border rounded disabled:opacity-50"
          >
            下一页
          </button>
        </div>
      )}
    </div>
  );
};

export default NotificationCenter;
