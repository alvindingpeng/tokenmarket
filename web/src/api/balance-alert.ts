import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';

// BalanceAlertView 余额预警配置: 可用余额低于 threshold_cny 时提醒用户。
// threshold_cny 为 0 等价于禁用 —— 服务端会把 enabled 归一成 false。
export type BalanceAlertView = {
    threshold_cny: number;
    enabled: boolean;
    last_triggered_at?: string;
};

// balanceAlertPath 服务端路由挂在 /api/v1/user/balance-alert 组下
// (internal/server/handlers/balance_alert.go), 前缀缺一不可 —— 少了 /api/v1 就是 404。
export const balanceAlertPath = '/api/v1/user/balance-alert';

// balanceAlertQueryOptions 余额预警配置的查询选项。
// queryFn 接 react-query 的 signal: 组件卸载时正在飞的请求立刻中止; 4xx 不重试(见 apiQueryRetry)。
export const balanceAlertQueryOptions = {
    queryKey: ['balance-alert'] as const,
    queryFn: ({ signal }: { signal: AbortSignal }) => apiRequest<BalanceAlertView>(balanceAlertPath, { signal }),
    retry: apiQueryRetry,
};

// saveBalanceAlert 保存余额预警配置(POST 与 GET/DELETE 同一路径)。
export function saveBalanceAlert(payload: { threshold_cny: number; enabled?: boolean }) {
    return apiRequest<BalanceAlertView>(balanceAlertPath, { method: 'POST', body: payload });
}

// deleteBalanceAlert 删除余额预警配置。
export function deleteBalanceAlert() {
    return apiRequest<{ deleted: boolean }>(balanceAlertPath, { method: 'DELETE' });
}

// useBalanceAlert 读取当前用户的余额预警配置。
export function useBalanceAlert() {
    return useQuery(balanceAlertQueryOptions);
}

// useBalanceAlertMutations 保存与取消余额预警; 成功后作废缓存, 界面以服务端为准。
export function useBalanceAlertMutations() {
    const queryClient = useQueryClient();
    const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['balance-alert'] });

    const save = useMutation({
        mutationFn: saveBalanceAlert,
        onSuccess: invalidate,
    });

    const remove = useMutation({
        mutationFn: deleteBalanceAlert,
        onSuccess: invalidate,
    });

    return { save, remove };
}
