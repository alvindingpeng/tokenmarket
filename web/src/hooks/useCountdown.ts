import { useCallback, useEffect, useRef, useState } from 'react';

// useCountdown 提供秒级倒计时: start 重新开始, 卸载时自动清掉挂起的定时器。
// 用递归 setTimeout 而不是 setInterval: 每次只排一个定时器, 清理路径唯一, 不会漏掉。
export function useCountdown() {
    const [remaining, setRemaining] = useState(0);
    const timer = useRef<number | null>(null);

    const stop = useCallback(() => {
        if (timer.current !== null) {
            window.clearTimeout(timer.current);
            timer.current = null;
        }
    }, []);

    // 卸载时停表: 避免组件消失后仍每秒 setState。
    useEffect(() => stop, [stop]);

    const start = useCallback((seconds: number) => {
        stop();
        const schedule = (left: number) => {
            if (left <= 0) {
                timer.current = null;
                return;
            }
            timer.current = window.setTimeout(() => {
                const next = left - 1;
                setRemaining(next);
                schedule(next);
            }, 1000);
        };
        setRemaining(seconds);
        schedule(seconds);
    }, [stop]);

    return { remaining, start };
}
