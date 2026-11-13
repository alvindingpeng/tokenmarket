import { describe, expect, it } from 'vitest';
import { createTranslator } from 'use-intl';
import en from '@/locales/en.json';
import { routeEventText, routeModeText, routePhaseText, routeReasonText } from './reason';

// 这组测试锁的是一个真实缺陷: 日志页曾经一律 `t('routeReason.' + reason)`, 而这些 key 从来没建过,
// 界面上印的是 'routeReason.strategy_price' 这种原始 key。功能写着, 用户看不懂 —— 比没做还糟。
const t = createTranslator({ locale: 'en', messages: en, namespace: 'log' }) as unknown as (key: string) => string;

describe('选路解释文案解析', () => {
    it('固定枚举值翻成人话', () => {
        expect(routeReasonText(t, 'strategy_price')).toBe('Cheapest of the eligible members');
        expect(routeReasonText(t, 'strategy_score')).toBe('Best weighted score');
        expect(routeReasonText(t, 'cold_start_explore')).toContain('Cold-start');
    });

    it('候选被排除的原因也能翻', () => {
        // 候选排除原因与决策原因共用一张表: 用户看到某渠道没被选中, 要知道是冷却还是不可用。
        expect(routeReasonText(t, 'cooldown')).toBe('Cooling down after failures');
        expect(routeReasonText(t, 'unavailable')).toContain('Unavailable');
    });

    it('动态串不被硬翻, 出口名原样保留', () => {
        // `egress:<出口名>` 是运行时拼的: 出口名本身就是内容。硬塞进翻译表只会得到错位译文,
        // 而完全原样输出又会印出 'egress:proxy-a' 这种内部标识。
        const text = routeReasonText(t, 'egress:proxy-a');
        expect(text).toContain('proxy-a');
        expect(text).toContain('Egress');
        expect(text).not.toContain('routeReason');
    });

    it('未收录的取值原样返回, 不印出翻译 key', () => {
        // 错误分类规则名是运营自定义的, 不可能预先收录。
        expect(routeReasonText(t, 'some_custom_rule')).toBe('some_custom_rule');
        expect(routeReasonText(t, undefined)).toBe('-');
    });

    it('阶段名与模式名都有文案', () => {
        expect(routePhaseText(t, 'selection')).toBe('Selection');
        expect(routePhaseText(t, 'unknown_phase')).toBe('unknown_phase');
        expect(routeModeText(t, 'score')).toBe('Weighted score');
        expect(routeModeText(t, 'failover')).toBe('Failover');
    });

    it('事件原因会分辨决策词表与事件词表', () => {
        // 同一个 reason 字段在事件里可能是 strategy_*, 也可能是事件专有词 —— 两张表都要认。
        expect(routeEventText(t, 'strategy_latency')).toBe('Fastest of the eligible members');
        expect(routeEventText(t, 'waiting_for_upstream')).toContain('Waiting');
    });
});
// 词表与真实数据的一致性: 生产日志里出现过的 reason 必须都有文案, 否则界面会退回内部代号。
// 这一条不是凑数 —— 第一次查生产数据时就发现 user_admitted 与 success 两个值没被收录,
// 而它们在每一个成功请求里都会出现。
describe('选路解释词表与生产数据一致', () => {
    it('生产日志实际出现的 reason 都有文案', () => {
        // 取值来自生产副本 relay_request_attempts / route_events 的实际样本。
        const observed = ['request_received', 'user_admitted', 'strategy_score', 'upstream_admitted', 'success', 'strategy_price'];
        for (const reason of observed) {
            const text = routeEventText(t, reason);
            expect(text, reason).not.toBe(reason);
        }
    });
});
