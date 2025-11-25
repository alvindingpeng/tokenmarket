
'use client';

import { motion } from "framer-motion"
import { useAuth } from '@/api/endpoints/user';
import { LoginForm } from '@/components/modules/login';
import { ContentLoader } from '@/route/content-loader';
import { NavBar, useNavStore } from '@/components/modules/navbar';
import { useTranslations } from 'next-intl'
import Logo from '@/components/modules/logo';

export function AppContainer() {
    const { isAuthenticated, isLoading } = useAuth();
    const { activeItem } = useNavStore();
    const t = useTranslations('navbar');

    if (isLoading) {
        return (
            <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
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
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
            >
                <LoginForm />
            </motion.div>
        )
    }

    return (
        <div className="px-3 md:px-6 max-w-7xl mx-auto text-foreground md:flex">
            <div className="md:pr-6">
                <NavBar />
            </div>
            <main className="w-full mb-28">
                <header className="flex items-center gap-x-2 my-6">
                    <Logo />
                    <div className="text-3xl font-bold mt-1">{t(activeItem)}</div>
                </header>
                <ContentLoader activeRoute={activeItem} />
            </main>
        </div>
    );
}
