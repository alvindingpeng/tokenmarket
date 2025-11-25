'use client';

import { useEffect } from 'react';
import { toast } from 'sonner';

export function ServiceWorkerRegister() {
    useEffect(() => {
        if (typeof window !== 'undefined' && 'serviceWorker' in navigator && process.env.NODE_ENV === 'production') {
            navigator.serviceWorker
                .register('/sw.js', { scope: '/' })
                .then((registration) => {
                    registration.update();
                    registration.addEventListener('updatefound', () => {
                        const newWorker = registration.installing;
                        if (newWorker) {
                            newWorker.addEventListener('statechange', () => {
                                if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
                                    toast.info('发现新版本', {
                                        description: '点击刷新按钮更新到最新版本',
                                        duration: Infinity,
                                        action: {
                                            label: '刷新',
                                            onClick: () => {
                                                newWorker.postMessage({ type: 'SKIP_WAITING' });
                                                window.location.reload();
                                            },
                                        },
                                    });
                                }
                            });
                        }
                    });
                })
                .catch(() => {
                });

            navigator.serviceWorker.addEventListener('controllerchange', () => {
                window.location.reload();
            });
        }
    }, []);

    return null;
}
