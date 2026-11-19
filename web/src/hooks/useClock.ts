import { useCallback, useSyncExternalStore } from 'react';

// 共享时钟。
// 日志页每张「运行中」卡片都要按 500ms 刷新已用时长, 早期实现是每张卡各开一个 setInterval:
// 一屏十几张卡就是十几个定时器、十几轮互不同步的渲染提交。
// 这里按间隔分桶共用定时器, 并在同一个回调里批量通知所有订阅者 —— React 18+ 会把同一次
// 回调里的更新合并成一次提交, 于是「一屏卡片同时走秒」只产生一次渲染。

interface Clock {
    now: number;
    timer: number | null;
    listeners: Set<() => void>;
}

const clocks = new Map<number, Clock>();

function clockFor(intervalMs: number): Clock {
    let clock = clocks.get(intervalMs);
    if (!clock) {
        clock = { now: Date.now(), timer: null, listeners: new Set() };
        clocks.set(intervalMs, clock);
    }
    return clock;
}

function subscribe(intervalMs: number, listener: () => void): () => void {
    const clock = clockFor(intervalMs);
    clock.listeners.add(listener);
    if (clock.timer === null) {
        clock.timer = window.setInterval(() => {
            clock.now = Date.now();
            // 先写快照再通知: 所有监听器在同一次回调里读到同一个时间值, 不会出现同一帧里
            // 卡片 A 显示 1.2s、卡片 B 显示 1.7s 的抖动。
            for (const notify of clock.listeners) notify();
        }, intervalMs);
    }
    return () => {
        clock.listeners.delete(listener);
        // 最后一个订阅者离开时停表, 避免页面切换后仍有空转定时器。
        if (clock.listeners.size === 0 && clock.timer !== null) {
            window.clearInterval(clock.timer);
            clock.timer = null;
        }
    };
}

function snapshot(intervalMs: number): number {
    return clockFor(intervalMs).now;
}

/**
 * useClock 返回按 intervalMs 刷新的当前时间戳。
 * enabled 为 false 时不订阅(也就不会引起重渲染), 返回最后一次快照 —— 用于「只在请求进行中才走秒」的场景。
 */
export function useClock(intervalMs: number, enabled = true): number {
    const subscribeFn = useCallback(
        (listener: () => void) => (enabled ? subscribe(intervalMs, listener) : () => {}),
        [enabled, intervalMs],
    );
    const getSnapshot = useCallback(() => snapshot(intervalMs), [intervalMs]);
    return useSyncExternalStore(subscribeFn, getSnapshot, getSnapshot);
}
