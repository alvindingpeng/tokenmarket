import type { GroupMode, GroupRelayConfig } from '@/api/group';

// 灰度分流是叠加在非手动模式之上的一层: 命中的请求先只走灰度成员, 失败再回到原模式的候选。
// 它不是一个选择模式, 所以既不出现在模式下拉里, 也不由模式预设决定取值 ——
// 切换模式、套用预设都必须原样保留这四项(见 preserveCanary)。
export type CanaryConfig = Pick<
    GroupRelayConfig,
    'canary_item_id' | 'canary_percent' | 'canary_user_ids' | 'canary_per_request'
>;

// canaryDefaults 是关闭灰度的取值, 与后端 CanaryActive 的判定一致: 没指定成员(0)就没有灰度。
export function canaryDefaults(): CanaryConfig {
    return { canary_item_id: 0, canary_percent: 0, canary_user_ids: [], canary_per_request: false };
}

// canaryOf 取出配置里的灰度四项, 缺失的值回落到关闭态。
// 入参取 Partial: 编辑入口拿到的是保存过的 relay_config, 老数据里可能根本没有这四个字段。
export function canaryOf(config?: Partial<CanaryConfig>): CanaryConfig {
    const defaults = canaryDefaults();
    return {
        canary_item_id: config?.canary_item_id ?? defaults.canary_item_id,
        canary_percent: config?.canary_percent ?? defaults.canary_percent,
        canary_user_ids: config?.canary_user_ids ?? defaults.canary_user_ids,
        canary_per_request: config?.canary_per_request ?? defaults.canary_per_request,
    };
}

// preserveCanary 把旧的灰度配置盖回一份新配置上, 供切换模式/套用预设时复用。
// 灰度与各模式的参数互不相干, 换模式不该顺手清掉用户刚填好的灰度。
export function preserveCanary(next: GroupRelayConfig, previous: GroupRelayConfig): GroupRelayConfig {
    return { ...next, ...canaryOf(previous) };
}

// canaryChanged 判断灰度四项是否与保存值不同, 供编辑提交时决定要不要带上 relay_config。
// 用户 ID 是集合语义: 先后顺序不影响路由结果, 重排一次不该被当成改动而多提交一次。
export function canaryChanged(next: GroupRelayConfig, previous: GroupRelayConfig): boolean {
    const left = canaryOf(next);
    const right = canaryOf(previous);
    return left.canary_item_id !== right.canary_item_id ||
        left.canary_percent !== right.canary_percent ||
        left.canary_per_request !== right.canary_per_request ||
        !sameIDs(left.canary_user_ids, right.canary_user_ids);
}

// sameIDs 按集合比较两份用户 ID: 排序后逐项相等即视为同一组, 与输入顺序无关。
function sameIDs(a: number[], b: number[]): boolean {
    if (a.length !== b.length) return false;
    const sortedA = [...a].sort((x, y) => x - y);
    const sortedB = [...b].sort((x, y) => x - y);
    return sortedA.every((id, index) => id === sortedB[index]);
}

// parseCanaryUserIDs 把「逗号或空格分隔」的用户 ID 输入解析成 number[]。
// 非整数、零与负数都算非法并原样回传: 静默丢弃会让人以为灰度已经生效而实际没有。
// 重复项按首次出现去重, 顺序保持输入顺序(提交给后端的是集合语义)。
export function parseCanaryUserIDs(text: string): { ids: number[]; invalid: string[] } {
    const ids: number[] = [];
    const invalid: string[] = [];
    for (const token of text.split(/[\s,，、]+/)) {
        if (!token) continue;
        const value = Number(token);
        if (!Number.isInteger(value) || value <= 0) {
            invalid.push(token);
            continue;
        }
        if (!ids.includes(value)) ids.push(value);
    }
    return { ids, invalid };
}

// formatCanaryUserIDs 把用户 ID 列表还原成输入框里的文本, 与 parseCanaryUserIDs 互逆。
export function formatCanaryUserIDs(ids: number[]): string {
    return ids.join(', ');
}

// canaryActive 判断灰度当下是否真的会分流, 与后端 CanaryActive 同口径: 有成员且比例非零。
// 只用于界面提示"配了但没生效", 不参与提交校验 —— 灰度没配好不该连别的字段都不让保存。
export function canaryActive(config: CanaryConfig): boolean {
    return config.canary_item_id !== 0 && config.canary_percent > 0;
}

// canarySubmitBlocked 判断用户 ID 输入是否应当拦住保存。
// 手动模式下这一块根本不存在, 它的输入也就不该妨碍保存 —— 切到手动后残留的非法文本不该成拦路虎。
export function canarySubmitBlocked(mode: GroupMode, userIDsText: string): boolean {
    if (mode === 'manual') return false;
    return parseCanaryUserIDs(userIDsText).invalid.length > 0;
}

// canarySubmitConfig 把输入框里的文本并回提交用的 relay_config。
// 用户列表以输入框为唯一真源: 组件状态里存的是文本, 提交前用同一个解析函数落成 number[],
// 校验与提交因此不可能出现"看着合法却存了另一份值"的分歧。
export function canarySubmitConfig(config: GroupRelayConfig, userIDsText: string): GroupRelayConfig {
    return { ...config, canary_user_ids: parseCanaryUserIDs(userIDsText).ids };
}
