export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

export function memberKey(member: { channel_grant_id: number }) {
    return String(member.channel_grant_id);
}

export function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}

// autoAddMatches 自动添加的匹配规则(优先级链):
// 1. 搜索框有关键字 → 渠道名 OR 模型名 包含命中(搜索框有可见筛选结果做预期锚点, 作为主匹配源);
// 2. 搜索框为空且组名非空 → 按组名匹配模型名(沿用旧语义, 向后兼容);
// 3. 两者皆空 → 不匹配(按钮置灰, 防止误点全量导入)。
// keyword 与 groupKey 均按 trim+小写包含比较, 与左侧列表筛选口径一致。
export function autoAddMatches(
    mc: { name: string; channel_name: string },
    keyword: string,
    groupKey: string,
): boolean {
    const kw = keyword.trim().toLowerCase();
    if (kw) {
        return mc.channel_name.toLowerCase().includes(kw) || mc.name.toLowerCase().includes(kw);
    }
    return matchesGroupName(mc.name, groupKey);
}
