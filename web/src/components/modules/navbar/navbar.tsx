"use client"

import { useSyncExternalStore } from "react"
import { motion } from "motion/react"
import { cn } from "@/lib/utils"
import { useNavStore, type NavItem } from "@/components/modules/navbar"
import { ROUTES } from "@/route/config"
import { usePreload } from "@/route/use-preload"
import { ENTRANCE_VARIANTS } from "@/lib/animations/fluid-transitions"

// 尺寸配置 - 按钮 = padding*2 + icon(24px)
const MOBILE = { size: 40, gap: 4 }   // p-2 (8*2) + icon 24 = 40, gap-1 = 4
const DESKTOP = { size: 48, gap: 12 } // p-3 (12*2) + icon 24 = 48, gap-3 = 12

function subscribeMediaQuery(query: string, cb: () => void) {
    const mql = window.matchMedia(query)
    const handler = () => cb()
    mql.addEventListener('change', handler)
    return () => mql.removeEventListener('change', handler)
}

function getMediaSnapshot(query: string) {
    return window.matchMedia(query).matches
}

function useMediaQuery(query: string) {
    return useSyncExternalStore(
        (cb) => subscribeMediaQuery(query, cb),
        () => getMediaSnapshot(query),
        () => false,
    )
}

export function NavBar() {
    const { activeItem, setActiveItem } = useNavStore()
    const { preload } = usePreload()

    // 检测是否为桌面端
    const isDesktop = useMediaQuery('(min-width: 768px)')

    // 计算当前选中项的索引
    const activeIndex = ROUTES.findIndex(route => route.id === activeItem)

    // 计算指示器的偏移量
    const config = isDesktop ? DESKTOP : MOBILE
    const offset = activeIndex * (config.size + config.gap)

    return (
        <div className="relative z-50 md:min-h-screen">
            <motion.nav
                aria-label="Main Navigation"
                className={cn(
                    "fixed bottom-6 left-1/2 -translate-x-1/2 flex items-center gap-1 p-3",
                    "md:sticky md:top-30 md:left-auto md:bottom-auto md:translate-x-0 md:flex-col md:gap-3",
                    "bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl",
                    "custom-shadow"
                )}
                variants={ENTRANCE_VARIANTS.navbar}
                initial="initial"
                animate="animate"
            >
                {/* 选中指示器 - 使用 Framer Motion 动画 */}
                <motion.div
                    className={cn(
                        "absolute bg-sidebar-primary rounded-2xl pointer-events-none",
                        "z-10"  // 在 hover 背景之上
                    )}
                    style={{
                        width: config.size,
                        height: config.size,
                    }}
                    initial={false}
                    animate={{
                        left: isDesktop ? 12 : 12 + offset,
                        top: isDesktop ? 12 + offset : 12,
                    }}
                    transition={{ type: "spring", stiffness: 300, damping: 30 }}
                />

                {ROUTES.map((route, index) => {
                    const isActive = activeItem === route.id
                    return (
                        <motion.button
                            key={route.id}
                            type="button"
                            onClick={() => setActiveItem(route.id as NavItem)}
                            onMouseEnter={() => preload(route.id)}
                            className={cn(
                                "relative p-2 md:p-3 rounded-2xl z-20",
                                isActive ? "text-sidebar-primary-foreground" : "text-sidebar-foreground/60 hover:bg-sidebar-accent"
                            )}
                            initial={{ opacity: 0, scale: 0.8 }}
                            animate={{
                                opacity: 1,
                                scale: 1,
                                transition: {
                                    delay: index * 0.05,
                                    duration: 0.3,
                                }
                            }}
                            whileHover={{ scale: 1.1, zIndex: 30 }}
                            whileTap={{ scale: 0.95 }}
                        >
                            <route.icon strokeWidth={2} />
                        </motion.button>
                    )
                })}
            </motion.nav>
        </div>
    )
}
