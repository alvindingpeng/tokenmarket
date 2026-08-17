import type { ReactNode } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'use-intl';
import Logo from '@/components/modules/logo';
import { NAV_ITEMS, useAppStore } from '@/stores/app';
import { preloadPage } from '@/lib/page-preload';
import { cn } from '@/lib/utils';

// AppShell 作为普通用户界面的稳定布局层，统一渲染导航、顶栏和页面内容。
export function AppShell({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
    const currentPage = useAppStore((state) => state.currentPage);
    const direction = useAppStore((state) => state.direction);
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const t = useTranslations('navbar');
    const activeIndex = NAV_ITEMS.findIndex((route) => route.id === currentPage); // activeIndex 表示选中项在 Dock 中的位置。

    return (
        <div className="mx-auto flex h-dvh max-w-6xl animate-in flex-col overflow-hidden px-3 fade-in duration-300 md:grid md:grid-cols-[auto_1fr] md:grid-rows-[auto_minmax(0,1fr)] md:gap-x-6 md:px-6">
            <div className="relative z-50 md:row-span-2 md:min-h-screen">
                <nav
                    aria-label="Main Navigation"
                    className={cn(
                        'fixed bottom-6 left-1/2 isolate -translate-x-1/2 flex animate-in items-center gap-1 p-3 fade-in zoom-in-95 duration-300',
                        'md:sticky md:top-30 md:left-auto md:bottom-auto md:translate-x-0 md:flex-col md:gap-3',
                        'bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl',
                    )}
                >
                    <span
                        aria-hidden="true"
                        className="pointer-events-none absolute left-3 top-3 z-0 size-10 rounded-2xl bg-sidebar-primary transition-transform duration-300 ease-out md:hidden"
                        style={{ transform: `translateX(${activeIndex * 2.75}rem)` }}
                    />
                    <span
                        aria-hidden="true"
                        className="pointer-events-none absolute left-3 top-3 z-0 hidden size-12 rounded-2xl bg-sidebar-primary transition-transform duration-300 ease-out md:block"
                        style={{ transform: `translateY(${activeIndex * 3.75}rem)` }}
                    />
                    {NAV_ITEMS.map((route) => {
                        const isActive = currentPage === route.id;

                        return (
                            <button
                                key={route.id}
                                type="button"
                                aria-label={route.label}
                                aria-current={isActive ? 'page' : undefined}
                                onMouseEnter={() => preloadPage(route.id)}
                                onFocus={() => preloadPage(route.id)}
                                onTouchStart={() => preloadPage(route.id)}
                                onClick={() => {
                                    preloadPage(route.id);
                                    setCurrentPage(route.id);
                                }}
                                className={cn(
                                    'relative z-20 flex size-10 items-center justify-center rounded-2xl p-2 transition-[color,background-color,transform] duration-150 ease-out hover:z-30 hover:scale-110 active:scale-95 md:size-12 md:p-3',
                                    isActive ? 'text-sidebar-primary-foreground' : 'text-sidebar-foreground/60 hover:bg-sidebar-accent',
                                )}
                            >
                                <span className="relative z-10">
                                    <route.icon strokeWidth={2} />
                                </span>
                            </button>
                        );
                    })}
                </nav>
            </div>

            <header className="my-6 flex flex-none items-center gap-x-2 px-2">
                <Logo size={48} />
                <div className="min-w-0 flex-1 overflow-hidden">
                    <AnimatePresence mode="wait" custom={direction}>
                        <motion.div
                            key={currentPage}
                            custom={direction}
                            variants={{
                                initial: (value: number) => ({ y: 32 * value, opacity: 0 }),
                                animate: { y: 0, opacity: 1 },
                                exit: (value: number) => ({ y: -32 * value, opacity: 0 }),
                            }}
                            initial="initial"
                            animate="animate"
                            exit="exit"
                            transition={{ duration: 0.3 }}
                            className="flex items-center"
                        >
                            <span className="mt-1 truncate text-3xl font-bold">
                                {t(currentPage)}
                            </span>
                        </motion.div>
                    </AnimatePresence>
                </div>
                {actions && <div className="ml-auto">{actions}</div>}
            </header>

            <main className="relative flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden">
                {children}
            </main>
        </div>
    );
}
