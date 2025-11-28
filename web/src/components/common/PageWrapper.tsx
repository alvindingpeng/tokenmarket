'use client';

import { motion } from 'framer-motion';
import { ReactNode } from 'react';
import { CARD_VARIANTS } from '@/lib/animations/fluid-transitions';

interface PageWrapperProps {
  children: ReactNode;
  className?: string;
}

/**
 * 通用页面包装器，为页面内容添加流体动画效果
 */
export function PageWrapper({ children, className = 'space-y-6' }: PageWrapperProps) {
  return (
    <motion.div
      className={className}
      variants={CARD_VARIANTS.container}
      initial="initial"
      animate="animate"
    >
      {Array.isArray(children) ? (
        children.map((child, index) => (
          <motion.div key={index} variants={CARD_VARIANTS.item}>
            {child}
          </motion.div>
        ))
      ) : (
        <motion.div variants={CARD_VARIANTS.item}>
          {children}
        </motion.div>
      )}
    </motion.div>
  );
}

