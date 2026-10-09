import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

export type ReconcileRow = {
    id: number; day: string; records: number; user_cost: number; owner_revenue: number;
    platform_revenue: number; duplicated: number; missing: number; stale_hold: number;
    unsettled: number; status: 'balanced' | 'mismatch' | 'review'; detail: string;
    created_at: string; updated_at: string;
};

export type BalanceFlow = {
    request_id: string; user_id: number; kind: 'reserve' | 'settle' | 'release';
    amount: number; settled: boolean; created_at: string;
    user_cost: number; owner_revenue: number; platform_revenue: number;
};

export type ArchiveFile = { name: string; size: number; mod_time: string };
export type BackupFile = { name: string; size: number; mod_time: string };

export type ChannelHealth = {
    id: number; channel_id: number; channel_code: string; latency_ms: number;
    status_code: number; error: string; healthy: boolean; checked_at: string;
};

export type ChannelAlert = {
    id: number; channel_id: number; channel_code: string;
    kind: 'down' | 'recovered' | 'degraded' | 'mismatch' | 'backup_failed' | 'archive_failed' | 'billing_failed' | 'test';
    reason: string;
    notified: boolean; created_at: string;
};

export type LifecycleResult = {
    cutoff: string; archived: number; file: string; requests_deleted: number;
    attempts_deleted: number; body_cleared: number; archive_error?: string;
    archive_enabled: boolean;
};

export type BackupResult = { file: string; size: number; verified: boolean; pruned: number };

export type OpsOverview = {
    requests_24h: number; success_24h: number; failed_24h: number;
    input_tokens_24h: number; output_tokens_24h: number; cost_24h: number;
    active_requests: number; channels_total: number; channels_enabled: number; alerts_24h: number;
};

// useOpsOverview 运维概览: 24 小时调用汇总与实时计数, 30 秒自动刷新。
export function useOpsOverview() {
    return useQuery({
        queryKey: ['ops', 'overview'],
        queryFn: () => apiRequest<OpsOverview>('/api/v1/ops/overview'),
        refetchInterval: 30000,
    });
}

export function useReconcileList(limit = 30) {
    return useQuery({
        queryKey: ['ops', 'reconcile', limit],
        queryFn: () => apiRequest<{ items: ReconcileRow[] }>('/api/v1/ops/reconcile/list?limit=' + limit).then(r => r.items),
    });
}

export function useRunReconcile() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (days: number = 1) =>
            apiRequest<{ items: ReconcileRow[] }>('/api/v1/ops/reconcile/run?days=' + days, { method: 'POST' }).then(r => r.items),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'reconcile'] }),
    });
}

export function useBalanceFlow(userId?: number, limit = 100) {
    return useQuery({
        queryKey: ['ops', 'balance-flow', userId, limit],
        queryFn: () => {
            let url = '/api/v1/ops/balance-flow?limit=' + limit;
            if (userId) url += '&user_id=' + userId;
            return apiRequest<{ items: BalanceFlow[] }>(url).then(r => r.items);
        },
    });
}

export function useArchives() {
    return useQuery({
        queryKey: ['ops', 'archives'],
        queryFn: () => apiRequest<{ items: ArchiveFile[]; retention_days: number }>('/api/v1/ops/log-lifecycle/list'),
    });
}

export function useRunLogLifecycle() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<LifecycleResult>('/api/v1/ops/log-lifecycle/run', { method: 'POST' }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'archives'] }),
    });
}

export function useBackups() {
    return useQuery({
        queryKey: ['ops', 'backups'],
        queryFn: () => apiRequest<{ items: BackupFile[] }>('/api/v1/ops/backup/list').then(r => r.items),
    });
}

export function useRunBackup() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<BackupResult>('/api/v1/ops/backup/run', { method: 'POST' }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'backups'] }),
    });
}

export function useChannelHealth(channelId?: number, limit = 50) {
    return useQuery({
        queryKey: ['ops', 'health', channelId, limit],
        queryFn: () => {
            let url = '/api/v1/ops/health/list?limit=' + limit;
            if (channelId) url += '&channel_id=' + channelId;
            return apiRequest<{ items: ChannelHealth[] }>(url).then(r => r.items);
        },
    });
}

export function useChannelAlerts(limit = 50) {
    return useQuery({
        queryKey: ['ops', 'alerts', limit],
        queryFn: () => apiRequest<{ items: ChannelAlert[] }>('/api/v1/ops/alerts/list?limit=' + limit).then(r => r.items),
    });
}

export function useTestAlertWebhook() {
    return useMutation({
        mutationFn: () => apiRequest<{ ok: boolean }>('/api/v1/ops/alerts/test-webhook', { method: 'POST' }),
    });
}

export function useRunHealthCheck() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<{ ok: boolean }>('/api/v1/ops/health/run', { method: 'POST' }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'health'] }),
    });
}

/** AlertRule 一条生效中的告警规则: 当前值与默认值并排, 便于确认哪些规则被改过。 */
export type AlertRule = {
    key: string;
    value: number;
    default: number;
    unit: string;
    description: string;
};

export function useAlertRules() {
    return useQuery({
        queryKey: ['ops', 'alert-rules'],
        queryFn: () => apiRequest<{ items: AlertRule[] }>('/api/v1/ops/alerts/rules').then(r => r.items),
    });
}

/** MetricsToken /metrics 抓取凭据: mode 为 off 时令牌无意义, 仅作展示。 */
export type MetricsToken = {
    mode: 'off' | 'bearer';
    token: string;
};

export function useMetricsToken(enabled: boolean) {
    return useQuery({
        queryKey: ['ops', 'metrics-token'],
        queryFn: () => apiRequest<MetricsToken>('/api/v1/ops/metrics/token'),
        enabled,
    });
}

export function useRotateMetricsToken() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<MetricsToken>('/api/v1/ops/metrics/token/rotate', { method: 'POST' }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'metrics-token'] }),
    });
}
