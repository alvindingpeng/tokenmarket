'use client';

import { AnimatePresence, motion } from 'framer-motion';
import { CONTENT_MAP } from './config';
import { ROUTE_CHANGE_VARIANTS } from '@/lib/animations/fluid-transitions';

export function ContentLoader({ activeRoute }: { activeRoute: string }) {
    const Component = CONTENT_MAP[activeRoute];

    if (!Component) {
        return (
            <div className="flex items-center justify-center h-64">
                <p className="text-muted-foreground">Route not found: {activeRoute}</p>
            </div>
        );
    }

    return (
        <AnimatePresence mode="wait">
            <motion.div
                key={activeRoute}
                variants={ROUTE_CHANGE_VARIANTS}
                initial="initial"
                animate="animate"
                exit="exit"
            >
                <Component />
            </motion.div>
        </AnimatePresence>
    );
}
