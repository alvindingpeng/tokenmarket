import { useQuery } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { POLL_MINUTE } from '@/lib/polling';

// 汇率快照(账面币种 USD 为基准): 改价工作台的 USD 等值预览与账单复核共用同一份数据。
export type FxRateItem = {
    from: string;
    to: string;
    rate: number;
    day: string;
    source: string;
};

const fxPath = '/api/v1/fx/rates';

export function useFxRates() {
    return useQuery({
        queryKey: ['fx-rates'] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<{ day: string; items: FxRateItem[] }>(fxPath, { signal }),
        retry: apiQueryRetry,
        // 牌价一天一更; 前端分钟级低频跟一次即可。
        refetchInterval: POLL_MINUTE,
    });
}
