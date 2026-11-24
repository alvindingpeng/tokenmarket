// 选路解释的文案解析。
//
// 背景(真实缺陷): 日志页此前一律 `t('routeReason.' + reason)`, 但这几个 key 从来没被建过,
// 于是界面上印的是 'routeReason.strategy_price' 这样的原始 key —— 功能写着, 用户看不懂。
// 更麻烦的是 reason 有两类: 固定枚举与运行时拼的 `egress:<出口名>` / 错误分类规则名。
// 后者不该硬翻: 出口名本身就是内容, 塞进翻译表只会得到错位的译文。

/** 决策 reason 与候选排除原因中, 有固定文案的那部分。新增 reason 时必须同步这里, 否则界面退化成原始串。 */
const ROUTE_REASON_KEYS = [
    'strategy_price', 'strategy_latency', 'strategy_success', 'strategy_score',
    'strategy_random', 'strategy_failover', 'strategy_manual',
    'canary_rollout', 'recovery_probe', 'cold_start_explore', 'affinity',
    'eligible', 'selected', 'unavailable', 'rate_or_concurrency_limit',
    'not_manual_target', 'cooldown',
    'auto_general', 'auto_summary', 'auto_coding', 'auto_tool', 'auto_extraction',
    'auto_unsupported', 'auto_not_qualified', 'auto_member_unavailable',
    'auto_protocol_incompatible', 'auto_channel_incompatible',
    'auto_context_unknown', 'auto_token_limit',
    'auto_tool_unverified', 'auto_ramp_hold', 'auto_circuit_open',
    'auto_quality_unverified', 'auto_quality_below_threshold', 'auto_eligible',
    'auto_sticky_hit', 'auto_sticky_miss', 'auto_fallback_no_candidate',
    'auto_explore', 'auto_sticky', 'auto_sticky_fallback', 'auto_reject',
    'active_selected', 'fallback_selected', 'rejected', 'explored',
    'sticky_hit', 'sticky_miss',
] as const;

/** 路由事件的阶段名。 */
const ROUTE_PHASE_KEYS = ['received', 'selection', 'waiting', 'admission', 'dispatch', 'error_class', 'auto_shadow', 'auto_routing', 'finished'] as const;

/** 路由事件的原因里, 有固定文案的那部分。 */
const ROUTE_EVENT_KEYS = [
    'request_received', 'waiting_for_upstream', 'canary_unavailable',
    'upstream_rate_limited', 'upstream_admitted', 'user_admitted', 'success',
] as const;

const reasonSet: ReadonlySet<string> = new Set(ROUTE_REASON_KEYS);
const phaseSet: ReadonlySet<string> = new Set(ROUTE_PHASE_KEYS);
const eventSet: ReadonlySet<string> = new Set(ROUTE_EVENT_KEYS);

/**
 * routeReasonText 把决策 reason 或候选排除原因翻成一句人话。
 *
 * 未收录的取值原样返回: 宁可露出内部代号, 也不要给出一个错位的翻译。
 */
type Translate = (key: string, values?: Record<string, string | number>) => string;

export function routeReasonText(t: Translate, reason: string | undefined): string {
    if (!reason) return '-';
    if (reasonSet.has(reason)) return t('routeReason.' + reason);
    if (reason.startsWith('egress:')) return t('eventReason.egress') + ' ' + reason.slice('egress:'.length);
    return reason;
}

/** routePhaseText 翻路由事件的阶段名; 未收录的原样返回。 */
export function routePhaseText(t: Translate, phase: string | undefined): string {
    if (!phase) return '-';
    if (phaseSet.has(phase)) return t('eventPhase.' + phase);
    return phase;
}

/** routeEventText 翻路由事件的原因; 错误分类规则名等动态值原样返回。 */
export function routeEventText(t: Translate, reason: string | undefined): string {
    if (!reason) return '-';
    // 决策 reason 也可能出现在事件里(strategy_*), 与决策共用同一张表。
    if (reasonSet.has(reason)) return t('routeReason.' + reason);
    if (eventSet.has(reason)) return t('eventReason.' + reason);
    if (reasonSet.has(reason)) return t('routeReason.' + reason);
    const autoShadow = /^(general|summary|coding|tool|extraction|unsupported):(item|no_candidate|forced)/.exec(reason);
    if (autoShadow) {
        const task = t('routeReason.auto_' + autoShadow[1]);
        if (reason.endsWith(':no_candidate')) return t('eventReason.auto_shadow_no_candidate', { task });
        if (reason.endsWith(':forced')) return t('eventReason.auto_forced', { task });
        return t('eventReason.auto_shadow_prediction', { task, id: reason.slice(reason.lastIndexOf('=') + 1) });
    }
    return reason;
}
/** 分组选路模式中, 有固定文案的那部分。 */
const ROUTE_MODE_KEYS: ReadonlySet<string> = new Set(['manual', 'failover', 'price', 'latency', 'success', 'score', 'random']);

/** routeModeText 翻分组选路模式; 未收录的原样返回。 */
export function routeModeText(t: Translate, mode: string | undefined): string {
    if (!mode) return '-';
    return ROUTE_MODE_KEYS.has(mode) ? t('mode.' + mode) : mode;
}
