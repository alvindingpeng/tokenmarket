'use client';

import { useMemo } from 'react';
import { useWindowSize } from '@uidotdev/usehooks';

/**
 * Tailwind breakpoints (px)
 */
const BREAKPOINTS = {
    sm: 640,
    md: 768,
    lg: 1024,
    xl: 1280,
    '2xl': 1536,
} as const;

type Breakpoint = keyof typeof BREAKPOINTS;

/**
 * Configuration for grid page size calculation
 */
export interface GridPageSizeConfig {
    /** Item height in pixels */
    itemHeight: number;
    /** Gap between items in pixels (default: 16 = gap-4) */
    gap?: number;
    /** Responsive column configuration: { breakpoint: columns } */
    columns: Partial<Record<Breakpoint | 'default', number>>;
    /** 
     * Offset for header/padding area (default: 100)
     * Header: logo(48) + my-6(24*2) ≈ 100px
     */
    offset?: number;
    /** Minimum items per page (default: 1) */
    minItems?: number;
    /** Maximum items per page (default: 50) */
    maxItems?: number;
}

/**
 * Get number of columns based on window width and column config
 */
function getColumnsForWidth(
    width: number,
    columns: Partial<Record<Breakpoint | 'default', number>>
): number {
    // Check breakpoints from largest to smallest
    if (width >= BREAKPOINTS['2xl'] && columns['2xl'] !== undefined) {
        return columns['2xl'];
    }
    if (width >= BREAKPOINTS.xl && columns.xl !== undefined) {
        return columns.xl;
    }
    if (width >= BREAKPOINTS.lg && columns.lg !== undefined) {
        return columns.lg;
    }
    if (width >= BREAKPOINTS.md && columns.md !== undefined) {
        return columns.md;
    }
    if (width >= BREAKPOINTS.sm && columns.sm !== undefined) {
        return columns.sm;
    }
    return columns.default ?? 1;
}

/**
 * Hook to calculate optimal page size based on viewport dimensions
 *
 * @param config - Grid configuration
 * @returns Calculated page size
 *
 * @example
 * ```tsx
 * const pageSize = useGridPageSize({
 *   itemHeight: 216,
 *   gap: 16,
 *   columns: { default: 1, md: 2, lg: 3, xl: 4 },
 * });
 * ```
 */
export function useGridPageSize(config: GridPageSizeConfig): number {
    const { width, height } = useWindowSize();

    const pageSize = useMemo(() => {
        const {
            itemHeight,
            gap = 16,
            columns,
            offset = 100,
            minItems = 1,
            maxItems = 50,
        } = config;

        // Fallback for SSR or initial render
        if (!width || !height) {
            return getColumnsForWidth(1024, columns); // Default to lg breakpoint
        }

        // Calculate columns based on current width
        const cols = getColumnsForWidth(width, columns);

        // Calculate available height for the grid
        const availableHeight = height - offset;

        // Calculate how many rows fit
        // If we have N rows: totalHeight = N * itemHeight + (N-1) * gap
        // Rearranging: N = (availableHeight + gap) / (itemHeight + gap)
        const rowHeight = itemHeight + gap;
        const rows = Math.max(1, Math.floor((availableHeight + gap) / rowHeight));

        // Calculate total items
        const totalItems = cols * rows;

        // Clamp to min/max
        return Math.max(minItems, Math.min(maxItems, totalItems));
    }, [width, height, config]);

    return pageSize;
}
