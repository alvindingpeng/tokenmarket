import type { CSSProperties, ReactNode } from 'react';
import { useEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'use-intl';
import Logo from '@/components/modules/logo';
import { SiteBanners } from '@/components/site-banners';
import { navItemsFor, useAppStore } from '@/stores/app';
import { useAuthStore } from '@/api/user';
import { preloadPage } from '@/lib/page-preload';
import { cn } from '@/lib/utils';

// AppShell 作为普通用户界面的稳定布局层，统一渲染导航、顶栏和页面内容。
export function AppShell({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
    const currentPage = useAppStore((state) => state.currentPage);
    const direction = useAppStore((state) => state.direction);
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const t = useTranslations('navbar');
    const role = useAuthStore((state) => state.role);
    const navItems = navItemsFor(role); // 导航按角色裁剪: 无权页面不出现在 Dock 中。
    const activeIndex = navItems.findIndex((route) => route.id === currentPage); // activeIndex 表示选中项在 Dock 中的位置。
    const [hoveredIndex, setHoveredIndex] = useState<number | null>(null); // hoveredIndex 表示当前悬浮项的位置。
    const [isNavHovered, setIsNavHovered] = useState(false); // isNavHovered 表示悬浮背景是否显示。
    const hoverIndicatorRef = useRef<HTMLSpanElement>(null); // hoverIndicatorRef 用于在淡入前确认悬浮背景的新位置。
    const navRef = useRef<HTMLElement>(null); // navRef 指向导航容器, 用于把选中项滚入视野。

    // 导航在两种形态下都会滚动: 手机是横向单行, iPad/矮窗口是纵向长列。
    // 切页后必须把选中项滚进视野, 否则当前页停在屏幕外 —— 用户既看不到高亮也点不到它。
    // 只沿真正溢出的那个轴滚动, 避免在另一轴上带动整页产生横向抖动。
    useEffect(() => {
        const container = navRef.current;
        if (!container) return;
        const active = container.children[activeIndex + 2] as HTMLElement | undefined; // 前两个子节点是两个指示层。
        if (!active) return;
        if (container.scrollWidth > container.clientWidth) {
            active.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' });
        } else if (container.scrollHeight > container.clientHeight) {
            active.scrollIntoView({ behavior: 'smooth', block: 'center', inline: 'nearest' });
        }
    }, [activeIndex, currentPage]);

    return (
        <div className="mx-auto flex h-dvh max-w-6xl animate-in flex-col overflow-hidden px-3 fade-in duration-300 md:grid md:grid-cols-[auto_1fr] md:grid-rows-[auto_auto_minmax(0,1fr)] md:gap-x-6 md:px-6">
            {/* md:min-h-0 让这一列可以被压缩, 否则内容高度会把网格行撑破并被外层 overflow-hidden 裁掉。 */}
            {/* row-span-3 跨过头部与横幅槽: 横幅出现或消失都不改变导航的 sticky 容器高度。 */}
            <div className="relative z-50 md:row-span-3 md:min-h-0">
                <nav
                    ref={navRef}
                    aria-label="Main Navigation"
                    className={cn(
                        // 移动端: 贴底单行, 横向可滚动 —— 导航项多于屏宽时滑动查看, 绝不换行挤压或溢出屏幕。
                        // 垂直位置用安全区偏移, 避免被 iOS 底部横条与浏览器工具栏盖住。
                        'fixed left-1/2 isolate flex -translate-x-1/2 animate-in items-center gap-1 p-2 fade-in zoom-in-95 duration-300',
                        'max-w-[calc(100vw-1rem)] overflow-x-auto overflow-y-hidden overscroll-x-contain',
                        // 手机上不存在悬浮滚动条语义, 隐藏后仍可滑动。
                        'nav-scrollbar',
                        // 竖排时改成纵向可滚动: iPad 及矮窗口放不下十几个导航项, 没有上限就会被裁掉且拖不出来。
                        // max-height 用 dvh 减去上下的留白, 保证最后一项永远能滚到; overflow-x 保持 hidden 避免横向抖动。
                        // top-24 与 max-height 共用同一组留白: 顶部 6rem 对齐标题行, 底部再留 2rem,
                        // 两项相加正好是 100dvh 减去 max-height, 导航既不会顶到页头也不会漏出屏幕底。
                        'md:sticky md:top-24 md:left-auto md:bottom-auto md:translate-x-0 md:max-w-none md:flex-col md:gap-3 md:p-3',
                        'md:max-h-[calc(100dvh-8rem)] md:overflow-y-auto md:overflow-x-hidden md:overscroll-contain md:nav-scrollbar',
                        'bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl',
                    )}
                    style={{ bottom: 'calc(env(safe-area-inset-bottom, 0px) + 0.75rem)' }}
                    onMouseLeave={() => setIsNavHovered(false)}
                >
                    <span
                        aria-hidden="true"
                        className="pointer-events-none absolute left-2 top-2 z-10 size-10 shrink-0 rounded-2xl bg-sidebar-primary transition-transform duration-300 ease-out [transform:translateX(var(--nav-offset-x))] md:left-3 md:top-3 md:size-12 md:[transform:translateY(var(--nav-offset-y))]"
                        style={{
                            '--nav-offset-x': `${activeIndex * 2.75}rem`,
                            '--nav-offset-y': `${activeIndex * 3.75}rem`,
                        } as CSSProperties}
                    />
                    <span
                        ref={hoverIndicatorRef}
                        aria-hidden="true"
                        className={cn(
                            'pointer-events-none absolute left-2 top-2 z-0 size-10 shrink-0 [transform:translateX(var(--nav-offset-x))] md:left-3 md:top-3 md:size-12 md:[transform:translateY(var(--nav-offset-y))]',
                            isNavHovered ? 'transition-transform duration-300 ease-out' : 'transition-none',
                        )}
                        style={{
                            '--nav-offset-x': `${(hoveredIndex ?? activeIndex) * 2.75}rem`,
                            '--nav-offset-y': `${(hoveredIndex ?? activeIndex) * 3.75}rem`,
                        } as CSSProperties}
                    >
                        <span
                            className="absolute inset-0 rounded-2xl bg-sidebar-accent transition-opacity duration-350 ease-linear"
                            style={{ opacity: isNavHovered ? 1 : 0 }}
                        />
                    </span>
                    {navItems.map((route, index) => {
                        const isActive = currentPage === route.id;

                        return (
                            <button
                                key={route.id}
                                type="button"
                                aria-label={route.label}
                                aria-current={isActive ? 'page' : undefined}
                                onMouseEnter={() => {
                                    // 首次进入先在不可见状态下定位，避免背景从上一次位置移动过来。
                                    if (isNavHovered) {
                                        setHoveredIndex(index);
                                    } else {
                                        flushSync(() => setHoveredIndex(index));
                                        hoverIndicatorRef.current?.getBoundingClientRect();
                                        setIsNavHovered(true);
                                    }
                                    preloadPage(route.id);
                                }}
                                onFocus={() => preloadPage(route.id)}
                                onTouchStart={() => preloadPage(route.id)}
                                onClick={() => {
                                    preloadPage(route.id);
                                    setCurrentPage(route.id);
                                }}
                                className={cn(
                                    'relative z-20 flex size-10 shrink-0 items-center justify-center rounded-2xl p-2 transition-[color,scale] duration-150 ease-linear hover:z-30 hover:scale-110 active:scale-95 md:size-12 md:p-3',
                                    isActive ? 'text-sidebar-primary-foreground' : 'text-sidebar-foreground/60',
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

            <header className="my-3 md:my-6 flex flex-none items-center gap-x-2 px-2">
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

            {/* 站点横幅槽(公告 / 维护模式提示, 取自「系统信息配置」)。
                没有内容时这是一个高度为 0 的空 div, 而不是被移出 DOM 的网格项 ——
                否则网格行序会少一项, 把 main 挤到 auto 行上失去 1fr 高度。 */}
            <div className="flex-none px-2 md:px-0">
                {/* 外层已负责左右留白, 这里只要纵向堆叠。 */}
                <SiteBanners className="flex flex-col gap-2" />
            </div>

            {/* main 是页面内容的默认滚动容器: 各页只需管好自己的高度, 无需各自再造一个滚动层。 */}
            {/* 底部内边距按导航高度 + 安全区留净空, 保证最后一行内容不被贴底导航盖住。 */}
            <main
                className="relative flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-y-auto overflow-x-hidden overscroll-contain px-1 pb-24 md:overflow-hidden md:px-0 md:pb-0"
                style={{ scrollPaddingBottom: 'calc(env(safe-area-inset-bottom, 0px) + 5.5rem)' }}
            >
                {children}
            </main>
        </div>
    );
}
