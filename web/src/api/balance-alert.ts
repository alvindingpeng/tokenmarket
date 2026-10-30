import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// BalanceAlertView 余额预警配置: 可用余额低于 threshold_cny 时提醒用户。
// threshold_cny 为 0 等价于禁用 —— 服务端会把 enabled 归一成 false。
export type BalanceAlertView = {
    threshold_cny: number;
    enabled: boolean;
    last_triggered_at?: string;
};

// balanceAlertQueryOptions 余额预警配置的查询选项。
export const balanceAlertQueryOptions = {
    queryKey: ['balance-alert'] as const,
    queryFn: () => apiRequest<BalanceAlertView>('/user/balance-alert'),
};

// useBalanceAlert 读取当前用户的余额预警配置。
export function useBalanceAlert() {
    return useQuery(balanceAlertQueryOptions);
}

// useBalanceAlertMutations 保存与取消余额预警; 成功后作废缓存, 界面以服务端为准。
export function useBalanceAlertMutations() {
    const queryClient = useQueryClient();
    const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['balance-alert'] });

    const save = useMutation({
        mutationFn: (payload: { threshold_cny: number; enabled?: boolean }) =>
            apiRequest<BalanceAlertView>('/user/balance-alert', { method: 'POST', body: payload }),
        onSuccess: invalidate,
    });

    const remove = useMutation({
        mutationFn: () => apiRequest<{ deleted: boolean }>('/user/balance-alert', { method: 'DELETE' }),
        onSuccess: invalidate,
    });

    return { save, remove };
}
