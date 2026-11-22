import { apiKeyUsageTokens, type APIKeyUsageDay } from '@/api/apikey';

// API Key 逐日用量图的纯逻辑。
//
// 组件文件只导出组件(Fast Refresh 约定), 而这些规则恰恰是最该被单测钉死的部分:
// 图表数据的形状、"该不该画图"的判据、日期标签的容错 —— 三者错了都不会报错,

// UsageChartPoint 是图表真正消费的一行。
//
// 字段名保持后端原样(request_count / user_cost / 四个词元桶), 不在这里改叫 requests/cost:
// 一旦改一层名字, 以后对不上接口的人要多一次心算, 而这份数据就是接口原样。
// label 是 X 轴要显示的 MM-DD, day 保留完整日期供悬浮提示用。
export type UsageChartPoint = {
    label: string;
    day: string;
    request_count: number;
    user_cost: number;
    // tokens 是四类词元之和(复用 apiKeyUsageTokens, 不另写一份加总逻辑)。
    tokens: number;
};

// finiteOrZero 把"可能是 undefined / NaN / Infinity"的数值收成一个可用于绘图的有限数。
//
// 为什么要收: recharts 拿到 NaN 时不会报错, 它会照那个值算出畸形的坐标轴和柱高 ——
// 界面上是一张看起来"有数据"的坏图, 比空态更难察觉。
// 收成 0 的后果是这一天在图里没有柱子, 与"这天确实没有调用"在视觉上一致;
// 但下面 hasUsageSamples 会把它算作无样本, 因此不会出现"画一条贴底的零线"那种假象。
function finiteOrZero(value: number | undefined | null): number {
    return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

// usageDayLabel 把后端的 YYYY-MM-DD 变成 X 轴上的 MM-DD。
//
// 直接 slice(5) 是不安全的: 后端换过日期格式(或缓存里还是旧的桶键 YYYYMMDD),
// slice 会切出 "1-20" / "DD" 这种半截字符串, 而它不会报错, 只会印在坐标轴上。
// 因此先校验形状(定长 10、两个连字符、其余全是数字), 不合格就原样返回 ——
// 宁可在轴上显示一个奇怪的完整字符串, 也不要显示一个看起来正常但错位的半截。
export function usageDayLabel(date: string): string {
    if (typeof date !== 'string' || date.length !== 10) return date;
    if (date[4] !== '-' || date[7] !== '-') return date;
    for (let i = 0; i < date.length; i += 1) {
        if (i === 4 || i === 7) continue;
        if (date[i] < '0' || date[i] > '9') return date;
    }
    return date.slice(5);
}

// buildUsageChartData 把日桶数组转成图表数据。
//
// 顺序原样保留: 后端给的是升序, 再排一次只会多一个可能出错的比较函数;
// 万一后端顺序变了, 图会照着数据画 —— 那是数据的问题, 不该由前端悄悄纠正。
// 入参允许 undefined: 后端未升级 / 旧缓存时 days 可能整个缺失,
// 那是"拿不到", 由空态表达, 不能让组件在 map 上崩掉。
export function buildUsageChartData(days: readonly APIKeyUsageDay[] | undefined | null): UsageChartPoint[] {
    if (!Array.isArray(days)) return [];
    return days.map((day) => ({
        label: usageDayLabel(day.date),
        day: day.date,
        request_count: finiteOrZero(day.request_count),
        user_cost: finiteOrZero(day.user_cost),
        // 先把四个桶收成有限数, 再交给 apiKeyUsageTokens 加总 —— 相加的逻辑只存在于那一处,
        // 这里重复实现一份的话, 两处口径迟早走岔(缓存读写是最容易被漏掉的两个)。
        tokens: apiKeyUsageTokens({
            ...day,
            input_token: finiteOrZero(day.input_token),
            output_token: finiteOrZero(day.output_token),
            cache_read_token: finiteOrZero(day.cache_read_token),
            cache_write_token: finiteOrZero(day.cache_write_token),
        }),
    }));
}

// hasUsageSamples 判定"这张图该不该画"。
//
// 两条都算无样本, 一律走空态:
//   1. days 为空数组(窗口内一个日桶都没有);
//   2. 每个日桶的 request_count 与 user_cost 都是 0。
//
// 为什么全零要走空态而不是画图: 画出来是一条贴着 0 的柱子, 它看起来与"这一天调用量很低"
// 完全一样, 但真相是这段窗口根本没有样本。本仓库反复强调的口径是"不许把没取到画成 0",
// 一条零线就是这句话在图形上的违例。
//
// 反过来, 只要**任意一天**有请求或有费用, 就是真的有样本, 必须画:
//   - 请求有、费用为 0(免费模型)是真实用量, 不能因为"没花钱"就不画;
//   - 请求为 0、费用不为 0(退费后净额/跨日结算)也是真实发生过的计费, 同样要画。
export function hasUsageSamples(days: readonly APIKeyUsageDay[] | undefined | null): boolean {
    if (!Array.isArray(days) || days.length === 0) return false;
    return days.some((day) => finiteOrZero(day.request_count) > 0 || finiteOrZero(day.user_cost) > 0);
}

// usageDaysSince 取"从 since 这天起(含当天)"的日桶。
//
// 为什么需要它: 报表的 days 覆盖**整个查询窗口**(默认是终身), 而这行图表的标题就是
// setting.apiKey.usage.daysChart —— 三语文案都是"最近 30 天"。若把终身几百个日桶全画进
// 320px 宽的卡片里, 每根柱子不到 1 像素, 图就退化成一条噪声带; 而且标题与实际窗口不符。
// 因此图表只画最近 30 天, 窗口边界用后端已算好的 recent_from(它已经把"窗口短于 30 天"的情况
// 收窄到窗口起点, 见 internal/op/apikey_usage.go)。
//
// 上方两行数字仍是"最近 30 天"与"终身"各一份, 所以即使最近 30 天一次调用都没有,
// 用户看到的也是"终身 42 次 + 图里最近 30 天没有"—— 这是真实结论, 不是把没取到画成 0。
//
// since 为空串(后端没给 / 旧缓存)时原样返回全部日桶: 拿不到边界就不截断,
// 宁可画得挤一点, 也不能悄悄丢掉一段时间 —— 那与"覆盖不全"是同一类谎。
export function usageDaysSince(
    days: readonly APIKeyUsageDay[] | undefined | null,
    since: string,
): APIKeyUsageDay[] {
    if (!Array.isArray(days)) return [];
    if (typeof since !== 'string' || since.length !== 10) return [...days];
    // YYYY-MM-DD 定长零填充, 字符串序即日期序, 不必解析成 Date(也就没有时区问题)。
    return days.filter((day) => day.date >= since);
}
