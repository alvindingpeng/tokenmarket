import { create } from 'zustand';
import type { RelayLog } from '@/api/endpoints/log';

interface LogState {
    // 所有日志（已按时间倒序排列）
    logs: RelayLog[];
    // 日志ID集合（用于快速去重）
    logIds: Set<number>;
    // 是否还有更多历史日志
    hasMore: boolean;
    // 初始化日志（从API获取的历史日志）
    initializeLogs: (logs: RelayLog[], pageSize: number) => void;
    // 追加历史日志（滚动加载）
    appendLogs: (logs: RelayLog[], pageSize: number) => void;
    // 添加单条日志（SSE推送）
    addLog: (log: RelayLog) => void;
    // 清空日志
    clearLogs: () => void;
}

export const useLogStore = create<LogState>()((set) => ({
    logs: [],
    logIds: new Set(),
    hasMore: true,

    initializeLogs: (initialLogs, pageSize) =>
        set((state) => {
            // 只添加不存在的日志
            const newLogs: RelayLog[] = [];
            const newIds = new Set(state.logIds);

            for (const log of initialLogs) {
                if (!newIds.has(log.id)) {
                    newLogs.push(log);
                    newIds.add(log.id);
                }
            }

            if (newLogs.length === 0) {
                return { hasMore: initialLogs.length >= pageSize };
            }

            // 合并并按时间倒序排列
            const merged = [...state.logs, ...newLogs].sort((a, b) => b.time - a.time);
            return { logs: merged, logIds: newIds, hasMore: initialLogs.length >= pageSize };
        }),

    appendLogs: (moreLogs, pageSize) =>
        set((state) => {
            // 只添加不存在的日志
            const newLogs: RelayLog[] = [];
            const newIds = new Set(state.logIds);

            for (const log of moreLogs) {
                if (!newIds.has(log.id)) {
                    newLogs.push(log);
                    newIds.add(log.id);
                }
            }

            // 如果返回数量小于 pageSize，说明没有更多了
            const hasMore = moreLogs.length >= pageSize;

            if (newLogs.length === 0) {
                return { hasMore };
            }

            // 追加到末尾（历史日志时间更早）
            const merged = [...state.logs, ...newLogs].sort((a, b) => b.time - a.time);
            return { logs: merged, logIds: newIds, hasMore };
        }),

    addLog: (log) =>
        set((state) => {
            // 快速检查是否已存在
            if (state.logIds.has(log.id)) {
                return state;
            }

            // 新日志插入到正确位置（保持时间倒序）
            const insertIdx = state.logs.findIndex((l) => l.time < log.time);
            const newLogs =
                insertIdx === -1
                    ? [...state.logs, log]
                    : [...state.logs.slice(0, insertIdx), log, ...state.logs.slice(insertIdx)];

            const newIds = new Set(state.logIds).add(log.id);

            return { logs: newLogs, logIds: newIds };
        }),

    clearLogs: () => set({ logs: [], logIds: new Set(), hasMore: true }),
}));

