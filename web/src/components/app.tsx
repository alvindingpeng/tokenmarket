
'use client';

import { motion, AnimatePresence } from "framer-motion"
import { useAuth } from '@/api/endpoints/user';
import { LoginForm } from '@/components/modules/login';
import { ContentLoader } from '@/route/content-loader';
import { NavBar, useNavStore } from '@/components/modules/navbar';
import { useTranslations } from 'next-intl'
import Logo from '@/components/modules/logo';
import { LOADING_VARIANTS, ENTRANCE_VARIANTS } from '@/lib/animations/fluid-transitions';

export function AppContainer() {
    const { isAuthenticated, isLoading } = useAuth();
    const { activeItem } = useNavStore();
    const t = useTranslations('navbar');

    if (isLoading) {
        return (
            <motion.div
                key="loading"
                variants={LOADING_VARIANTS}
                initial="initial"
                animate="animate"
                exit="exit"
                className="min-h-screen flex items-center justify-center"
            >
                <div className="text-center">
                    <div className="animate-spin rounded-full border-b-2 border-primary mx-auto h-8 w-8" />
                </div>
            </motion.div>
        );
    }

    if (!isAuthenticated) {
        return (
            <AnimatePresence mode="wait">
                <LoginForm />
            </AnimatePresence>
        )
    }

    return (
        <motion.div
            key="main-app"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: 0.3 }}
            className="px-3 md:px-6 max-w-6xl mx-auto md:flex"
        >
            <NavBar />
            <main className="w-full mb-28 min-w-0">
                <motion.header
                    variants={ENTRANCE_VARIANTS.header}
                    initial="initial"
                    animate="animate"
                    className="flex items-center gap-x-2 my-6"
                >
                    <Logo />
                    <div className="text-3xl font-bold mt-1">{t(activeItem)}</div>
                </motion.header>
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
