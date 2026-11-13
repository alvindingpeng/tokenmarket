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
] as const;

/** 路由事件的阶段名。 */
const ROUTE_PHASE_KEYS = ['received', 'selection', 'waiting', 'admission', 'dispatch', 'error_class'] as const;

/** 路由事件的原因里, 有固定文案的那部分。 */
const ROUTE_EVENT_KEYS = [
    'request_received', 'waiting_for_upstream', 'canary_unavailable',
    'upstream_rate_limited', 'upstream_admitted',
] as const;

const reasonSet: ReadonlySet<string> = new Set(ROUTE_REASON_KEYS);
const phaseSet: ReadonlySet<string> = new Set(ROUTE_PHASE_KEYS);
const eventSet: ReadonlySet<string> = new Set(ROUTE_EVENT_KEYS);

/**
 * routeReasonText 把决策 reason 或候选排除原因翻成一句人话。
 *
 * 未收录的取值原样返回: 宁可露出内部代号, 也不要给出一个错位的翻译。
 */
export function routeReasonText(t: (key: string) => string, reason: string | undefined): string {
    if (!reason) return '-';
    if (reasonSet.has(reason)) return t('routeReason.' + reason);
    if (reason.startsWith('egress:')) return t('eventReason.egress') + ' ' + reason.slice('egress:'.length);
    return reason;
}

/** routePhaseText 翻路由事件的阶段名; 未收录的原样返回。 */
export function routePhaseText(t: (key: string) => string, phase: string | undefined): string {
    if (!phase) return '-';
    if (phaseSet.has(phase)) return t('eventPhase.' + phase);
    return phase;
}

/** routeEventText 翻路由事件的原因; 错误分类规则名等动态值原样返回。 */
export function routeEventText(t: (key: string) => string, reason: string | undefined): string {
    if (!reason) return '-';
    // 决策 reason 也可能出现在事件里(strategy_*), 与决策共用同一张表。
    if (reasonSet.has(reason)) return t('routeReason.' + reason);
    if (eventSet.has(reason)) return t('eventReason.' + reason);
    return reason;
}
/** 分组选路模式中, 有固定文案的那部分。 */
const ROUTE_MODE_KEYS: ReadonlySet<string> = new Set(['manual', 'failover', 'price', 'latency', 'success', 'score', 'random']);

/** routeModeText 翻分组选路模式; 未收录的原样返回。 */
export function routeModeText(t: (key: string) => string, mode: string | undefined): string {
    if (!mode) return '-';
    return ROUTE_MODE_KEYS.has(mode) ? t('mode.' + mode) : mode;
}