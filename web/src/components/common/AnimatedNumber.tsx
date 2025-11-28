import { useEffect, useState, useRef } from 'react';

interface AnimatedNumberProps {
    value: string | number | undefined;
    duration?: number;
}

export function AnimatedNumber({ value, duration = 800 }: AnimatedNumberProps) {
    const [displayValue, setDisplayValue] = useState(0);
    const [isAnimating, setIsAnimating] = useState(false);
    const animationRef = useRef<number | undefined>(undefined);

    useEffect(() => {
        if (value === undefined || value === null || value === '-') {
            setDisplayValue(0);
            return;
        }

        const numericValue = typeof value === 'string'
            ? parseFloat(value.replace(/,/g, ''))
            : value;

        if (isNaN(numericValue)) {
            return;
        }

        setIsAnimating(true);
        const startValue = displayValue;
        const endValue = numericValue;
        const startTime = Date.now();

        const animate = () => {
            const currentTime = Date.now();
            const elapsed = currentTime - startTime;
            const progress = Math.min(elapsed / duration, 1);

            const easeProgress = 1 - Math.pow(1 - progress, 3);
            const currentValue = startValue + (endValue - startValue) * easeProgress;

            setDisplayValue(currentValue);

            if (progress < 1) {
                animationRef.current = requestAnimationFrame(animate);
            } else {
                setIsAnimating(false);
            }
        };

        animationRef.current = requestAnimationFrame(animate);

        return () => {
            if (animationRef.current) {
                cancelAnimationFrame(animationRef.current);
            }
        };
    }, [value, duration]);

    if (value === undefined || value === null) {
        return <span>-</span>;
    }

    const shouldShowDecimals = typeof value === 'string' && value.includes('.');
    const decimalPlaces = shouldShowDecimals ? 2 : 0;

    const formattedValue = displayValue.toLocaleString('en-US', {
        minimumFractionDigits: decimalPlaces,
        maximumFractionDigits: decimalPlaces
    });

    return <span>{formattedValue}</span>;
}