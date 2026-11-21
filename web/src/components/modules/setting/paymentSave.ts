// 批量保存设置的提交结果归纳。
//
// 为什么需要单独一段纯逻辑: 支付配置是一张长表单, 十几个独立的设置键逐个提交。
// 中途失败会出现「一半新一半旧」的状态, 而此前代码只有一句 toast.error ——
// 用户看到的是「保存失败」, 但不知道是哪几项失败了, 于是只能整张表再提交一遍(重复保存已成功的项),
// 或者干脆以为全都没成(其实改了一半)。两种误判都会留下不一致的配置。
//
// 这里把「哪几项成了、哪几项没成」算清楚, 交给界面如实列出。

export type BatchSaveOutcome = {
    // saved 已确认落库的键。
    saved: string[];
    // failed 首次失败的键 —— 从它开始停止提交, 后面的键压根没发出去。
    failed: string;
    // skipped 因前面失败而没发出的键。
    skipped: string[];
    // done 是否全部成功(调用方据此决定提示语)。
    done: boolean;
};

// summarizeBatchSave 归纳本次提交结果。
//
// pending 是本次要提交的键序列(按提交顺序), savedCount 是已经成功的个数,
// failedKey 是第一个失败的键。停止点是**失败处**而不是继续往下发: 继续发会造出
// 「后面成了、中间没成」的乱序状态 —— 配置项之间常有隐含依赖(比如开了满赠却丢了满赠规则)。
export function summarizeBatchSave(pending: string[], savedCount: number, failedKey: string): BatchSaveOutcome {
    const saved = pending.slice(0, savedCount);
    // done 由「有没有失败键」决定, 不由 savedCount 推: 全部成功时 failedKey 是空串,
    // 而 savedCount 恰好等于 pending.length —— 两个条件都在这一个判据里。
    const done = failedKey === '';
    const skipped = done ? [] : pending.slice(savedCount + 1);
    return { saved, failed: failedKey, skipped, done };
}

// unsavedKeys 列出所有**尚未落库**的键(失败项 + 没发出去的项)。
//
// 界面用它给输入框打「未保存」标记: 标记必须按 key 精确落到具体格子, 整表标红等于没标。
export function unsavedKeys(outcome: BatchSaveOutcome): string[] {
    // 全部成功时返回空数组而不是 ['']: 调用方会用它去遍历打标记,
    // 一个空串虽然不会命中任何真实键, 但"未保存列表里有一个元素"这件事本身就会误导下一处调用方。
    if (outcome.done) return [];
    return [outcome.failed, ...outcome.skipped];
}

// isFieldUnsaved 该输入框是不是上次没保存成功的那一项。
//
// 标记必须**按 key 精确到格子**: 整表标红等于没标 —— 用户看着一片红, 只能把整张表从头再确认一遍,
// 而那正是这段代码想省掉的事。所以这里不做任何模糊匹配(前缀、包含), 只做完全相等。
export function isFieldUnsaved(outcome: BatchSaveOutcome | null, key: string): boolean {
    if (outcome === null) return false;
    return unsavedKeys(outcome).includes(key);
}
