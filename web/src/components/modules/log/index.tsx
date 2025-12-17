'use client';

import { useMemo } from 'react';
import { useLogList, useLogStream, type RelayLog } from '@/api/endpoints/log';
import { PageWrapper } from '@/components/common/PageWrapper';
import { LogCard } from './Card';

/**
 * 日志页面组件
 * - 初始加载20条历史日志
 * - SSE 实时推送新日志
 */
export function Log() {
    // 初始加载最新20条日志
    const { data: initialLogs } = useLogList({ page: 1, page_size: 20 });

    // SSE 实时日志流
    const { logs: streamLogs } = useLogStream(500);

    // 合并历史日志和实时日志，按时间倒序
    const allLogs = useMemo(() => {
        const logsMap = new Map<number, RelayLog>();

        // 先添加历史日志
        initialLogs?.forEach(log => logsMap.set(log.id, log));

        // 添加实时日志（会覆盖重复的）
        streamLogs.forEach(log => logsMap.set(log.id, log));

        // 转为数组并按时间倒序
        return Array.from(logsMap.values()).sort((a, b) => b.time - a.time);
    }, [initialLogs, streamLogs]);

    return (
        <PageWrapper className="grid grid-cols-1 gap-4">
            {allLogs.map((log) => (
                <LogCard key={`log-${log.id}`} log={log} />
            ))}
        </PageWrapper>
    );
}
