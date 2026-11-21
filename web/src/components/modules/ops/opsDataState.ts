// 运维页三个区块的「失败 / 加载 / 空 / 有数据」判定。
//
// 为什么抽成纯函数: 这些判定原先散在 JSX 里, 而且都是缺的 —— 只判 isLoading 不看 isError,
// 于是取数失败会落进空态分支, 屏幕上写「还没有建过预算」/「还没有导入过账单」。
// 抽出来之后可以直接对着四条输入断言四种输出, 不必起浏览器。

// OpsDataState 区块的四种终态。
// error   : 取数失败, 且没有可用旧数据
// stale   : 取数失败, 但有上一次成功的数据(保留展示 + 提示可能过期)
// loading : 首次加载中
// empty   : 真的没有数据
// ready   : 有数据可渲染
export type OpsDataState = 'error' | 'stale' | 'loading' | 'empty' | 'ready';

// opsDataState 按优先级判定。
//
// isError 优先于 isLoading: 重试时两个同时为真, 此时必须显示错误态而不是转圈 ——
// 转圈会让人以为还在努力, 其实上一次已经失败了。
export function opsDataState(input: {
    isError: boolean;
    isLoading: boolean;
    count: number;
}): OpsDataState {
    const { isError, isLoading, count } = input;
    if (isError) return count > 0 ? 'stale' : 'error';
    if (isLoading) return 'loading';
    if (count === 0) return 'empty';
    return 'ready';
}

// BudgetRowMode 一行预算的操作区显示什么。
//
// idle           : 显示「停用/启用」与「删除」两个按钮
// confirmToggle  : 已点过一次启停, 等第二次确认
// confirmDelete  : 已点过一次删除, 等第二次确认
export type BudgetRowMode = 'idle' | 'confirmToggle' | 'confirmDelete';

// budgetRowMode 判断某一行的操作区状态。
//
// pending 用「具体某一行 + 哪种操作」而不是一个布尔: 表格里同时有多行, 一个布尔会让点第二行时
// 第一行的确认态也跟着亮起来 —— 那时候按下去删掉的可能是另一条预算。
export function budgetRowMode(pending: { id: number; kind: 'delete' | 'toggle' } | null, rowId: number): BudgetRowMode {
    if (!pending || pending.id !== rowId) return 'idle';
    return pending.kind === 'delete' ? 'confirmDelete' : 'confirmToggle';
}
