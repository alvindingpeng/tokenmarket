'use client';

import { AnimatePresence, motion } from 'framer-motion';
import { CONTENT_MAP } from './config';

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
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.3 }}
            >
                <Component />
            </motion.div>
        </AnimatePresence>
    );
}
