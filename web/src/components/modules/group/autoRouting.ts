import type { SelectedMember } from './ItemList';

export type AutoTaskConfigField =
    | 'auto_general'
    | 'auto_summary'
    | 'auto_coding'
    | 'auto_tool'
    | 'auto_extract';

export function parseContextOverrides(value: Record<string, number> | undefined): Record<number, number> {
    const overrides: Record<number, number> = {};
    for (const [key, context] of Object.entries(value ?? {})) {
        const grantID = Number(key);
        if (Number.isSafeInteger(grantID) && grantID > 0 && Number.isSafeInteger(context) && context > 0) {
            overrides[grantID] = context;
        }
    }
    return overrides;
}

export function contextOverridesValue(overrides: Record<number, number>): Record<string, number> | undefined {
    const values: Record<string, number> = {};
    for (const [key, context] of Object.entries(overrides)) {
        const grantID = Number(key);
        if (Number.isSafeInteger(grantID) && grantID > 0 && Number.isSafeInteger(context) && context > 0) {
            values[String(grantID)] = context;
        }
    }
    return Object.keys(values).length > 0 ? values : undefined;
}

export function compareContextOverrides(left: Record<string, number> | undefined, right: Record<string, number> | undefined): boolean {
    const keys = new Set([...Object.keys(left ?? {}), ...Object.keys(right ?? {})]);
    for (const key of keys) {
        if ((left ?? {})[key] !== (right ?? {})[key]) return true;
    }
    return false;
}

// parseAutoGrantIDs 兼容 v1.16.5 已保存的逗号分隔授权 ID。重复与其他用户的
// 历史 ID 不在这里过滤，保存时会按当前分组已选成员重新序列化。
export function parseAutoGrantIDs(value: string | undefined): number[] {
    const ids = new Set<number>();
    for (const token of (value ?? '').split(',')) {
        const id = Number(token.trim());
        if (Number.isSafeInteger(id) && id > 0) ids.add(id);
    }
    return [...ids];
}

export function autoGrantIDsValue(members: SelectedMember[], selectedGrantIDs: number[]): string {
    const available = new Set(members.map((member) => member.channel_grant_id));
    const selected = new Set(selectedGrantIDs);
    return members
        .map((member) => member.channel_grant_id)
        .filter((id, index, ids) => available.has(id) && selected.has(id) && ids.indexOf(id) === index)
        .join(',');
}

export function toggleAutoGrantID(selectedGrantIDs: number[], grantID: number, checked: boolean): number[] {
    const selected = new Set(selectedGrantIDs);
    if (checked) selected.add(grantID);
    else selected.delete(grantID);
    return [...selected];
}
