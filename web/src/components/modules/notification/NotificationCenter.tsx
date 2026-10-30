import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Bell, Check, CheckCheck, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { useNotificationList, useNotificationMutations, type NotificationType } from '@/api/notification';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

// NotificationCenter 通知中心页面: 完整列表 + 已读管理。
export function NotificationCenter() {
    const t = useTranslations('notification');
    const [status, setStatus] = useState<'all' | 'unread' | 'read'>('all');
    const [page, setPage] = useState(1);
    const pageSize = 20;

    const { data, isLoading } = useNotificationList({
        status: status === 'all' ? undefined : status,
        page,
        page_size: pageSize,
    });
    const { markRead, markAllRead, remove } = useNotificationMutations();

    const items = data?.items ?? [];
    const total = data?.total ?? 0;
    const totalPages = Math.max(1, Math.ceil(total / pageSize));

    const handleError = (error: unknown) => {
        toast.error(error instanceof Error ? error.message : t('actionFailed'));
    };

    const handleMarkRead = (ids: number[]) => {
        markRead.mutate(ids, { onError: handleError });
    };

    const handleMarkAllRead = () => {
        markAllRead.mutate(undefined, { onError: handleError });
    };

    const handleRemove = (id: number) => {
        remove.mutate(id, { onError: handleError });
    };

    const filters: { id: typeof status; label: string }[] = [
        { id: 'all', label: t('filterAll') },
        { id: 'unread', label: t('filterUnread') },
        { id: 'read', label: t('filterRead') },
    ];

    return (
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 p-4 md:p-6">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                    <Bell className="size-5 text-muted-foreground" />
                    <h1 className="text-xl font-semibold">{t('title')}</h1>
                </div>
                <Button variant="outline" size="sm" onClick={handleMarkAllRead} disabled={markAllRead.isPending}>
                    <CheckCheck className="size-4" />
                    {t('markAllRead')}
                </Button>
            </div>

            <div className="flex items-center gap-2">
                {filters.map((filter) => (
                    <Button
                        key={filter.id}
                        variant={status === filter.id ? 'default' : 'ghost'}
                        size="sm"
                        onClick={() => {
                            setStatus(filter.id);
                            setPage(1);
                        }}
                    >
                        {filter.label}
                    </Button>
                ))}
            </div>

            {isLoading ? (
                <Card>
                    <CardContent className="py-10 text-center text-sm text-muted-foreground">{t('loading')}</CardContent>
                </Card>
            ) : items.length === 0 ? (
                <Card>
                    <CardContent className="py-10 text-center text-sm text-muted-foreground">{t('empty')}</CardContent>
                </Card>
            ) : (
                <div className="flex flex-col gap-2">
                    {items.map((item) => {
                        const unread = item.status === 'unread';
                        return (
                            <Card key={item.id} className={cn(unread && 'border-primary/40 bg-primary/5')}>
                                <CardContent className="flex items-start gap-3 p-4">
                                    <div className="min-w-0 flex-1">
                                        <div className="flex flex-wrap items-center gap-2">
                                            <span className="font-medium">{item.title}</span>
                                            <Badge variant={unread ? 'default' : 'secondary'} className="text-[10px]">
                                                {t('type_' + (item.type as NotificationType).replace(/[^a-z_]/g, ''))}
                                            </Badge>
                                            {unread && <span className="size-1.5 rounded-full bg-primary" />}
                                        </div>
                                        <p className="mt-1 break-words text-sm text-muted-foreground">{item.content}</p>
                                        <span className="mt-2 block text-xs text-muted-foreground/80">
                                            {new Date(item.created_at).toLocaleString()}
                                        </span>
                                    </div>
                                    <div className="flex shrink-0 items-center gap-1">
                                        {unread && (
                                            <Button
                                                variant="ghost"
                                                size="icon"
                                                aria-label={t('markRead')}
                                                onClick={() => handleMarkRead([item.id])}
                                            >
                                                <Check className="size-4 text-primary" />
                                            </Button>
                                        )}
                                        <Button
                                            variant="ghost"
                                            size="icon"
                                            aria-label={t('delete')}
                                            onClick={() => handleRemove(item.id)}
                                        >
                                            <Trash2 className="size-4 text-destructive" />
                                        </Button>
                                    </div>
                                </CardContent>
                            </Card>
                        );
                    })}
                </div>
            )}

            {totalPages > 1 && (
                <div className="flex items-center justify-between text-sm">
                    <span className="text-muted-foreground">{t('total', { total })}</span>
                    <div className="flex items-center gap-2">
                        <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
                            {t('prev')}
                        </Button>
                        <span className="text-muted-foreground">
                            {page} / {totalPages}
                        </span>
                        <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage((p) => p + 1)}>
                            {t('next')}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}
