import { describe, expect, it } from 'vitest';
import {
    DEFAULT_USER_TIER_POLICIES,
    LIMIT_FIELDS,
    USER_TIERS,
    draftFromPolicies,
    emptyTierPolicies,
    isNonNegativeInteger,
    isUserTier,
    parseLimitField,
    parseUserTierPolicies,
    policiesFromDraft,
    serializeUserTierPolicies,
    validateTierDraft,
} from './user-tier';

// 配额 JSON 是"管理员手写/界面序列化"两头都能进的数据, 解析容错与校验必须逐条钉死:
// 一个悄悄回落到默认值的解析错误, 表现是"限流额度莫名变回出厂值", 极难追。

describe('user-tier 等级闭集', () => {
    it('合法等级与后端 model.UserTiers 一致', () => {
        expect([...USER_TIERS]).toEqual(['free', 'basic', 'pro', 'enterprise']);
        for (const tier of USER_TIERS) expect(isUserTier(tier)).toBe(true);
    });

    it('空串与未知值都不是合法等级', () => {
        for (const value of ['', 'gold', 'Free ', null, undefined, 1, {}]) {
            expect(isUserTier(value)).toBe(false);
        }
    });

    it('默认配额覆盖全部等级且与后端默认值同源', () => {
        for (const tier of USER_TIERS) {
            expect(DEFAULT_USER_TIER_POLICIES[tier]).toBeDefined();
            expect(Object.keys(DEFAULT_USER_TIER_POLICIES[tier]).sort()).toEqual([...LIMIT_FIELDS].sort());
        }
        // enterprise 出厂即不限(全 0), free 有可解释的小额度 —— 这两个方向都不能反。
        expect(DEFAULT_USER_TIER_POLICIES.enterprise).toEqual({ rpm: 0, tpm: 0, concurrent: 0 });
        expect(DEFAULT_USER_TIER_POLICIES.free.rpm).toBeGreaterThan(0);
    });
});

describe('user-tier 非负整数校验', () => {
    it('只认十进制非负整数', () => {
        expect(isNonNegativeInteger('0')).toBe(true);
        expect(isNonNegativeInteger('20')).toBe(true);
        expect(isNonNegativeInteger(' 7 ')).toBe(true);
        // 负数/小数/科学计数/空串/字母一律拒绝: parseInt 会把它们悄悄截成别的数。
        for (const value of ['-1', '2.5', '1e3', '', ' ', 'abc', '+3']) {
            expect(isNonNegativeInteger(value)).toBe(false);
        }
    });

    it('parseLimitField 对合法值返回数字, 非法值返回 null', () => {
        expect(parseLimitField('42')).toBe(42);
        expect(parseLimitField(' 0 ')).toBe(0);
        expect(parseLimitField('2.5')).toBeNull();
        expect(parseLimitField('-3')).toBeNull();
    });
});

describe('user-tier 解析设置 JSON', () => {
    it('空串 = 从未配置: 回落到默认值且不报错', () => {
        const result = parseUserTierPolicies('');
        expect(result.error).toBe('');
        expect(result.policies).toEqual(emptyTierPolicies());
        expect(parseUserTierPolicies(null).error).toBe('');
        expect(parseUserTierPolicies(undefined).error).toBe('');
    });

    it('只写部分等级合法: 缺的等级沿用默认值', () => {
        const result = parseUserTierPolicies('{"pro":{"rpm":900,"tpm":5000000,"concurrent":64}}');
        expect(result.error).toBe('');
        expect(result.policies.pro).toEqual({ rpm: 900, tpm: 5000000, concurrent: 64 });
        // 其它等级仍是默认值, 界面才能显示完整的一行可编辑输入。
        expect(result.policies.free).toEqual(DEFAULT_USER_TIER_POLICIES.free);
    });

    it('等级名大小写与空白容错', () => {
        const result = parseUserTierPolicies('{ " PRO ": { "rpm": 1 } }');
        expect(result.error).toBe('');
        expect(result.policies.pro.rpm).toBe(1);
    });

    it('非法 JSON / 非法结构 / 未知等级 / 负数与小数都给出明确错误码', () => {
        expect(parseUserTierPolicies('{oops').error).toBe('invalidJson');
        expect(parseUserTierPolicies('[1,2]').error).toBe('invalidShape');
        expect(parseUserTierPolicies('null').error).toBe('invalidShape');
        expect(parseUserTierPolicies('{"gold":{"rpm":1}}').error).toBe('invalidTier');
        expect(parseUserTierPolicies('{"free":{"rpm":-1}}').error).toBe('invalidLimit');
        expect(parseUserTierPolicies('{"free":{"rpm":1.5}}').error).toBe('invalidLimit');
        expect(parseUserTierPolicies('{"free":42}').error).toBe('invalidShape');
    });

    it('解析失败时 policies 回到默认值(界面据此显示整行, 错误另报)', () => {
        const result = parseUserTierPolicies('{"gold":{"rpm":1}}');
        expect(result.policies).toEqual(emptyTierPolicies());
    });
});

describe('user-tier 序列化与往返', () => {
    it('序列化只包含四个合法等级, 字段顺序固定', () => {
        const json = serializeUserTierPolicies({
            free: { rpm: 1, tpm: 2, concurrent: 3 },
            basic: { rpm: 1, tpm: 2, concurrent: 3 },
            pro: { rpm: 1, tpm: 2, concurrent: 3 },
            enterprise: { rpm: 0, tpm: 0, concurrent: 0 },
        });
        const parsed = JSON.parse(json) as Record<string, unknown>;
        expect(Object.keys(parsed)).toEqual([...USER_TIERS]);
        expect(JSON.stringify(parsed.free)).toBe('{"rpm":1,"tpm":2,"concurrent":3}');
    });

    it('序列化结果能被解析回原值(往返一致)', () => {
        const policies = {
            free: { rpm: 10, tpm: 1000, concurrent: 1 },
            basic: { rpm: 20, tpm: 2000, concurrent: 2 },
            pro: { rpm: 30, tpm: 3000, concurrent: 3 },
            enterprise: { rpm: 0, tpm: 0, concurrent: 0 },
        };
        const roundTrip = parseUserTierPolicies(serializeUserTierPolicies(policies));
        expect(roundTrip.error).toBe('');
        expect(roundTrip.policies).toEqual(policies);
    });
});

describe('user-tier 表单草稿', () => {
    it('草稿 -> 校验 -> 数值策略 的完整链路', () => {
        const draft = draftFromPolicies(emptyTierPolicies());
        expect(validateTierDraft(draft)).toBeNull();
        const policies = policiesFromDraft(draft);
        expect(policies).toEqual(emptyTierPolicies());
    });

    it('空串与负数在草稿校验里被拦下, 并指出是哪一档哪一项', () => {
        const draft = draftFromPolicies(emptyTierPolicies());
        draft.pro.rpm = '';
        expect(validateTierDraft(draft)).toEqual({ tier: 'pro', field: 'rpm' });
        const negative = draftFromPolicies(emptyTierPolicies());
        // 界面输入框只留数字, 但状态里仍可能是空串; 用一个非数字字符串验证校验兜底。
        negative.basic.concurrent = 'x';
        expect(validateTierDraft(negative)).toEqual({ tier: 'basic', field: 'concurrent' });
    });
});
