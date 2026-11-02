// 用户服务等级与配额策略的纯逻辑层。
//
// 为什么单独抽一个文件: 等级闭集、默认配额、JSON 解析/校验三处都要用(用户管理表单、
// 设置页配额编辑、单测), 写在组件里就没法在不起 React 的情况下验证 —
// 而"负数/浮点/非法键"这类输入恰恰是最容易在界面上被悄悄吞掉的问题。
//
// 字段与后端 model/user_tier.go 严格对齐:
//   tier: free | basic | pro | enterprise(空串=未设置, 后端按 free 兜底)
//   limits: {rpm, tpm, concurrent}, 单位分别是 每分钟请求数 / 每分钟 token 数 / 并发数,
//           0 表示该维度不限制(不是"禁用")。

// USER_TIERS 合法等级, 顺序即界面展示顺序(由低到高), 与 model.UserTiers 一致。
export const USER_TIERS = ['free', 'basic', 'pro', 'enterprise'] as const;

// UserTier 服务等级标识。
export type UserTier = (typeof USER_TIERS)[number];

// isUserTier 判断外部值(接口回包/历史设置)是否是合法等级。
export function isUserTier(value: unknown): value is UserTier {
    return typeof value === 'string' && (USER_TIERS as readonly string[]).includes(value);
}

// TierLimits 单个等级的限流额度; 0 = 不限制。
export type TierLimits = {
    rpm: number;
    tpm: number;
    concurrent: number;
};

// LIMIT_FIELDS 三个数值维度, 表单与序列化共用同一顺序, 避免两处漂移。
export const LIMIT_FIELDS = ['rpm', 'tpm', 'concurrent'] as const;
export type LimitField = (typeof LIMIT_FIELDS)[number];

// UserTierPolicies 各等级配额; 允许只写部分等级(缺的等级视为未配置, 由后端跳过兜底)。
export type UserTierPolicies = Partial<Record<UserTier, TierLimits>>;

// DEFAULT_USER_TIER_POLICIES 与后端 model.DefaultUserTierPolicies 同值。
// 界面首次打开(设置键还是空串)时用它兜底, 让管理员看到一组可解释的起点,
// 而不是一片空白输入框 —— 空白会让人误以为"要不要自己从零编一套"。
export const DEFAULT_USER_TIER_POLICIES: Record<UserTier, TierLimits> = {
    free: { rpm: 20, tpm: 40000, concurrent: 2 },
    basic: { rpm: 120, tpm: 400000, concurrent: 8 },
    pro: { rpm: 600, tpm: 2000000, concurrent: 32 },
    enterprise: { rpm: 0, tpm: 0, concurrent: 0 },
};

// isNonNegativeInteger 判断字符串是否是"非负整数"。
// 只认十进制整数: 2.5 / -1 / "1e3" 一律拒绝 —— 配额是计数, 小数没有语义,
// 而 parseInt 会把 "2.5" 悄悄截成 2, 让管理员以为改成了别的值。
export function isNonNegativeInteger(value: string): boolean {
    return /^\d+$/.test(value.trim());
}

// parseLimitField 把输入框文本转成数值; 非法值返回 null 由调用方拦截。
export function parseLimitField(value: string): number | null {
    if (!isNonNegativeInteger(value)) return null;
    return Number.parseInt(value.trim(), 10);
}

// EMPTY_TIER_POLICIES 以默认值为起点构造全部等级的草稿(表单可编辑)。
export function emptyTierPolicies(): Record<UserTier, TierLimits> {
    const next = {} as Record<UserTier, TierLimits>;
    for (const tier of USER_TIERS) {
        const limits = DEFAULT_USER_TIER_POLICIES[tier];
        next[tier] = { ...limits };
    }
    return next;
}

// ParseResult 解析结果: error 非空表示原始 JSON 不可用, 此时 policies 为默认值。
export type ParseResult = {
    policies: Record<UserTier, TierLimits>;
    // error 是给界面看的"为什么这段设置不可信", 空串表示解析成功或原始值为空。
    error: 'invalidJson' | 'invalidShape' | 'invalidTier' | 'invalidLimit' | '';
};

// parseUserTierPolicies 解析设置里的配额 JSON。
// 空串 = 从未配置 -> 返回默认值且不报错(这是正常的初始状态, 不是错误)。
// 非法 JSON / 非法结构 / 未知等级 / 负数与非整数一律给出明确的 error 标记:
// 直接静默回落到默认值会让管理员以为"保存的那套配额还生效着"。
export function parseUserTierPolicies(raw: string | null | undefined): ParseResult {
    const trimmed = (raw ?? '').trim();
    if (trimmed === '') {
        return { policies: emptyTierPolicies(), error: '' };
    }
    let parsed: unknown;
    try {
        parsed = JSON.parse(trimmed);
    } catch {
        return { policies: emptyTierPolicies(), error: 'invalidJson' };
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return { policies: emptyTierPolicies(), error: 'invalidShape' };
    }
    const policies = emptyTierPolicies();
    for (const [rawTier, rawLimits] of Object.entries(parsed as Record<string, unknown>)) {
        const tier = rawTier.trim().toLowerCase();
        if (!isUserTier(tier)) return { policies: emptyTierPolicies(), error: 'invalidTier' };
        if (rawLimits === null || typeof rawLimits !== 'object' || Array.isArray(rawLimits)) {
            return { policies: emptyTierPolicies(), error: 'invalidShape' };
        }
        const limits = rawLimits as Record<string, unknown>;
        const next: TierLimits = { ...policies[tier] };
        for (const field of LIMIT_FIELDS) {
            const value = limits[field];
            // 缺字段沿用默认值(后端把缺省当 0/未配置, 界面需要给出可编辑的整行)
            if (value === undefined) continue;
            // 只接受非负整数: 负数与浮点在配额里没有意义, 后端也会拒绝。
            if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
                return { policies: emptyTierPolicies(), error: 'invalidLimit' };
            }
            next[field] = value;
        }
        policies[tier] = next;
    }
    return { policies, error: '' };
}

// serializeUserTierPolicies 把表单草稿序列化成设置值。
// 只输出四个合法等级, 且按固定字段顺序输出 —— 让保存前后 diff 稳定,
// 也避免把后端未知的键写回去(后端遇到未知等级会整段拒绝)。
export function serializeUserTierPolicies(policies: Record<UserTier, TierLimits>): string {
    const wire: Record<string, TierLimits> = {};
    for (const tier of USER_TIERS) {
        const limits = policies[tier];
        wire[tier] = { rpm: limits.rpm, tpm: limits.tpm, concurrent: limits.concurrent };
    }
    return JSON.stringify(wire);
}

// validateTierDraft 校验整张表单草稿; 返回第一个非法字段的等级与字段名, 全合法返回 null。
// 空串输入会被 parseLimitField 判为非法 —— 让管理员看到"这里没填"比静默按 0 保存更安全。
export function validateTierDraft(
    draft: Record<UserTier, Record<LimitField, string>>,
): { tier: UserTier; field: LimitField } | null {
    for (const tier of USER_TIERS) {
        for (const field of LIMIT_FIELDS) {
            if (parseLimitField(draft[tier][field]) === null) return { tier, field };
        }
    }
    return null;
}

// draftFromPolicies 把(可能部分缺省的)策略转成全部等级、全部字段的文本草稿。
export function draftFromPolicies(
    policies: Record<UserTier, TierLimits>,
): Record<UserTier, Record<LimitField, string>> {
    const draft = {} as Record<UserTier, Record<LimitField, string>>;
    for (const tier of USER_TIERS) {
        const limits = policies[tier];
        draft[tier] = {
            rpm: String(limits.rpm),
            tpm: String(limits.tpm),
            concurrent: String(limits.concurrent),
        };
    }
    return draft;
}

// policiesFromDraft 把已通过校验的草稿转回数值策略。
export function policiesFromDraft(
    draft: Record<UserTier, Record<LimitField, string>>,
): Record<UserTier, TierLimits> {
    const policies = {} as Record<UserTier, TierLimits>;
    for (const tier of USER_TIERS) {
        policies[tier] = {
            rpm: parseLimitField(draft[tier].rpm) ?? 0,
            tpm: parseLimitField(draft[tier].tpm) ?? 0,
            concurrent: parseLimitField(draft[tier].concurrent) ?? 0,
        };
    }
    return policies;
}
