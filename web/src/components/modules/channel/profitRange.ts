// 渠道利润面板的日期范围工具独立成文件: Profit.tsx 只导出组件,
// 否则 react-refresh 会因为"文件同时导出组件与函数"拒绝热更新。

// pad2 给日期选择器补两位; 不用 toISOString: 那会按 UTC 切成前一天。
const pad2 = (value: number) => String(value).padStart(2, '0');

// profitDateInput 把 Date 渲染成 <input type="date"> 认的本地日期串。
export function profitDateInput(date: Date): string {
    return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

// defaultProfitRange 默认最近 30 天(含今天), 与后端缺省区间一致。
// today 可注入, 便于测试固定日期, 否则结果会随运行日漂移。
export function defaultProfitRange(today: Date = new Date()): { startDate: string; endDate: string } {
    return profitPresetRange(30, today);
}

// profitPresetRange 生成"最近 N 天"的区间; N=1 即今天。
export function profitPresetRange(days: number, today: Date = new Date()): { startDate: string; endDate: string } {
    const end = new Date(today.getFullYear(), today.getMonth(), today.getDate());
    const start = new Date(end);
    start.setDate(start.getDate() - (Math.max(1, days) - 1));
    return { startDate: profitDateInput(start), endDate: profitDateInput(end) };
}

// profitRangeValid 校验起止日期: 空的日期不发请求, 起点晚于终点是明显输入错误,
// 交给后端只会拿到一个 400, 不如就地提示。
export function profitRangeValid(startDate: string, endDate: string): boolean {
    return Boolean(startDate) && Boolean(endDate) && startDate <= endDate;
}
