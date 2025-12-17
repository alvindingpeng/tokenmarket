'use client';

import { useEffect, useRef } from 'react';
import { useLogs } from '@/api/endpoints/log';
import { PageWrapper } from '@/components/common/PageWrapper';
import { LogCard } from './Card';
import { Loader2 } from 'lucide-react';

/**
 * 日志页面组件
 * - 初始加载20条历史日志
 * - SSE 实时推送新日志
 * - 滚动自动加载更多
 */
export function Log() {
    const { logs, hasMore, isLoading, isLoadingMore, loadMore } = useLogs({ pageSize: 20 });
    const loadMoreRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        const target = loadMoreRef.current;
        if (!target) return;

        const observer = new IntersectionObserver(
            (entries) => {
                if (entries[0].isIntersecting && hasMore && !isLoadingMore && !isLoading && logs.length > 0) {
                    loadMore();
                }
            },
            { rootMargin: '100px' }
        );

        observer.observe(target);
        return () => observer.disconnect();
    }, [hasMore, isLoading, isLoadingMore, loadMore, logs.length]);

    return (
        <PageWrapper className="grid grid-cols-1 gap-4">
            {logs.map((log) => (
                <LogCard key={`log-${log.id}`} log={log} />
            ))}

            <div ref={loadMoreRef} className="flex justify-center py-4">
                {isLoadingMore && (
                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                )}
                {!hasMore && logs.length > 0 && (
                    <span className="text-sm text-muted-foreground">没有更多日志了</span>
                )}
            </div>
        </PageWrapper>
    );
}
