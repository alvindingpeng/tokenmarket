// 设置项保存失败时的回滚基准。
//
// 为什么需要单独一个模块: 三张设置卡片(SystemInfo / Platform / System)都遇到同一个问题 ——
// 输入框在 blur 时提交, 界面上显示的是"刚敲进去的新值", 而服务端可能还是旧值。
// 保存失败时如果不回滚, 界面与现实就此分叉, 管理员以为已经生效。
//
// 回滚到哪? **必须回到"最近一次确认落库的值"**, 而不是去 settings 列表里按 key 查。
// 后者看起来等价, 其实不等价: 列表是查询缓存, 在连续两次编辑之间可能还没刷新 ——
// 于是第二次失败会回退到第一次之前的值, 用户会以为"第二次改动被接受了, 只是又变回去了",
// 比不回滚更难排查。这里维护的是"自己发出去并被服务端确认过的值", 时间序是准的。

// SettingEntry 只取用得到的两个字段, 免得这个纯模块反向依赖 api 层类型。
export type SettingEntry = { key: string; value: string };

// confirmedFromSettings 从设置列表取出"服务端已确认"的初始基准。
// 列表还没到(data 为 undefined)时返回空表: 此时任何保存都还没有基准,
// 回滚时会选择"不动输入框"而不是把它清空(见 rollbackValue 的返回值语义)。
export function confirmedFromSettings(settings: SettingEntry[] | undefined): Record<string, string> {
    const confirmed: Record<string, string> = {};
    for (const item of settings ?? []) confirmed[item.key] = item.value;
    return confirmed;
}

// rollbackValue 给出该键应回滚到的值。
//
// 返回 undefined 表示"没有可回滚的基准" —— 调用方此时应当**保持输入框不变**。
// 清空输入框是另一种谎言: 它既没有回到服务端的值, 又抹掉了用户刚敲进去的内容,
// 两种损失叠加。
export function rollbackValue(confirmed: Record<string, string>, key: string): string | undefined {
    return Object.hasOwn(confirmed, key) ? confirmed[key] : undefined;
}

// FailedSave 一次保存失败之后, 输入框该显示什么、提示语用哪一句。
//
// 这是「失败态伪装成成功」这条缺陷的**唯一**修复点, 所以它被抽成纯函数并被三张设置卡片共用:
// 输入框是受控的, 显示的一直是刚敲进去的新值; 保存失败后若不回滚, 界面停在那个值上而服务端
// 仍是旧值 —— 管理员按界面判断「已经改了」, 直到刷新或下次进来才发现没有。
//
// 两种情况的取舍是明确的:
//   · 有基准 → 回到基准, 让用户重新输入一次。好过让他在错误的认知下离开。
//   · 没基准 → 不动输入框。清空它既没回到服务端值(因为不知道服务端值), 又抹掉了用户刚敲的内容,
//     是第二重谎言。此时提示语原样透出错误信息, 不说「已还原」(其实什么也没还原)。
export type FailedSave = {
    // input 输入框应当显示的值。
    input: string;
    // reverted 是否真的回滚了 —— 决定提示语用「保存失败, 已还原」还是原样透出错误。
    reverted: boolean;
};

export function failedSave(confirmed: Record<string, string>, key: string, currentInput: string): FailedSave {
    const previous = rollbackValue(confirmed, key);
    if (previous === undefined) return { input: currentInput, reverted: false };
    return { input: previous, reverted: true };
}

// SaveDeps 执行一次单项保存所需的全部外部依赖。
//
// 抽成这个形状是为了让「失败后到底调了 apply 没有」可被单测断言:
// 三张设置卡片的 onError 在组件里是闭包, mock SSR 碰不到它; 而这条行为恰恰是本缺陷的核心 ——
// 保存失败后界面停在用户敲的新值上, 服务端仍是旧值。
export type SaveDeps = {
    key: string;
    value: string;
    // confirmed 基准表(组件里是 useRef().current, 传进来的是同一个对象, 成功时会被就地更新)。
    confirmed: Record<string, string>;
    // currentInput 输入框此刻显示的值 —— 没有基准时保持它不变, 不留空白。
    currentInput: string;
    // apply 把值写回输入框绑定的 state。
    apply: (next: string) => void;
    // submit 真的发出请求; 失败要抛出(组件传 mutateAsync, 它 reject 时带的是后端消息)。
    submit: (key: string, value: string) => Promise<unknown>;
};

// SaveResult 一次保存的结果。
//   ok        : 已落库, confirmed 已更新
//   reverted  : 失败且回滚到基准值
//   unchanged : 失败且没有基准, 输入框原样保留(不许说已还原)
export type SaveResult =
    | { ok: true }
    | { ok: false; message: string; reverted: boolean; input: string };

// saveWithRollback 提交一项设置, 失败时按 failedSave 的决定把输入框改回去。
//
// 顺序上只有一点容易做错: 失败分支必须先算 failedSave 再 apply。
// 无条件 apply(initialValue) 是另一种 bug —— 那会把「从没成功保存过」的项
// 也写回一个它从未拥有过的值。
export async function saveWithRollback(deps: SaveDeps): Promise<SaveResult> {
    const { key, value, confirmed, currentInput, apply, submit } = deps;
    try {
        await submit(key, value);
        // 基准只在服务端确认之后更新: 这是回滚唯一可信的时间序。
        confirmed[key] = value;
        return { ok: true };
    } catch (error) {
        const outcome = failedSave(confirmed, key, currentInput);
        apply(outcome.input);
        const message = error instanceof Error ? error.message : String(error);
        return { ok: false, message, reverted: outcome.reverted, input: outcome.input };
    }
}
