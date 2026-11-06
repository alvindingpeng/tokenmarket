import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { POLL_SLOW } from '@/lib/polling';

export type BillingRecoveryRow = {
    id: number; reservation_id: number; request_id: string; user_id: number;
    status: string; attempts: number; next_attempt_at: string | null;
    last_error: string; created_at: string; updated_at: string;
};

export function useBillingRecovery() {
    return useQuery({
        queryKey: ['ops', 'billing-recovery'],
        queryFn: () => apiRequest<{ items: BillingRecoveryRow[] }>('/api/v1/ops/billing-recovery/list?limit=100').then(r => r.items),
        refetchInterval: POLL_SLOW,
    });
}

export function useRunBillingRecovery() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<{ accepted: boolean }>('/api/v1/ops/billing-recovery/run', { method: 'POST', timeoutMs: 35000 }),
        onSettled: () => qc.invalidateQueries({ queryKey: ['ops', 'billing-recovery'] }),
    });
}

export type ReconcileRow = {
    id: number; day: string; records: number; user_cost: number; owner_revenue: number;
    platform_revenue: number; duplicated: number; missing: number; stale_hold: number;
    unsettled: number; status: 'balanced' | 'mismatch' | 'review' | 'explained'; detail: string;
    // note 是人工处置说明: 差异查清但数字不可改时留下的结论, 重算不会覆盖它。
    note: string; noted_at: string | null; noted_by: number;
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

// ChannelAlert 是一条站内告警。
// subject: 告警主体(真实渠道名 / 用户名 / 任务名), 由产生告警的一方填好。
// channel_code: 保留为分享码, 只有对外分享的渠道才有, 不能当名字用。
// detail: 逐行 "标签: 值" 的结构化明细, 展开查看用; 外部通知正文取自同一批字段。
export type ChannelAlert = {
    id: number; channel_id: number; channel_code: string; subject: string; detail: string;
    kind: 'down' | 'recovered' | 'degraded' | 'key_disabled' | 'model_disabled'
        | 'mismatch' | 'billing_failed' | 'backup_failed' | 'archive_failed'
        | 'low_balance' | 'quota_exceeded' | 'task_failed' | 'task_recovered'
        | 'update_available' | 'watchdog_alert'
        | 'route_persist_failed' | 'route_persist_recovered'
        | 'test';
    reason: string;
    notified: boolean; created_at: string;
};

export type LifecycleResult = {
    cutoff: string; archived: number; file: string; requests_deleted: number;
    attempts_deleted: number; body_cleared: number; archive_error?: string;
    archive_enabled: boolean;
};

export type BackupResult = { file: string; size: number; verified: boolean; pruned: number };

// LatencyPercentiles 一对分位值, 单位毫秒。
export type LatencyPercentiles = { p50_ms: number; p95_ms: number };

// LatencyRow 是按「渠道 × 调用方国家」聚合的延迟分解。
// upstream_wait 是首字节减去 DNS/建连/TLS 后的剩余, 即上游自己排队的时间:
// 它偏高说明该换上游渠道, connect/tls 偏高说明该加出口或走边缘接入。
export type LatencyRow = {
    channel_code: string; channel_key: string; country: string;
    egress_id: number; egress_name: string; // 出口池里的出口; 0 表示未走出口池。
    requests: number; reused_rate: number;
    ttfb: LatencyPercentiles; dns: LatencyPercentiles; connect: LatencyPercentiles;
    tls: LatencyPercentiles; upstream_wait: LatencyPercentiles;
};

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
        refetchInterval: POLL_SLOW,
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
            apiRequest<{ items: ReconcileRow[] }>('/api/v1/ops/reconcile/run?days=' + days, { method: 'POST', timeoutMs: 90000 }).then(r => r.items),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'reconcile'] }),
    });
}

// useSetReconcileNote 登记/撤销某日对账差异的处置说明。
// 只写说明与状态, 差异数字一个都不动 —— 后端也拒绝改数字。
export function useSetReconcileNote() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ day, note }: { day: string; note: string }) =>
            apiRequest<ReconcileRow>('/api/v1/ops/reconcile/note', { method: 'POST', body: { day, note } }),
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

// useLatencyBreakdown 拉取延迟归因, 窗口按天; 归因是排查动作, 不必高频刷新。
export function useLatencyBreakdown(days = 7) {
    return useQuery({
        queryKey: ['ops', 'latency', days],
        queryFn: () => apiRequest<{ days: number; rows: LatencyRow[] }>('/api/v1/ops/latency?days=' + days).then(r => r.rows),
        refetchInterval: POLL_SLOW,
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

// AuditArchiveResult 审计归档执行的回执(与 op.AuditArchiveResult 对齐)。
// deleted 是被清掉的审计行数, archived 是写进归档文件的行数; 两者不等是正常的
// (归档受 archive_enabled 开关控制, 关闭时只删不归档)。
export type AuditArchiveResult = {
    cutoff: string;
    archived: number;
    file: string;
    deleted: number;
    retention_days: number;
    archive_enabled: boolean;
    archive_error?: string;
};

export type AuditArchiveList = {
    items: ArchiveFile[];
    retention_days: number;
    enabled: boolean;
};

// useAuditArchives 审计归档文件清单 + 当前留存配置。
// 与调用日志归档是两个独立的目录/接口, 不能复用 useArchives(键不同, 数据也不同)。
export function useAuditArchives() {
    return useQuery({
        queryKey: ['ops', 'audit-archives'],
        queryFn: () => apiRequest<AuditArchiveList>('/api/v1/ops/audit-lifecycle/list'),
    });
}

// useRunAuditLifecycle 立即归档并清理超期审计记录。该接口要求二次验证:
// apiRequest 撞上 428 会走 step-up 弹窗, 管理员输一次密码后自动重发,
// 所以这里不需要额外处理凭证(与 useRunLogLifecycle / useRunBackup 同一口径)。
export function useRunAuditLifecycle() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<AuditArchiveResult>('/api/v1/ops/audit-lifecycle/run', { method: 'POST', timeoutMs: 90000 }),
        onSuccess: () => qc.invalidateQueries({ queryKey: ['ops', 'audit-archives'] }),
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
        // 备份要落盘并校验, 库大时可能超过默认超时。
        mutationFn: () => apiRequest<BackupResult>('/api/v1/ops/backup/run', { method: 'POST', timeoutMs: 90000 }),
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

// AlertTestChannel 可做连通性测试的渠道, 与后端 alert.SendOnce 的分支保持一致。
// 群机器人渠道也支持一键测试: 否则管理员改完地址只能等下一次真实故障才知道配错了。
export type AlertTestChannel = 'telegram' | 'email' | 'feishu' | 'dingtalk' | 'wecom';

// useTestAlertChannel 按指定渠道发送测试告警, 验证凭据与连通性。
export function useTestAlertChannel() {
    return useMutation({
        mutationFn: (channel: AlertTestChannel) =>
            apiRequest<{ ok: boolean }>(`/api/v1/ops/alerts/test/${channel}`, { method: 'POST' }),
    });
}

export function useRunHealthCheck() {
    const qc = useQueryClient();
    return useMutation({
        // 健康检查会逐个探测渠道上游, 渠道多时可能超过默认超时。
        mutationFn: () => apiRequest<{ ok: boolean }>('/api/v1/ops/health/run', { method: 'POST', timeoutMs: 90000 }),
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
// AlertDeliveryStats 告警送达概览。
// 存在的理由: 改造前"告警没发出去"在界面上完全看不见 —— 2026-11-02 有 260 条告警
// 因 Telegram 实体解析 400 静默丢失, 管理员直到翻日志才发现。
export type AlertDeliveryStats = {
    pending: number; gave_up: number; failed_24h: number; delivered_24h: number;
    // expired 未送达、未达重试上限、但已超出补发窗口而不再自动补发的条数。
    // 没有这一格时, 这类行在概览里一格都不算(既不是 pending 也不是 gave_up),
    // 却在下方列表里显示成"已放弃" —— 看板与列表互相打脸。
    expired: number;
    // redeliver_window_hours 当前补发窗口(小时), 0 表示不限窗口。
    redeliver_window_hours: number;
    last_error: string; last_error_at?: string | null;
    // failure_reasons 最近 24 小时失败原因的前几名(按出现次数降序, 已归一)。
    // 只看 last_error 无法区分偶发与系统性: 同一原因反复出现才是要修的配置问题。
    failure_reasons?: { reason: string; count: number }[];
};

export function useAlertDelivery() {
    return useQuery({
        queryKey: ['ops', 'alert-delivery'],
        queryFn: () => apiRequest<AlertDeliveryStats>('/api/v1/ops/alerts/delivery'),
        refetchInterval: POLL_SLOW,
    });
}

// UndeliveredAlert 尚未送达的告警(含排期与已放弃)。
export type UndeliveredAlert = ChannelAlert & {
    notify_attempts: number;
    next_notify_at?: string | null;
    last_notify_error: string;
    notified_at?: string | null;
};

export function useUndeliveredAlerts(limit = 50) {
    return useQuery({
        queryKey: ['ops', 'alerts-undelivered', limit],
        queryFn: () => apiRequest<{ items: UndeliveredAlert[] }>('/api/v1/ops/alerts/undelivered?limit=' + limit).then(r => r.items),
        refetchInterval: POLL_SLOW,
    });
}

// useRunAlertRedeliver 立即补发一次到期告警(后台任务也会每 5 分钟自动跑)。
export function useRunAlertRedeliver() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<{ sent: number; gave_up: number }>('/api/v1/ops/alerts/redeliver', { method: 'POST', timeoutMs: 70000 }),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: ['ops', 'alert-delivery'] });
            void qc.invalidateQueries({ queryKey: ['ops', 'alerts-undelivered'] });
            void qc.invalidateQueries({ queryKey: ['ops', 'alerts'] });
        },
    });
}

// useRequeueAlert 手动把一条未送达告警重新排进补发队列。
// 自动补发受窗口限制(默认 48 小时), 更早的告警只能由管理员显式放行 —— 直接改数据库
// 既危险又不可审计。
export function useRequeueAlert() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: number) => apiRequest<{ ok: boolean }>(`/api/v1/ops/alerts/${id}/requeue`, { method: 'POST' }),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: ['ops', 'alert-delivery'] });
            void qc.invalidateQueries({ queryKey: ['ops', 'alerts-undelivered'] });
            void qc.invalidateQueries({ queryKey: ['ops', 'alerts'] });
        },
    });
}
// TaskStatus 与后端 task.TaskStatus 一一对应; 时间字段后端用 RFC3339, 零值序列化为 "0001-01-01T00:00:00Z"。
// last_start 为零值即"启动后从未触发", 前端要据此显示"从未运行"而不是一个 2000 多年前的时间。
export type TaskStatus = {
    name: string;
    scheduled: boolean;
    interval_ns: number;
    run_on_start: boolean;
    running: boolean;
    consecutive_failures: number;
    last_start: string;
    last_finish: string;
    last_success: string;
    last_duration_ns: number;
    last_error?: string;
};

// useTaskStatus 轮询后台任务运行状态。纯只读接口, 慢轮询足够 —— 任务最小周期是分钟级。
export function useTaskStatus() {
    return useQuery({
        queryKey: ['ops', 'tasks'],
        queryFn: () => apiRequest<{ items: TaskStatus[] }>('/api/v1/ops/tasks').then(r => r.items),
        refetchInterval: POLL_SLOW,
    });
}

// AlertSilenceWindow 告警静默窗口: 计划内维护期间告警照常落库但不外发。
export type AlertSilenceWindow = {
    id: number;
    start_at: string;
    end_at: string;
    kind: string; // 空串表示静默全部类型
    note: string;
    created_by: number;
    created_at: string;
};

// useAlertSilence 窗口清单(含已过期的, 供回看)。
export function useAlertSilence() {
    return useQuery({
        queryKey: ['ops', 'alert-silence'],
        queryFn: () => apiRequest<{ items: AlertSilenceWindow[] }>('/api/v1/ops/silence/list').then(r => r.items),
        refetchInterval: POLL_SLOW,
    });
}

// useCreateAlertSilence 新建窗口。后端校验时长(最长 72h)与类型闭集, 失败直接 toast。
export function useCreateAlertSilence() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (input: { start_at: string; end_at: string; kind?: string; note?: string }) =>
            apiRequest<{ item: AlertSilenceWindow }>('/api/v1/ops/silence/create', { method: 'POST', body: JSON.stringify(input) }),
        onSuccess: () => void qc.invalidateQueries({ queryKey: ['ops', 'alert-silence'] }),
    });
}

// useDeleteAlertSilence 取消静默(幂等)。
export function useDeleteAlertSilence() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: number) => apiRequest<{ ok: boolean }>('/api/v1/ops/silence/' + id, { method: 'DELETE' }),
        onSuccess: () => void qc.invalidateQueries({ queryKey: ['ops', 'alert-silence'] }),
    });
}

