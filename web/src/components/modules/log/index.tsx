import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Activity, ChevronLeft, ChevronRight, Download, Loader2, Logs, Search, X } from 'lucide-react';
import { toast } from 'sonner';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useAuthStore } from '@/api/user';
import { apiRequest } from '@/api/client';
import { useGroupList, type Group } from '@/api/group';
import { routeEventText, routeReasonText } from './reason';
import { logExportUrl, useLogList, useLogs } from '@/api/log';
import { downloadFile, isStepUpRequired } from '@/api/client';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { LogCard } from './Item';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

// PAGE_SIZE 历史日志每页条数。
const PAGE_SIZE = 20;

// STATUS_OPTIONS 终态筛选选项, 与后端终态定义一致。
const STATUS_OPTIONS = ['success', 'failed', 'canceled', 'committed', 'running'] as const;

interface AutoRoutingSummary {
    requests: number;
    task_classified: number;
    success: number;
    failed: number;
    success_rate: number;
    tasks: { task: string; requests: number; success: number; failed: number; success_rate: number; avg_duration_ms: number; p90_duration_ms: number; first_token_samples: number; avg_first_token_ms: number; p90_first_token_ms: number; stream_started: number; stream_failed_after_first: number; stream_canceled_after_first: number; stream_incomplete_rate: number }[];
    models: { task: string; model: string; attempts: number; success: number; success_rate: number; avg_duration_ms: number; p90_duration_ms: number; first_token_samples: number; avg_first_token_ms: number; p90_first_token_ms: number; avg_cost_usd: number }[];
    exclusions: { reason: string; count: number }[];
    events: { reason: string; count: number }[];
    predictions: { task: string; compared: number; same_model: number; same_model_rate: number; predicted_cost_usd: number; actual_cost_usd: number; cost_delta_usd: number }[];
    errors: { task: string; class: string; count: number }[];
    error_class_counts: { reason: string; count: number }[];
    cost_bands: { task: string; band: string; requests: number; success: number; avg_prompt_tokens: number; avg_cost_usd: number; p90_cost_usd: number }[];
    context_window_hints: {
        group_id?: number;
        group_item_id?: number;
        task: string;
        model: string;
        channel_code: string;
        channel_grant_id?: number;
        samples: number;
        estimated_samples?: number;
        observed_min_input_tokens: number;
        observed_max_input_tokens: number;
        observed_max_success_input_tokens?: number;
        configured_context_length?: number;
        has_context_override?: boolean;
        suggested_context_length: number;
        config_status?: string;
        reason: string;
    }[];
    sample_limit: number;
    readiness: {
        task: string;
        state: string;
        recommended_ramp_percent: number;
        confidence_level_percent: number;
        classified: number;
        evidence_users: number;
        evidence_buckets: number;
        required_buckets: number;
        shadow_requests: number;
        shadow_compared: number;
        shadow_coverage: number;
        prediction_agreement: number;
        prediction_agreement_lower: number;
        active_requests: number;
        active_success_rate: number;
        active_success_lower: number;
        predicted_cost_usd: number;
        actual_cost_usd: number;
        cost_delta_percent: number;
        cost_ratio_p90_percent: number;
        cost_samples: number;
        configured_candidates: number;
        verified_candidates: number;
        anthropic_stream_candidates: number;
        anthropic_stream_verified: number;
        model_teams: number;
        model_samples: number;
        model_eligible_teams: number;
        model_success_rate: number;
        model_teams_lower: number;
        admin_multi_user_evidence: boolean;
        model_min_success_rate: number;
        model_risks: {
            task: string;
            model: string;
            state: 'collect_samples' | 'healthy' | 'blocked';
            active_requests: number;
            success: number;
            failed: number;
            success_rate: number;
            success_lower: number;
            channel_failures: number;
            request_failures: number;
            candidate_failures: number;
            other_failures: number;
            error_class_counts: { reason: string; count: number }[];
            band_risks: {
                task: string;
                model: string;
                band: 'short' | 'medium' | 'long' | 'xlong';
                state: 'collect_samples' | 'healthy' | 'blocked';
                samples: number;
                success: number;
                success_rate: number;
                success_lower: number;
                avg_prompt_tokens: number;
                p90_prompt_tokens: number;
                first_token_samples: number;
                first_token_p90_ms: number;
                stream_started: number;
                stream_incomplete: number;
                stream_incomplete_rate: number;
                error_class_counts: { reason: string; count: number }[];
                eligible_for_promotion: boolean;
                reasons: string[];
            }[];
            earlier_samples: number;
            earlier_success: number;
            earlier_success_rate: number;
            recent_samples: number;
            recent_success: number;
            recent_success_rate: number;
            success_trend: 'insufficient' | 'stable' | 'declining';
            success_rate_delta: number;
            first_token_samples: number;
            first_token_p90_ms: number;
            earlier_first_token_samples: number;
            earlier_first_token_p90_ms: number;
            recent_first_token_samples: number;
            recent_first_token_p90_ms: number;
            first_token_trend: 'insufficient' | 'stable' | 'degrading';
            first_token_p90_delta_ms: number;
            stream_started: number;
            stream_incomplete: number;
            stream_incomplete_rate: number;
            earlier_stream_started: number;
            earlier_stream_incomplete: number;
            earlier_stream_incomplete_rate: number;
            recent_stream_started: number;
            recent_stream_incomplete: number;
            recent_stream_incomplete_rate: number;
            stream_trend: 'insufficient' | 'stable' | 'degrading';
            stream_incomplete_rate_delta: number;
            eligible_for_promotion: boolean;
            reasons: string[];
        }[];
        first_token_samples: number;
        first_token_p90_ms: number;
        stream_started: number;
        stream_incomplete: number;
        stream_incomplete_rate: number;
        reasons: string[];
    }[];
}

// AutoRoutingPanel 从现有调用日志聚合实验数据。看板不要求先打开所有请求详情，
// 因而不会在前端逐条拼装尝试记录，也不会失真于只保留 300 条的实时列表。
function AutoRoutingPanel() {
    const t = useTranslations('log.history');
    const tr = useTranslations('log.autoRouting');
    const [days, setDays] = useState(7);
    const groups = useGroupList();
    const [groupID, setGroupID] = useState(0);
    const role = useAuthStore((state) => state.role);
    const groupOptions = useMemo(() => (groups.data ?? []).filter((group: Group) => group.mode === 'score' && group.relay_config.auto_routing), [groups.data]);
    const summary = useQuery({
        queryKey: ['logs', 'auto-routing', 'summary', days, groupID],
        queryFn: () => apiRequest<AutoRoutingSummary>(`/api/v1/log/auto-routing/summary?days=${days}${groupID ? `&group_id=${groupID}` : ''}`),
        refetchInterval: 15 * 1000,
    });
    const data = summary.data;
    const applyOverride = useMutation({
        mutationFn: ({ groupId, grantId, expected, limit }: { groupId: number; grantId: number; expected: number; limit: number }) =>
            apiRequest(`/api/v1/group/context-override/${groupId}`, {
                method: 'POST',
                body: { channel_grant_id: grantId, expected_override: expected, context_length: limit },
            }),
        onSuccess: () => {
            toast.success(tr('contextWindowSaved'));
            void summary.refetch();
        },
        onError: (error) => toast.error(error.message),
    });
    if (summary.isLoading) return <div className="flex h-full items-center justify-center"><Loader2 className="size-5 animate-spin text-muted-foreground" /></div>;
    if (summary.isError || !data) return <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><Activity className="size-6" /><span>{tr('loadFailed')}</span></div>;
    return (
        <div className="flex h-full min-h-0 flex-col gap-3 overflow-auto">
            <div className="flex flex-wrap items-center gap-2">
                <Select value={String(groupID)} onValueChange={(value) => setGroupID(Number(value))}>
                    <SelectTrigger className="w-56 rounded-xl"><SelectValue placeholder={tr('allGroups')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="0">{tr('allGroups')}</SelectItem>
                        {groupOptions.map((group) => <SelectItem key={group.id} value={String(group.id)}>{group.name}</SelectItem>)}
                    </SelectContent>
                </Select>
                <Select value={String(days)} onValueChange={(value) => setDays(Number(value))}>
                    <SelectTrigger className="w-36 rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="1">{tr('daysOne')}</SelectItem>
                        <SelectItem value="7">{tr('daysSeven')}</SelectItem>
                        <SelectItem value="30">{tr('daysThirty')}</SelectItem>
                    </SelectContent>
                </Select>
                <span className="ml-auto text-xs text-muted-foreground">{tr('window', { requests: data.requests })} · {tr('samplesMax', { count: data.sample_limit })}</span>
            </div>
            {data.task_classified === 0 ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 rounded-2xl border border-border/50 bg-muted/20 px-6 text-center text-muted-foreground">
                    <Activity className="size-8" />
                    <p className="text-sm font-medium">{tr('empty')}</p>
                    {groupOptions.length === 0 && <p className="max-w-lg text-xs">{tr('enableFirst')}</p>}
                </div>
            ) : (
                <div className="grid gap-3 lg:grid-cols-3">
                    {[{label:tr('classified'), value:data.task_classified}, {label:tr('successRate'), value:`${(data.success_rate * 100).toFixed(1)}%`}, {label:tr('failures'), value:data.failed}].map((card) => (
                        <div key={card.label} className="rounded-2xl border border-border/60 bg-card p-4">
                            <p className="text-xs text-muted-foreground">{card.label}</p>
                            <p className="mt-1 text-2xl font-semibold">{card.value}</p>
                        </div>
                    ))}
                </div>
            )}
            {data.tasks.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-3 text-sm font-semibold">{tr('tasksTitle')}</h3>
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.tasks.map((task) => (
                            <div key={task.task} className="rounded-xl border border-border/40 bg-muted/20 p-3">
                                <div className="flex items-center justify-between"><span className="text-sm font-medium">{tr('task.' + task.task)}</span><span className="text-xs text-muted-foreground">{tr('requestCount', { count: task.requests })}</span></div>
                                <div className="mt-2 grid grid-cols-2 gap-2 text-xs"><span>{tr('rate', { value: (task.success_rate * 100).toFixed(1) + '%' })}</span><span>{tr('latency', { value: Math.round(task.avg_duration_ms) + ' ms' })}</span><span>{tr('tailLatency', { value: Math.round(task.p90_duration_ms) + ' ms' })}</span><span>{tr('failedShort', { count: task.failed })}</span></div>
                                {task.first_token_samples > 0 && (
                                    <div className="mt-2 grid grid-cols-2 gap-2 text-xs">
                                        <span>{tr('firstTokenLatency', { value: Math.round(task.avg_first_token_ms) + ' ms' })}</span>
                                        <span>{tr('firstTokenTail', { value: Math.round(task.p90_first_token_ms) + ' ms', samples: task.first_token_samples })}</span>
                                        <span className={task.stream_incomplete_rate > 0.05 ? 'text-destructive' : undefined}>{tr('streamIncomplete', { value: (task.stream_incomplete_rate * 100).toFixed(1) + '%', count: task.stream_failed_after_first + task.stream_canceled_after_first })}</span>
                                    </div>
                                )}
                                <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-emerald-500" style={{ width: `${Math.min(100, task.success_rate * 100)}%` }} /></div>
                            </div>
                        ))}
                    </div>
                </section>
            )}
            {data.readiness.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-1 text-sm font-semibold">{tr('readinessTitle')}</h3>
                    <p className="mb-3 text-xs leading-relaxed text-muted-foreground">{tr('readinessHint')}</p>
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.readiness.map((item) => (
                            <div key={item.task} className="rounded-xl border border-border/40 bg-muted/20 p-3 text-xs">
                                <div className="flex items-start justify-between gap-2">
                                    <span className="font-medium">{tr('task.' + item.task)}</span>
                                    <span className={
                                        item.state === 'scale_active' || item.state === 'ready_for_active'
                                            ? 'rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-300'
                                            : item.state === 'pause_active' || item.state === 'investigate_prediction'
                                                ? 'rounded-full bg-destructive/10 px-2 py-0.5 text-[10px] text-destructive'
                                                : 'rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground'
                                    }>
                                        {tr(`readinessState.${item.state}`)}
                                    </span>
                                </div>
                                <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1">
                                    <span>{tr('readinessShadow', { compared: item.shadow_compared, total: item.shadow_requests })}</span>
                                    {item.admin_multi_user_evidence
                                        ? <span>{tr('readinessEvidence', { users: item.evidence_users, buckets: item.evidence_buckets, required: item.required_buckets })}</span>
                                        : <span>{tr('readinessUserEvidence', { buckets: item.evidence_buckets, required: item.required_buckets })}</span>}
                                    <span>{tr('readinessAgreement', { value: `${(item.prediction_agreement * 100).toFixed(1)}%` })}</span>
                                    {item.shadow_compared > 0 && <span>{tr('readinessAgreementLower', { level: item.confidence_level_percent, value: `${(item.prediction_agreement_lower * 100).toFixed(1)}%` })}</span>}
                                    <span>{tr('readinessCoverage', { value: `${(item.shadow_coverage * 100).toFixed(1)}%` })}</span>
                                    <span>{tr('readinessCandidates', { verified: item.verified_candidates, total: item.configured_candidates })}</span>
                                    {item.anthropic_stream_candidates > 0 && <span>{tr('readinessAnthropicStream', { verified: item.anthropic_stream_verified, total: item.anthropic_stream_candidates })}</span>}
                                    {item.active_requests > 0 && <span>{tr('readinessActive', { count: item.active_requests, value: `${(item.active_success_rate * 100).toFixed(1)}%` })}</span>}
                                    {item.active_requests > 0 && <span>{tr('readinessActiveLower', { level: item.confidence_level_percent, value: `${(item.active_success_lower * 100).toFixed(1)}%` })}</span>}
                                    {item.cost_samples > 0 && <span>{tr('readinessCost', { samples: item.cost_samples, value: `${item.cost_ratio_p90_percent.toFixed(1)}%` })}</span>}
                                    {item.model_teams > 0 && <span>{tr('readinessModels', { count: item.model_teams, value: `${(item.model_min_success_rate * 100).toFixed(1)}%` })}</span>}
                                    {item.first_token_samples > 0 && <span>{tr('readinessFirstToken', { value: `${item.first_token_p90_ms.toFixed(0)} ms`, samples: item.first_token_samples })}</span>}
                                    {item.stream_started > 0 && <span>{tr('readinessStream', { value: `${(item.stream_incomplete_rate * 100).toFixed(1)}%`, count: item.stream_incomplete })}</span>}
                                </div>
                                {item.recommended_ramp_percent > 0 && (
                                    <p className="mt-2 font-medium text-foreground">{tr('readinessRamp', { value: item.recommended_ramp_percent })}</p>
                                )}
                                {item.reasons.length > 0 && (
                                    <p className="mt-1 leading-relaxed text-muted-foreground">{item.reasons.map((reason) => tr('readinessReason.' + reason)).join(' · ')}</p>
                                )}
                                {item.model_risks.length > 0 && (
                                    <div className="mt-3 border-t border-border/50 pt-3">
                                        <div className="mb-2 flex items-center justify-between gap-2">
                                            <span className="font-medium">{tr('modelRiskTitle')}</span>
                                            <span className="text-muted-foreground">{tr('modelRiskEligible', { eligible: item.model_eligible_teams, total: item.model_teams })}</span>
                                        </div>
                                        <div className="space-y-2">
                                            {item.model_risks.map((risk) => (
                                                <div key={`${risk.task}-${risk.model}`} className="rounded-lg border border-border/40 bg-background/60 p-2">
                                                    <div className="flex items-center justify-between gap-2">
                                                        <span className="truncate font-mono text-[11px] text-foreground">{risk.model}</span>
                                                        <span className={
                                                            risk.state === 'healthy'
                                                                ? 'shrink-0 rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-700 dark:text-emerald-300'
                                                                : risk.state === 'blocked'
                                                                    ? 'shrink-0 rounded-full bg-destructive/10 px-2 py-0.5 text-[10px] text-destructive'
                                                                    : 'shrink-0 rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground'
                                                        }>
                                                            {tr(`modelRiskState.${risk.state}`)}
                                                        </span>
                                                    </div>
                                                    <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                                                        <span>{tr('modelRiskSamples', { count: risk.active_requests })}</span>
                                                        <span>{tr('rate', { value: `${(risk.success_rate * 100).toFixed(1)}%` })}</span>
                                                        {risk.failed > 0 && <span>{tr('modelRiskFailureBreakdown', {
                                                            channel: risk.channel_failures,
                                                            request: risk.request_failures,
                                                            candidate: risk.candidate_failures,
                                                        })}</span>}
                                                        {risk.success_trend !== 'insufficient' && (
                                                            <span className={risk.success_trend === 'declining' ? 'text-destructive' : undefined}>
                                                                {tr('modelRiskTrend', {
                                                                    earlier: `${(risk.earlier_success_rate * 100).toFixed(1)}%`,
                                                                    recent: `${(risk.recent_success_rate * 100).toFixed(1)}%`,
                                                                })}
                                                            </span>
                                                        )}
                                                        <span className={
                                                            risk.success_trend === 'declining'
                                                                ? 'text-destructive'
                                                                : 'text-muted-foreground'
                                                        }>
                                                            {tr(`modelRiskTrendState.${risk.success_trend}`)}
                                                        </span>
                                                        {risk.first_token_samples > 0 && <span>{tr('modelRiskFirstToken', { value: `${risk.first_token_p90_ms.toFixed(0)} ms`, samples: risk.first_token_samples })}</span>}
                                                        {risk.first_token_trend !== 'insufficient' && (
                                                            <span className={risk.first_token_trend === 'degrading' ? 'text-destructive' : undefined}>
                                                                {tr('modelRiskFirstTokenTrend', {
                                                                    earlier: `${risk.earlier_first_token_p90_ms.toFixed(0)} ms`,
                                                                    recent: `${risk.recent_first_token_p90_ms.toFixed(0)} ms`,
                                                                })}
                                                            </span>
                                                        )}
                                                        {risk.stream_started > 0 && <span className={risk.state === 'blocked' ? 'text-destructive' : undefined}>{tr('modelRiskStream', { value: `${(risk.stream_incomplete_rate * 100).toFixed(1)}%`, count: risk.stream_incomplete })}</span>}
                                                        {risk.stream_trend !== 'insufficient' && (
                                                            <span className={risk.stream_trend === 'degrading' ? 'text-destructive' : undefined}>
                                                                {tr('modelRiskStreamTrend', {
                                                                    earlier: `${(risk.earlier_stream_incomplete_rate * 100).toFixed(1)}%`,
                                                                    recent: `${(risk.recent_stream_incomplete_rate * 100).toFixed(1)}%`,
                                                                })}
                                                            </span>
                                                        )}
                                                    </div>
                                                    {risk.error_class_counts.length > 0 && (
                                                        <div className="mt-1 flex flex-wrap gap-1">
                                                            {risk.error_class_counts.map((item) => (
                                                                <span key={item.reason} className={`rounded-full px-1.5 py-0.5 text-[10px] ${autoFailureBadgeClass(item.reason)}`}>
                                                                    {tr('errorClass.' + item.reason)} · {item.count}
                                                                </span>
                                                            ))}
                                                        </div>
                                                    )}
                                                    {risk.band_risks.length > 0 && (
                                                        <div className="mt-2 border-t border-border/40 pt-2">
                                                            <div className="mb-1 text-[10px] font-medium text-muted-foreground">{tr('modelRiskBandTitle')}</div>
                                                            <div className="flex flex-wrap gap-1">
                                                                {risk.band_risks.map((band) => (
                                                                    <span
                                                                        key={`${risk.model}-${band.band}`}
                                                                        title={band.reasons.map((reason) => tr('modelRiskBandReason.' + reason)).join(' · ')}
                                                                        className={`rounded-full px-2 py-0.5 text-[10px] ${band.state === 'blocked'
                                                                            ? 'bg-destructive/10 text-destructive'
                                                                            : band.state === 'healthy'
                                                                                ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                                                                                : 'bg-muted text-muted-foreground'
                                                                            }`}
                                                                    >
                                                                        {tr('costBand.' + band.band)} · {tr('rate', { value: `${(band.success_rate * 100).toFixed(1)}%` })} · {Math.round(band.avg_prompt_tokens)} tokens
                                                                    </span>
                                                                ))}
                                                            </div>
                                                            {risk.band_risks.some((band) => band.error_class_counts.length > 0) && (
                                                                <div className="mt-1 flex flex-wrap gap-1">
                                                                    {risk.band_risks.flatMap((band) => band.error_class_counts.map((item) => (
                                                                        <span key={`${risk.model}-${band.band}-${item.reason}`} className={`rounded-full px-1.5 py-0.5 text-[10px] ${autoFailureBadgeClass(item.reason)}`}>
                                                                            {tr('costBand.' + band.band)} · {tr('errorClass.' + item.reason)} · {item.count}
                                                                        </span>
                                                                    )))}
                                                                </div>
                                                            )}
                                                        </div>
                                                    )}
                                                    {risk.reasons.length > 0 && (
                                                        <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">{risk.reasons.map((reason) => tr('modelRiskReason.' + reason)).join(' · ')}</p>
                                                    )}
                                                </div>
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </div>
                        ))}
                    </div>
                </section>
            )}
            {data.events.some((event) => event.reason.startsWith('unsupported_')) && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-2 text-sm font-semibold">{tr('unsupportedShapes')}</h3>
                    {data.events.filter((event) => event.reason.startsWith('unsupported_')).map((event) => (
                        <div key={event.reason} className="flex justify-between gap-3 py-1 text-xs">
                            <span className="font-mono">{event.reason}</span><span className="tabular-nums">{event.count}</span>
                        </div>
                    ))}
                </section>
            )}
            {(data.exclusions.length > 0 || data.events.length > 0) && (
                <div className="grid gap-3 lg:grid-cols-2">
                    <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('exclusionsTitle')}</h3><div className="space-y-1">{data.exclusions.slice(0, 10).map((item) => <div key={item.reason} className="flex justify-between gap-3 text-xs"><span className="text-muted-foreground">{routeReasonText(t, item.reason)}</span><span className="font-mono">{item.count}</span></div>)}</div></section>
                    <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('eventsTitle')}</h3><div className="space-y-1">{data.events.slice(0, 10).map((item) => <div key={item.reason} className="flex justify-between gap-3 text-xs"><span className="text-muted-foreground">{routeEventText(t, item.reason) || item.reason}</span><span className="font-mono">{item.count}</span></div>)}</div></section>
                </div>
            )}
            {data.errors.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-3 text-sm font-semibold">{tr('errorsTitle')}</h3>
                    {data.error_class_counts.length > 0 && (
                        <div className="mb-3 flex flex-wrap gap-2">
                            {data.error_class_counts.map((item) => (
                                <span key={item.reason} className="rounded-full bg-muted px-2 py-1 text-[11px] text-muted-foreground">
                                    {tr('errorClass.' + item.reason)} · {item.count}
                                </span>
                            ))}
                        </div>
                    )}
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.errors.slice(0, 12).map((item) => (
                            <div key={`${item.task}-${item.class}`} className="flex items-center justify-between gap-3 rounded-lg border border-border/40 px-3 py-2 text-xs">
                                <span>{tr('task.' + item.task)} · {tr('errorClass.' + item.class)}</span>
                                <span className="font-mono">{item.count}</span>
                            </div>
                        ))}
                    </div>
                </section>
            )}
            {data.cost_bands.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-1 text-sm font-semibold">{tr('costBandsTitle')}</h3>
                    <p className="mb-3 text-xs text-muted-foreground">{tr('costBandsHint')}</p>
                    <div className="space-y-2">
                        {data.cost_bands.map((band) => (
                            <div key={`${band.task}-${band.band}`} className="grid grid-cols-[minmax(0,1fr)_auto_auto_auto] items-center gap-3 rounded-lg border border-border/40 px-3 py-2 text-xs">
                                <span>{tr('task.' + band.task)} · {tr('costBand.' + band.band)}</span>
                                <span className="tabular-nums text-muted-foreground">{tr('requestCount', { count: band.requests })}</span>
                                <span className="tabular-nums">{tr('tokenAverage', { value: Math.round(band.avg_prompt_tokens) })}</span>
                                <span className="tabular-nums">{tr('costAverage', { avg: band.avg_cost_usd.toFixed(4), p90: band.p90_cost_usd.toFixed(4) })}</span>
                            </div>
                        ))}
                    </div>
                </section>
            )}
            {data.context_window_hints.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-1 text-sm font-semibold">{tr('contextWindowHintsTitle')}</h3>
                    <p className="mb-3 text-xs text-muted-foreground">{tr('contextWindowHintsHint')}</p>
                    <div className="space-y-2">
                        {data.context_window_hints.slice(0, 10).map((item) => {
                            const minFailed = item.observed_min_input_tokens || item.observed_max_input_tokens;
                            const range = minFailed !== item.observed_max_input_tokens
                                ? `${minFailed}–${item.observed_max_input_tokens}`
                                : `${minFailed}`;
                            const statusClass =
                                item.config_status === 'exceeds_observed_failure'
                                    ? 'bg-destructive/10 text-destructive'
                                    : item.config_status === 'review_margin'
                                        ? 'bg-amber-500/10 text-amber-700 dark:text-amber-300'
                                        : item.config_status === 'covered'
                                            ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                                            : 'bg-muted text-muted-foreground';
                            return (
                                <div key={JSON.stringify([item.group_id, item.group_item_id, item.task, item.model, item.channel_code])} className="rounded-xl border border-border/40 px-3 py-2 text-xs">
                                    <div className="flex flex-wrap items-center gap-2">
                                        <span className="font-medium">{item.channel_code || '-'}</span>
                                        <span className="font-mono">{item.model}</span>
                                        <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{tr('task.' + item.task)}</span>
                                        {item.channel_grant_id ? (
                                            <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-[10px] text-muted-foreground">
                                                {tr('contextWindowHintGrant', { id: item.channel_grant_id })}
                                            </span>
                                        ) : null}
                                        {item.config_status ? (
                                            <span className={`rounded-full px-2 py-0.5 text-[10px] ${statusClass}`}>
                                                {tr('contextWindowHintStatus.' + item.config_status)}
                                            </span>
                                        ) : null}
                                    </div>
                                    <p className="mt-1 text-muted-foreground tabular-nums">
                                        {tr('contextWindowHintObserved', {
                                            samples: item.samples,
                                            range,
                                            suggestion: item.suggested_context_length,
                                        })}
                                    </p>
                                    {Boolean(item.estimated_samples) && <p className="mt-1 text-muted-foreground">{tr('contextWindowEstimated', { count: item.estimated_samples ?? 0 })}</p>}
                                    {(item.configured_context_length || item.observed_max_success_input_tokens) ? (
                                        <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground tabular-nums">
                                            {item.configured_context_length ? (
                                                <span>
                                                    {tr(
                                                        item.has_context_override
                                                            ? 'contextWindowHintConfiguredOverride'
                                                            : 'contextWindowHintConfiguredDefault',
                                                        { tokens: item.configured_context_length }
                                                    )}
                                                </span>
                                            ) : null}
                                            {item.observed_max_success_input_tokens ? (
                                                <span>
                                                    {tr('contextWindowHintMaxSuccess', {
                                                        tokens: item.observed_max_success_input_tokens,
                                                    })}
                                                </span>
                                            ) : null}
                                        </div>
                                    ) : null}
                                    {role === 'admin' && groupID > 0 && item.channel_grant_id && item.suggested_context_length > 0 &&
                                      (item.config_status === 'exceeds_observed_failure' || item.config_status === 'review_margin') &&
                                      item.suggested_context_length >= (item.observed_max_success_input_tokens ?? 0) ? (
                                        <Button size="sm" variant="outline" className="mt-2" disabled={applyOverride.isPending} onClick={() => {
                                            const grantId = item.channel_grant_id;
                                            if (grantId && window.confirm(tr('contextWindowConfirm', {
                                                model: item.model, grant: grantId, tokens: item.suggested_context_length,
                                            }))) {
                                                applyOverride.mutate({ groupId: groupID, grantId, expected: item.has_context_override ? item.configured_context_length ?? 0 : 0, limit: item.suggested_context_length });
                                            }
                                        }}>{tr('contextWindowApply')}</Button>
                                    ) : null}
                                </div>
                            );
                        })}
                    </div>
                </section>
            )}
            {data.models.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4"><h3 className="mb-3 text-sm font-semibold">{tr('modelsTitle')}</h3><div className="space-y-2">{data.models.slice(0, 20).map((model) => <div key={`${model.task}-${model.model}`} className="grid grid-cols-[minmax(0,1fr)_auto_auto_auto] items-center gap-3 rounded-lg border border-border/40 px-3 py-2 text-xs"><span className="truncate">{tr('task.' + model.task)} · {model.model}</span><span className="tabular-nums text-muted-foreground">{tr('requestCount', { count: model.attempts })}</span><span className="tabular-nums">{tr('rate', { value: (model.success_rate * 100).toFixed(1) + '%' })}</span><span className="tabular-nums text-muted-foreground">{model.first_token_samples > 0 ? tr('firstTokenTail', { value: Math.round(model.p90_first_token_ms) + ' ms', samples: model.first_token_samples }) : tr('tailLatency', { value: Math.round(model.p90_duration_ms) + ' ms' })}</span></div>)}</div></section>
            )}
            {data.predictions.length > 0 && (
                <section className="rounded-2xl border border-border/60 bg-card p-4">
                    <h3 className="mb-3 text-sm font-semibold">{tr('predictionsTitle')}</h3>
                    <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                        {data.predictions.map((item) => <div key={item.task} className="rounded-xl border border-border/40 bg-muted/20 p-3 text-xs">
                            <div className="flex justify-between"><span className="font-medium">{tr('task.' + item.task)}</span><span>{tr('predictionSamples', { count: item.compared })}</span></div>
                            <p className="mt-2">{tr('predictionAgreement', { value: (item.same_model_rate * 100).toFixed(1) + '%' })}</p>
                            <p className="mt-1 text-muted-foreground">{tr('predictionCostDelta', { predicted: item.predicted_cost_usd.toFixed(4), actual: item.actual_cost_usd.toFixed(4), delta: item.cost_delta_usd.toFixed(4) })}</p>
                        </div>)}
                    </div>
                </section>
            )}
        </div>
    );
}

function autoFailureBadgeClass(reason: string) {
    switch (reason) {
        case 'rate':
        case 'quota':
        case 'auth':
        case 'overload':
        case 'compatibility':
        case 'timeout':
        case 'unknown':
            return 'bg-amber-500/10 text-amber-700 dark:text-amber-300';
        case 'client':
        case 'policy':
        case 'canceled':
            return 'bg-sky-500/10 text-sky-700 dark:text-sky-300';
        case 'model':
            return 'bg-destructive/10 text-destructive';
        default:
            return 'bg-muted text-muted-foreground';
    }
}

// LogHistory 渲染历史日志的分页视图: 关键字/状态筛选 + 页码导航。
function LogHistory() {
    const t = useTranslations('log.history');
    const [page, setPage] = useState(0);
    const [keyword, setKeyword] = useState('');
    const [draftKeyword, setDraftKeyword] = useState('');
    const [status, setStatus] = useState('');
    const [clientIp, setClientIp] = useState('');
    const [draftIp, setDraftIp] = useState('');
    const [model, setModel] = useState('');
    const [draftModel, setDraftModel] = useState('');
    const [channel, setChannel] = useState('');
    const [draftChannel, setDraftChannel] = useState('');

    // 筛选条件变化时重置页码, 避免停留在不存在的页。
    const offset = page * PAGE_SIZE;
    const { data, isLoading, isError, refetch, isFetching } = useLogList({
        limit: PAGE_SIZE,
        offset,
        keyword: keyword || undefined,
        status: status || undefined,
        clientIp: clientIp || undefined,
        model: model || undefined,
        channel: channel || undefined,
    });

    const items = data?.items ?? [];
    const total = data?.total ?? 0;
    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    const applyKeyword = () => {
        setKeyword(draftKeyword.trim());
        setClientIp(draftIp.trim());
        setModel(draftModel.trim());
        setChannel(draftChannel.trim());
        setPage(0);
    };

    // 导出使用与列表一致的筛选条件, 浏览器直接下载 CSV。
    // 日志导出是敏感导出, 走通用客户端(撞上 step-up 会弹密码确认并带凭证重发这一次)。
    const exportLogs = async () => {
        try {
            await downloadFile(logExportUrl({
                keyword: keyword || undefined,
                status: status || undefined,
                clientIp: clientIp || undefined,
                model: model || undefined,
                channel: channel || undefined,
            }), 'switchyard-logs.csv');
        } catch (error) {
            // 用户取消密码确认 = 请求没发出去, 不必再报错。
            if (!isStepUpRequired(error)) toast.error(error instanceof Error ? error.message : String(error));
        }
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <div className="relative min-w-0 flex-1 basis-56">
                    <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        className="rounded-xl pl-9 pr-8"
                        placeholder={t('searchPlaceholder')}
                        value={draftKeyword}
                        onChange={(e) => setDraftKeyword(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') applyKeyword();
                        }}
                    />
                    {draftKeyword && (
                        <button
                            type="button"
                            onClick={() => {
                                setDraftKeyword('');
                                setKeyword('');
                                setPage(0);
                            }}
                            className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                        >
                            <X className="size-4" />
                        </button>
                    )}
                </div>
                <Select value={status} onValueChange={(value) => { setStatus(value); setPage(0); }}>
                    <SelectTrigger className="w-36 rounded-xl">
                        <SelectValue placeholder={t('statusAll')} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="">{t('statusAll')}</SelectItem>
                        {STATUS_OPTIONS.map((option) => (
                            <SelectItem key={option} value={option}>{t('status.' + option)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Input className="w-28 rounded-xl" placeholder={t('filterIp')}
                    value={draftIp}
                    onChange={(e) => setDraftIp(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Input className="w-32 rounded-xl" placeholder={t('filterModel')}
                    value={draftModel}
                    onChange={(e) => setDraftModel(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Input className="w-32 rounded-xl" placeholder={t('filterChannel')}
                    value={draftChannel}
                    onChange={(e) => setDraftChannel(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') applyKeyword(); }} />
                <Button variant="outline" size="sm" className="rounded-xl" onClick={applyKeyword}>
                    <Search className="size-4" />
                    {t('search')}
                </Button>
                <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void exportLogs()}>
                    <Download className="size-4" />
                    {t('export')}
                </Button>
            </div>

            <div className="min-h-0 flex-1">
                {isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                ) : isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <p>{t('loadFailed')}</p>
                        <Button variant="outline" size="sm" onClick={() => void refetch()} className="rounded-xl">
                            {t('retry')}
                        </Button>
                    </div>
                ) : items.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <Logs className="size-8" />
                        <span className="text-sm">{t('empty')}</span>
                    </div>
                ) : (
                    <VirtualizedGrid
                        items={items}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={104}
                        overscan={8}
                        getItemKey={(log) => `log-${log.id}`}
                        renderItem={(log) => <LogCard log={log} />}
                    />
                )}
            </div>

            <div className="flex shrink-0 items-center justify-between gap-4">
                <span className="text-xs text-muted-foreground">
                    {t('pagination.page', { page: page + 1, total })}
                    {isFetching && <Loader2 className="ml-2 inline size-3 animate-spin" />}
                </span>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setPage((p) => Math.max(0, p - 1))}
                        disabled={page === 0}
                        className="rounded-xl"
                    >
                        <ChevronLeft className="size-4" />
                        {t('pagination.prev')}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
                        disabled={page >= totalPages - 1}
                        className="rounded-xl"
                    >
                        {t('pagination.next')}
                        <ChevronRight className="size-4" />
                    </Button>
                </div>
            </div>
        </div>
    );
}

// LogLive 渲染实时日志流视图。
function LogLive() {
    const t = useTranslations('log');
    const { logs, isLoading, error } = useLogs();

    if (isLoading) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (logs.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                {!error && <Logs className="size-8" />}
                <span className="text-sm">{error ? t('list.disconnected') : t('list.empty')}</span>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {error && (
                <div className="flex shrink-0 items-center justify-center px-1 pb-3 text-xs text-destructive">
                    <span>{t('list.disconnected')}</span>
                </div>
            )}
            <div className="min-h-0 flex-1">
                <VirtualizedGrid
                    items={logs}
                    layout="list"
                    columns={{ default: 1 }}
                    estimateItemHeight={104}
                    overscan={8}
                    getItemKey={(log) => `log-${log.id}`}
                    renderItem={(log) => <LogCard log={log} />}
                />
            </div>
        </div>
    );
}

// Log 展示进程内调用日志: 实时流视图与可查询/筛选/分页的历史视图。
export function Log() {
    const t = useTranslations('log');
    const [tab, setTab] = useState<'live' | 'history' | 'auto'>('live');

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <Tabs value={tab} onValueChange={(value) => setTab(value as 'live' | 'history' | 'auto')} className="w-auto">
                    <TabsList className="rounded-xl">
                        <TabsTrigger value="live">{t('tabs.live')}</TabsTrigger>
                        <TabsTrigger value="history">{t('tabs.history')}</TabsTrigger>
                        <TabsTrigger value="auto">{t('tabs.autoRouting')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <div className="min-h-0 flex-1">
                {tab === 'live' ? <LogLive /> : tab === 'history' ? <LogHistory /> : <AutoRoutingPanel />}
            </div>
        </div>
    );
}
