
'use client';

import { useState, useEffect } from 'react';
import { motion, AnimatePresence } from "motion/react"
import { useAuth } from '@/api/endpoints/user';
import { LoginForm } from '@/components/modules/login';
import { ContentLoader } from '@/route/content-loader';
import { NavBar, useNavStore } from '@/components/modules/navbar';
import { useTranslations } from 'next-intl'
import Logo from '@/components/modules/logo';
import { Toolbar } from '@/components/modules/toolbar';
import { ENTRANCE_VARIANTS } from '@/lib/animations/fluid-transitions';

// Logo 绘制动画时长
const LOGO_ANIMATION_DURATION = 1400;

export function AppContainer() {
    const { isAuthenticated, isLoading: authLoading } = useAuth();
    const { activeItem, direction } = useNavStore();
    const t = useTranslations('navbar');

    // Logo 动画完成状态
    const [logoAnimationComplete, setLogoAnimationComplete] = useState(false);

    useEffect(() => {
        const timer = setTimeout(() => setLogoAnimationComplete(true), LOGO_ANIMATION_DURATION);
        return () => clearTimeout(timer);
    }, []);

    // 加载状态
    const isLoading = authLoading || !logoAnimationComplete;

    // 加载页面
    if (isLoading) {
        return (
            <div className="min-h-screen flex items-center justify-center bg-background">
                <Logo size={120} animate />
            </div>
        );
    }

    // 登录页面
    if (!isAuthenticated) {
        return (
            <AnimatePresence mode="wait">
                <motion.div
                    key="login"
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.3 }}
                >
                    <LoginForm />
                </motion.div>
            </AnimatePresence>
        );
    }

    // 主界面
    return (
        <motion.div
            key="main-app"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: 0.3 }}
            className="px-3 md:px-6 max-w-6xl mx-auto md:grid md:grid-cols-[auto_1fr] md:gap-6"
        >
            <NavBar />
            <main className="w-full mb-28 min-w-0 md:mb-6">
                <header className="flex items-center gap-x-2 my-6 px-2">
                    <Logo size={48} />
                    <div className="flex-1 overflow-hidden">
                        <AnimatePresence mode="wait" custom={direction}>
                            <motion.div
                                key={activeItem}
                                custom={direction}
                                variants={{
                                    initial: (direction: number) => ({
                                        y: 32 * direction,
                                        opacity: 0
                                    }),
                                    animate: {
                                        y: 0,
                                        opacity: 1
                                    },
                                    exit: (direction: number) => ({
                                        y: -32 * direction,
                                        opacity: 0
                                    })
                                }}
                                initial="initial"
                                animate="animate"
                                exit="exit"
                                transition={{ duration: 0.3 }}
                                className="flex items-center"
                            >
                                <span className="text-3xl font-bold mt-1">{t(activeItem)}</span>
                                {/* Header 插槽 - 子页面通过 Portal 渲染到这里 */}
                            </motion.div>
                        </AnimatePresence>
                    </div>
                    <div className="ml-auto">
                        <Toolbar />
                    </div>
                </header>
                <motion.div
                    variants={ENTRANCE_VARIANTS.content}
                    initial="initial"
                    animate="animate"
                >
                    <ContentLoader activeRoute={activeItem} />
                </motion.div>
            </main>
        </motion.div>
    );
}

