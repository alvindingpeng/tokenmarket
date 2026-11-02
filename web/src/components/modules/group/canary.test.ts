import { describe, expect, it } from 'vitest';
import type { GroupRelayConfig } from '@/api/group';
import {
    canaryActive,
    canaryChanged,
    canaryDefaults,
    canarySubmitBlocked,
    canaryOf,
    canarySubmitConfig,
    formatCanaryUserIDs,
    parseCanaryUserIDs,
    preserveCanary,
} from './canary';

// 灰度是叠加层, 这些纯函数决定了"默认关闭""切换模式不丢值""非法输入不落库"三件事, 单独钉住。

const base = (patch: Partial<GroupRelayConfig> = {}): GroupRelayConfig => ({
    request_timeout_seconds: 600,
    max_wait_seconds: 30,
    max_waiting_requests: 0,
    max_total_attempts: 10,
    member_stream_idle_timeout_seconds: 60,
    member_max_attempts: 1,
    member_retry_interval_seconds: 1,
    member_non_stream_response_timeout_seconds: 120,
    member_stream_first_event_timeout_seconds: 30,
    member_cooldown_seconds: 60,
    member_affinity_seconds: 0,
    price_metric: 'blended',
    metric_window_size: 20,
    score_price_weight: 0,
    score_latency_weight: 0,
    score_success_weight: 0,
    reliability_floor: 0,
    canary_item_id: 0,
    canary_percent: 0,
    canary_user_ids: [],
    canary_per_request: false,
    ...patch,
});

describe('canary 默认值', () => {
    it('未配置时全关: 成员 0、比例 0、用户为空、每请求随机关闭', () => {
        expect(canaryDefaults()).toEqual({
            canary_item_id: 0,
            canary_percent: 0,
            canary_user_ids: [],
            canary_per_request: false,
        });
    });

    it('canaryOf 把缺失字段补成关闭态, 不改成 truthy', () => {
        expect(canaryOf({ canary_item_id: 7 })).toEqual({
            canary_item_id: 7,
            canary_percent: 0,
            canary_user_ids: [],
            canary_per_request: false,
        });
        expect(canaryOf(undefined)).toEqual(canaryDefaults());
    });

    it('成员 0 或比例 0 都不算生效, 两者齐备才算', () => {
        expect(canaryActive(canaryDefaults())).toBe(false);
        expect(canaryActive({ canary_item_id: 3, canary_percent: 0, canary_user_ids: [], canary_per_request: false })).toBe(false);
        expect(canaryActive({ canary_item_id: 0, canary_percent: 50, canary_user_ids: [], canary_per_request: false })).toBe(false);
        expect(canaryActive({ canary_item_id: 3, canary_percent: 50, canary_user_ids: [], canary_per_request: false })).toBe(true);
    });
});

describe('灰度用户 ID 解析', () => {
    it('逗号、空格、中文逗号与顿号都当分隔符, 重复项只留一次', () => {
        expect(parseCanaryUserIDs('12, 34 56，78、12')).toEqual({ ids: [12, 34, 56, 78], invalid: [] });
    });

    it('空输入解析成空列表, 不算非法', () => {
        expect(parseCanaryUserIDs('   ')).toEqual({ ids: [], invalid: [] });
    });

    it('非整数、零与负数都算非法并原样回传, 让用户看见自己输错的是什么', () => {
        expect(parseCanaryUserIDs('12 abc -3 4.5 0')).toEqual({ ids: [12], invalid: ['abc', '-3', '4.5', '0'] });
    });

    it('format 与 parse 互为逆运算', () => {
        const ids = [12, 34, 56];
        expect(parseCanaryUserIDs(formatCanaryUserIDs(ids)).ids).toEqual(ids);
    });

    it('提交时只用输入框的解析结果, 不会把半截输入存进去', () => {
        const config = base({ canary_item_id: 9, canary_percent: 20, canary_user_ids: [1] });
        expect(canarySubmitConfig(config, '12, 34').canary_user_ids).toEqual([12, 34]);
        expect(canarySubmitConfig(config, '12 abc').canary_user_ids).toEqual([12]);
        // 其他字段原样保留, 灰度只替代用户列表。
        expect(canarySubmitConfig(config, '12').canary_item_id).toBe(9);
        expect(canarySubmitConfig(config, '12').canary_percent).toBe(20);
    });
});

describe('非法输入挡住保存', () => {
    it('非手动模式下有非法用户 ID 就不让保存', () => {
        expect(canarySubmitBlocked('failover', '12 abc')).toBe(true);
        expect(canarySubmitBlocked('price', '0')).toBe(true);
        expect(canarySubmitBlocked('failover', '12, 34')).toBe(false);
        expect(canarySubmitBlocked('failover', '')).toBe(false);
    });

    it('手动模式下这一块根本不存在, 残留的非法文本也不该挡住保存', () => {
        expect(canarySubmitBlocked('manual', '12 abc')).toBe(false);
    });
});

describe('切换模式保留灰度', () => {
    it('preserveCanary 用旧配置盖住新预设, 灰度四项不变而模式参数取新值', () => {
        const previous = base({ canary_item_id: 5, canary_percent: 30, canary_user_ids: [7], canary_per_request: true, metric_window_size: 20 });
        const next = base({ metric_window_size: 10, canary_item_id: 0, canary_percent: 0, canary_user_ids: [], canary_per_request: false });
        const merged = preserveCanary(next, previous);
        expect(merged).toMatchObject({
            metric_window_size: 10,
            canary_item_id: 5,
            canary_percent: 30,
            canary_user_ids: [7],
            canary_per_request: true,
        });
    });

    it('canaryChanged 只看灰度四项, 顺序不同不算改动', () => {
        const saved = base({ canary_item_id: 5, canary_percent: 30, canary_user_ids: [7, 8] });
        expect(canaryChanged(saved, saved)).toBe(false);
        expect(canaryChanged(base({ canary_user_ids: [8, 7], canary_item_id: 5, canary_percent: 30 }), saved)).toBe(false);
        expect(canaryChanged(base({ ...canaryOf(saved), canary_percent: 31 }), saved)).toBe(true);
        expect(canaryChanged(base({ ...canaryOf(saved), canary_per_request: true }), saved)).toBe(true);
        expect(canaryChanged(base({ ...canaryOf(saved), canary_item_id: 6 }), saved)).toBe(true);
        expect(canaryChanged(base({ ...canaryOf(saved), canary_user_ids: [7] }), saved)).toBe(true);
        // 非灰度字段不同不算灰度改动, 由各自的比较负责。
        expect(canaryChanged(base({ ...canaryOf(saved), metric_window_size: 30 }), saved)).toBe(false);
    });
});
