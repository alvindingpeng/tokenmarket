// 投递日志的筛选条件 + 页码, 以及"改筛选回第 1 页"的唯一入口。
//
// 单独成文件的原因:
//   1) 组件文件只导出组件, 才能让 React Fast Refresh 正常工作(react-refresh 规则);
//   2) "换筛选必须重置页码"是纯逻辑, 抽出来就能在不起 React 的情况下单测。

// LogView 投递日志当前的筛选与页码。
export type LogView = { page: number; status: string; event: string };

// setLogFilter 变更筛选时把页码重置回 1:
// 停在第 3 页去查一个只有 1 页结果的状态/事件, 用户看到的是空列表, 会误以为「没有数据」。
// 空串代表「全部」, 与 api 层 webhookLogsPath 的「空值不拼参数」约定一致。
export function setLogFilter(view: LogView, patch: Partial<Pick<LogView, 'status' | 'event'>>): LogView {
    return { ...view, ...patch, page: 1 };
}
