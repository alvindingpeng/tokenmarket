'use client';

import { motion } from 'motion/react';

interface LogoProps {
    size?: number | string;
    animate?: boolean;
}

const paths = [
    "M50 15 C70 15 85 30 85 50 C85 65 75 75 70 80 M50 15 C30 15 15 30 15 50 C15 65 25 75 30 80",
    "M30 80 Q30 90 20 90",
    "M43 77 Q43 90 38 90",
    "M57 77 Q57 90 62 90",
    "M70 80 Q70 90 80 90",
];

export default function Logo({ size = 48, animate = false }: LogoProps) {
    const sizeValue = size === '100%' ? '100%' : size;

    if (animate) {
        return (
            <motion.svg
                viewBox="0 0 100 100"
                xmlns="http://www.w3.org/2000/svg"
                width={sizeValue}
                height={sizeValue}
                className="text-primary"
            >
                {paths.map((d, index) => (
                    <motion.path
                        key={index}
                        d={d}
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="6"
                        strokeLinecap="round"
                        initial={{ pathLength: 0, opacity: 0 }}
                        animate={{ pathLength: 1, opacity: 1 }}
                        transition={{
                            pathLength: {
                                duration: 0.8,
                                delay: index * 0.15,
                                ease: "easeInOut",
                            },
                            opacity: {
                                duration: 0.2,
                                delay: index * 0.15,
                            },
                        }}
                    />
                ))}
            </motion.svg>
        );
    }

    return (
        <motion.svg
            viewBox="0 0 100 100"
            xmlns="http://www.w3.org/2000/svg"
            width={sizeValue}
            height={sizeValue}
            className="text-primary"
        >
            {paths.map((d, index) => (
                <path
                    key={index}
                    d={d}
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="6"
                    strokeLinecap="round"
                />
            ))}
        </motion.svg>
    );
}
