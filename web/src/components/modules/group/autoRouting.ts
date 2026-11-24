import type { SelectedMember } from './ItemList';

export type AutoTaskConfigField =
    | 'auto_general'
    | 'auto_summary'
    | 'auto_coding'
    | 'auto_tool'
    | 'auto_extract';

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
