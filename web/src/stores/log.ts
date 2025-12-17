import { create } from 'zustand';
import type { RelayLog } from '@/api/endpoints/log';

interface LogState {
    // 实时日志缓存
    streamLogs: RelayLog[];
    // 添加实时日志
    addStreamLog: (log: RelayLog, maxLogs?: number) => void;
    // 清空实时日志
    clearStreamLogs: () => void;
}

export const useLogStore = create<LogState>()((set) => ({
    streamLogs: [],
    addStreamLog: (log, maxLogs = 500) =>
        set((state) => {
            // 检查是否已存在（避免重复）
            if (state.streamLogs.some((l) => l.id === log.id)) {
                return state;
            }
            const newLogs = [log, ...state.streamLogs];
            if (newLogs.length > maxLogs) {
                return { streamLogs: newLogs.slice(0, maxLogs) };
            }
            return { streamLogs: newLogs };
        }),
    clearStreamLogs: () => set({ streamLogs: [] }),
}));

