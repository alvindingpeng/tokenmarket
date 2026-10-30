import { useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Bell, Check, CheckCheck, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { useNotificationList, useNotificationMutations, useNotificationUnreadCount } from '@/api/notification';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

// NOTIFICATION_ICONS 每种通知类型的图标; 用图标而不是 emoji, 跟随主题色板。
const NOTIFICATION_ICONS: Record<string, typeof Bell> = {
    balance_low: Bell,
    ratelimit_hit: Check,
    channel_down: Trash2,
    channel_review: CheckCheck,
    withdrawal: Check,
    recharge: CheckCheck,
};

// NotificationBell 顶栏通知铃铛: 显示未读数, 点击展开最近通知。
// 未读数独立轮询: 列表查询只在弹层打开时拉取, 避免常驻页面持续请求。
export function NotificationBell() {
    const t = useTranslations('notification');
    const [open, setOpen] = useState(false);
    const { data: unread, refetch: refetchUnread } = useNotificationUnreadCount();
    const { data: recent } = useNotificationList({ page: 1, page_size: 5 });
    const { markRead, markAllRead } = useNotificationMutations();

    const unreadCount = unread?.unread_count ?? 0;

    // 弹层打开时刷新一次未读数, 保证点开瞬间的数字是最新的。
    useEffect(() => {
        if (!open) return;
        void refetchUnread();
    }, [open, refetchUnread]);

    const handleMarkRead = (ids: number[]) => {
        markRead.mutate(ids, {
            onError: (error: unknown) => toast.error(error instanceof Error ? error.message : t('markFailed')),
        });
    };

    const handleMarkAllRead = () => {
        markAllRead.mutate(undefined, {
            onError: (error: unknown) => toast.error(error instanceof Error ? error.message : t('markFailed')),
        });
    };

    const items = recent?.items ?? [];

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    aria-label={t('title')}
                    className="relative rounded-lg p-2 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                >
                    <Bell className="size-5" />
                    {unreadCount > 0 && (
                        <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-medium text-destructive-foreground">
                            {unreadCount > 99 ? '99+' : unreadCount}
                        </span>
                    )}
                </button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-96 p-0">
                <div className="flex items-center justify-between border-b px-4 py-3">
                    <span className="text-sm font-medium">{t('title')}</span>
                    {unreadCount > 0 && (
                        <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 text-xs"
                            onClick={handleMarkAllRead}
                            disabled={markAllRead.isPending}
                        >
                            <CheckCheck className="size-3.5" />
                            {t('markAllRead')}
                        </Button>
                    )}
                </div>

                {items.length === 0 ? (
                    <div className="px-4 py-10 text-center text-sm text-muted-foreground">{t('empty')}</div>
                ) : (
                    <div className="max-h-96 divide-y overflow-y-auto">
                        {items.map((item) => {
                            const Icon = NOTIFICATION_ICONS[item.type] ?? Bell;
                            const unread = item.status === 'unread';
                            return (
                                <button
                                    key={item.id}
                                    type="button"
                                    onClick={() => unread && handleMarkRead([item.id])}
                                    className={cn(
                                        'flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-muted',
                                        unread && 'bg-primary/5',
                                    )}
                                >
                                    <Icon className={cn('mt-0.5 size-4 shrink-0 text-muted-foreground', unread && 'text-primary')} />
                                    <div className="min-w-0 flex-1">
                                        <div className="flex items-center gap-2">
                                            <span className="truncate text-sm font-medium">{item.title}</span>
                                            {unread && <span className="size-1.5 shrink-0 rounded-full bg-primary" />}
                                        </div>
                                        <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{item.content}</p>
                                    </div>
                                </button>
                            );
                        })}
                    </div>
                )}
            </PopoverContent>
        </Popover>
    );
}
