import React, { useEffect, useState } from 'react';
import { Bell } from 'lucide-react';
import { getUnreadCount, getNotificationList, markAsRead, Notification } from '@/api/notification';
import { useNavigate } from 'react-router-dom';
import { formatDistanceToNow } from 'date-fns';
import { zhCN } from 'date-fns/locale';

const NotificationBell: React.FC = () => {
  const [unreadCount, setUnreadCount] = useState(0);
  const [showDropdown, setShowDropdown] = useState(false);
  const [recentNotifications, setRecentNotifications] = useState<Notification[]>([]);
  const navigate = useNavigate();

  const fetchUnreadCount = async () => {
    try {
      const res = await getUnreadCount();
      setUnreadCount(res.unread_count || 0);
    } catch (error) {
      console.error('Failed to fetch unread count:', error);
    }
  };

  const fetchRecentNotifications = async () => {
    try {
      const res = await getNotificationList({ page: 1, page_size: 5 });
      setRecentNotifications(res.items || []);
    } catch (error) {
      console.error('Failed to fetch notifications:', error);
    }
  };

  useEffect(() => {
    fetchUnreadCount();
    // 每 30 秒轮询一次未读数
    const interval = setInterval(fetchUnreadCount, 30000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    if (showDropdown) {
      fetchRecentNotifications();
    }
  }, [showDropdown]);

  const handleMarkAsRead = async (id: number, e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await markAsRead([id]);
      fetchUnreadCount();
      fetchRecentNotifications();
    } catch (error) {
      console.error('Failed to mark as read:', error);
    }
  };

  const handleViewAll = () => {
    setShowDropdown(false);
    navigate('/notifications');
  };

  const getNotificationIcon = (type: string) => {
    switch (type) {
      case 'balance_low':
        return '💰';
      case 'ratelimit_hit':
        return '⚠️';
      case 'channel_down':
        return '🔴';
      case 'channel_review':
        return '✅';
      case 'withdrawal':
        return '💵';
      case 'recharge':
        return '💳';
      default:
        return '🔔';
    }
  };

  return (
    <div className="relative">
      <button
        onClick={() => setShowDropdown(!showDropdown)}
        className="relative p-2 hover:bg-gray-100 rounded-full transition-colors"
      >
        <Bell className="w-5 h-5 text-gray-600" />
        {unreadCount > 0 && (
          <span className="absolute top-1 right-1 bg-red-500 text-white text-xs rounded-full w-5 h-5 flex items-center justify-center">
            {unreadCount > 9 ? '9+' : unreadCount}
          </span>
        )}
      </button>

      {showDropdown && (
        <>
          <div
            className="fixed inset-0 z-10"
            onClick={() => setShowDropdown(false)}
          ></div>
          <div className="absolute right-0 mt-2 w-96 bg-white rounded-lg shadow-xl border z-20 max-h-[500px] overflow-y-auto">
            <div className="p-4 border-b flex items-center justify-between">
              <h3 className="font-semibold text-gray-900">通知</h3>
              {unreadCount > 0 && (
                <span className="text-xs text-gray-500">{unreadCount} 条未读</span>
              )}
            </div>

            {recentNotifications.length === 0 ? (
              <div className="p-8 text-center text-gray-500">暂无通知</div>
            ) : (
              <div className="divide-y">
                {recentNotifications.map((notif) => (
                  <div
                    key={notif.id}
                    className={`p-4 hover:bg-gray-50 cursor-pointer ${
                      notif.status === 'unread' ? 'bg-blue-50' : ''
                    }`}
                    onClick={() => {
                      if (notif.link) {
                        navigate(notif.link);
                        setShowDropdown(false);
                      }
                    }}
                  >
                    <div className="flex items-start gap-3">
                      <span className="text-xl flex-shrink-0">
                        {getNotificationIcon(notif.type)}
                      </span>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 mb-1">
                          <p className="text-sm font-medium text-gray-900 truncate">
                            {notif.title}
                          </p>
                          {notif.status === 'unread' && (
                            <span className="w-2 h-2 bg-blue-500 rounded-full flex-shrink-0"></span>
                          )}
                        </div>
                        <p className="text-xs text-gray-600 line-clamp-2 mb-1">
                          {notif.content}
                        </p>
                        <div className="flex items-center justify-between">
                          <span className="text-xs text-gray-400">
                            {formatDistanceToNow(new Date(notif.created_at), {
                              addSuffix: true,
                              locale: zhCN,
                            })}
                          </span>
                          {notif.status === 'unread' && (
                            <button
                              onClick={(e) => handleMarkAsRead(notif.id, e)}
                              className="text-xs text-blue-600 hover:text-blue-700"
                            >
                              标记已读
                            </button>
                          )}
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}

            <div className="p-3 border-t bg-gray-50">
              <button
                onClick={handleViewAll}
                className="w-full text-center text-sm text-blue-600 hover:text-blue-700 font-medium"
              >
                查看全部通知
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
};

export default NotificationBell;
