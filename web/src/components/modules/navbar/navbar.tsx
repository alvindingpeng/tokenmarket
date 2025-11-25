"use client"

import { motion } from "framer-motion"
import { cn } from "@/lib/utils"
import { useNavStore, type NavItem } from "@/components/modules/navbar"
import { ROUTES } from "@/route/config"
import { usePreload } from "@/route/use-preload"

export function NavBar() {
    const { activeItem, setActiveItem } = useNavStore()
    const { preload } = usePreload()

    return (
        <aside className="sticky top-30">
            <nav
                aria-label="Main Navigation"
                className={cn(
                    "fixed z-20 bottom-6 left-1/2 -translate-x-1/2 flex items-center gap-1 p-3",
                    "md:relative md:left-auto md:bottom-auto md:translate-x-0 md:flex-col md:gap-3",
                    "bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl",
                    "custom-shadow",
                    "transition duration-300"
                )}
            >
                {ROUTES.map((route) => (
                    <button
                        key={route.id}
                        type="button"
                        onClick={() => setActiveItem(route.id as NavItem)}
                        onMouseEnter={() => preload(route.id)}
                        className={cn(
                            "relative p-2 md:p-3 rounded-2xl",
                            activeItem === route.id ? "text-sidebar-primary-foreground" : "text-sidebar-foreground/60 hover:bg-sidebar-accent"
                        )}
                    >
                        {activeItem === route.id && (
                            <motion.div
                                layoutId="active-nav"
                                className="transition-none absolute inset-0 bg-sidebar-primary rounded-2xl"
                                transition={{ type: "spring", stiffness: 300, damping: 30 }}
                            />
                        )}
                        <route.icon strokeWidth={2} className="relative" />
                    </button>
                ))}
            </nav>
        </aside>
    )
}
