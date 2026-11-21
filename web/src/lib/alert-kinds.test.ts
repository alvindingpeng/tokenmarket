import { describe, expect, it } from 'vitest';
import { ALERT_KINDS, ALERT_KINDS_UNREGISTERED, isAlertKind, isRegisteredAlertKind } from './alert-kinds';

// GO_ALERT_KINDS 手抄自 internal/op/alert_silence.go 的 AlertValidKind 白名单(闭集)。
// 刻意手抄而不是解析 Go 源码: 解析器会把注释、格式变化都算成漂移, 手抄的清单漂移时
// 差异一眼看得出。Go 侧新增 kind 时这条测试变红 —— 那就是同步前端的提示。
const GO_ALERT_KINDS = [
    'down', 'recovered', 'degraded', 'key_disabled', 'model_disabled',
    'low_balance', 'quota_exceeded', 'mismatch', 'billing_failed',
    'backup_failed', 'archive_failed', 'watchdog_alert',
    'route_persist_failed', 'route_persist_recovered',
    'task_failed', 'task_recovered',
    'key_degraded', 'model_degraded', 'payment_amount_mismatch', 'reserve',
    'input_tokens', 'output_tokens', 'requests', 'update_available', 'test',
    'withdrawal_approve_timeout', 'payment_after_expiry',
    'fx_rate_refresh_failed',
    'usage_missing', 'account_invalidated',
    'billing_archive_failed', 'audit_archive_failed', 'fx_rate_stale',
] as const;

// GO_EMITTED_NOT_IN_CLOSED 是"发得出但没进闭集"的 kind: 每个发出点都 grep 过,
// 与闭集求差集。上一批 5 个已补进闭集, 因此现在是空的 —— 若再出现半成品 kind,
// 往这里加一项即可, 上面那条"集合相等"的断言会立刻变红提示同步。
const GO_EMITTED_NOT_IN_CLOSED: readonly string[] = [];

// GO_EMITTED_NOT_IN_CLOSED_PAST 上一批修掉的半成品 kind(task-43)。留作回归护栏:
// 它们随时不能再退回"能收到但登记不了"的状态。
const GO_EMITTED_NOT_IN_CLOSED_PAST = [
    'usage_missing',
    'account_invalidated',
    'billing_archive_failed',
    'audit_archive_failed',
    'fx_rate_stale',
] as const;

describe('ALERT_KINDS 与后端闭集一致', () => {
    it('集合与 Go 的 AlertValidKind 白名单完全相等', () => {
        expect(new Set(ALERT_KINDS)).toEqual(new Set(GO_ALERT_KINDS));
    });

    it('无重复项且每项都是非空小写蛇形命名', () => {
        expect(new Set(ALERT_KINDS).size).toBe(ALERT_KINDS.length);
        for (const kind of ALERT_KINDS) {
            expect(kind).toMatch(/^[a-z][a-z0-9_]*$/);
        }
    });

    it('已发出但未登记的 kind 集合与后端现状一致', () => {
        expect(new Set(ALERT_KINDS_UNREGISTERED)).toEqual(new Set(GO_EMITTED_NOT_IN_CLOSED));
    });

    it('两份清单不相交: 已登记的不该又出现在未登记里', () => {
        // 交集非空说明后端已经补上了闭集, 但前端忘了从 UNREGISTERED 挪走。
        const overlap = ALERT_KINDS_UNREGISTERED.filter((k) => (ALERT_KINDS as readonly string[]).includes(k));
        expect(overlap).toEqual([]);
    });

    it('历史缺陷已修复: fx_rate_refresh_failed 进了闭集', () => {
        // 这条 kind 由 fxrate.go 长期发出却没登记, 管理员建静默窗口一直被 400。
        expect(isRegisteredAlertKind('fx_rate_refresh_failed')).toBe(true);
    });

    it('两个资金安全 kind 在闭集内', () => {
        expect(isRegisteredAlertKind('withdrawal_approve_timeout')).toBe(true);
        expect(isRegisteredAlertKind('payment_after_expiry')).toBe(true);
    });

    it('上一批半成品 kind 现在既能收到也能登记了', () => {
        // 这 5 个此前"发得出但登记不全": 列表里能收到, 建静默窗口却被 400 拒。
        for (const kind of GO_EMITTED_NOT_IN_CLOSED_PAST) {
            expect(isAlertKind(kind)).toBe(true);
            expect(isRegisteredAlertKind(kind)).toBe(true);
        }
    });

    it('两个判定函数都不认非法值', () => {
        for (const bad of ['not_a_kind', 'Down', 'usage_missing ', '', 'usage-missing']) {
            expect(isAlertKind(bad)).toBe(false);
            expect(isRegisteredAlertKind(bad)).toBe(false);
        }
    });
});