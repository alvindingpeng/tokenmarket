import { toast as sonnerToast } from 'sonner';
import { CircleCheck, CircleX, AlertTriangle } from 'lucide-react';

// ToastOptions 定义通知的可选显示参数。
type ToastOptions = {
    description?: string; // 通知的补充说明。
    duration?: number; // 通知的显示时长。
};

// toast 封装项目统一的通知入口。
export const toast = {
    success: (message: string, options?: ToastOptions) => {
        sonnerToast(message, {
            icon: <CircleCheck className="size-5 text-primary" />,
            position: 'top-left',
            ...options,
        });
    },
    error: (message: string, options?: ToastOptions) => {
        sonnerToast(message, {
            icon: <CircleX className="size-5 text-destructive" />,
            position: 'top-left',
            ...options,
        });
    },
    warning: (message: string, options?: ToastOptions) => {
        sonnerToast(message, {
            icon: <AlertTriangle className="size-5 text-destructive/70" />,
            position: 'top-left',
            ...options,
        });
    },
};
