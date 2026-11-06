import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest, apiQueryRetry } from './client';
import { POLL_NORMAL } from '@/lib/polling';

// 用户自管花费预算: 与管理端同表, scope 锁定本人 user 级。
// 用量(spent/ratio_pct/window_end)由服务端算好, 前端直接渲染进度条。
export type MyBudget = {
    id: number;
    user_id: number;
    period: string;
    action: string;
    limit_amount: number;
    warn_ratio: number;
    enabled: boolean;
    spent: number;
    limit: number;
    ratio_pct: number;
    window_end: string;
};

export type MyBudgetInput = {
    period: string;
    action: string;
    limit_amount: number;
    warn_ratio: number;
    enabled?: boolean;
};

const myBudgetsPath = '/api/v1/user/budgets';

async function fetchMyBudgets(signal?: AbortSignal): Promise<MyBudget[]> {
    const res = await apiRequest<{ items: MyBudget[] }>(myBudgetsPath, { signal });
    return res.items ?? [];
}

export function useMyBudgets() {
    return useQuery({ queryKey: ['my-budgets'] as const, queryFn: ({ signal }: { signal: AbortSignal }) => fetchMyBudgets(signal), retry: apiQueryRetry, refetchInterval: POLL_NORMAL });
}

export function useMyBudgetMutations() {
    const qc = useQueryClient();
    const invalidate = () => void qc.invalidateQueries({ queryKey: ['my-budgets'] });
    const create = useMutation({
        mutationFn: (input: MyBudgetInput) => apiRequest<{ item: MyBudget }>(myBudgetsPath, { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
    const update = useMutation({
        mutationFn: (args: { id: number } & MyBudgetInput) =>
            apiRequest<{ item: MyBudget }>(myBudgetsPath + '/' + args.id, { method: 'PUT', body: args }),
        onSuccess: invalidate,
    });
    const remove = useMutation({
        mutationFn: (id: number) => apiRequest<{ ok: boolean }>(myBudgetsPath + '/' + id, { method: 'DELETE' }),
        onSuccess: invalidate,
    });
    return { create, update, remove };
}
