import type { UserStatsOverview } from '@/api/user-stats';

// 统计页的时区口径纯函数: 决定"要不要告诉用户两个时区不同", 以及怎么把后端桶标签缩成刻度。
//
// 为什么单独成文件: 组件文件只导出组件才能保住 Fast Refresh, 而这几条判据正该被单测钉死。
//
// 背景(后端 contract, 不是缺陷): 统计页同时存在两个时区。
//   - 查询【区间边界】按 stats_timezone 算(用户偏好生效则用用户时区, 否则服务器时区);
//   - 日/周/月【分组】与【数据留存】恒按 stats_calendar_timezone(服务器时区)算 ——
//     汇总行是按服务器自然日固化的, 日历一换, 统计页就会和账单页、留存口径对不上。
// 因此"分组时区不跟着查询时区走"是刻意的。两个时区挂钟一致时(偏移相同)用户看不到任何差别, 也就
// 不该看到提示: 只有偏移真的不同、看到的日子真的会错开时才说明。

// TimezoneNotice 三种状态:
//   legacy   —— 拿不到时区字段(老后端), 一律不提示, 也不要把 undefined 拼进文案;
//   none     —— 两个时区当前偏移相同, 挂钟一致, 无需提示;
//   calendar —— 偏移不同, 需要说明"边界按窗口时区、分组按日历时区"。
export type TimezoneNotice =
    | { kind: 'legacy' }
    | { kind: 'none' }
    | { kind: 'calendar'; calendarTimezone: string; windowTimezone: string };

// timezoneNotice 判定页面顶部是否要出现时区口径说明。
//
// 判据:
//   1. overview 未就绪或字段缺失(老后端) -> legacy。前端不提示、不编造 —— 拿不到事实就不说。
//   2. 两个偏移相等 -> none。挂钟一致时用户看到的日期与分组完全一致, 按"名字是否相同"判会误报:
//      不同 IANA 名在某个时刻可能共享同一偏移(例如 +08:00 下的多个亚洲时区)。
//   3. 偏移不同 -> calendar。两个名字都带出来: 只说"时区不同"而不说是哪两个时区, 用户无从判断差在哪。
//
// 名字相同也一定落在 none: 同名必然同偏移, 不需要额外短路。offset 相等即 none 已经覆盖它。
export function timezoneNotice(overview: UserStatsOverview | undefined): TimezoneNotice {
    // 老后端没有这几个字段: 运行时先确认它们真的是数字, 而不是依赖类型声明 ——
    // 类型上它们是必填, 但部署里很可能还是旧版本, 用运行时校验才能同时覆盖两种状态。
    const windowOffset = overview?.stats_timezone_offset_minutes;
    const calendarOffset = overview?.stats_calendar_timezone_offset_minutes;
    const windowTimezone = overview?.stats_timezone;
    const calendarTimezone = overview?.stats_calendar_timezone;
    if (typeof windowOffset !== 'number' || typeof calendarOffset !== 'number'
        || typeof windowTimezone !== 'string' || typeof calendarTimezone !== 'string') {
        return { kind: 'legacy' };
    }
    // 挂钟一致就不提示: 偏移是用户唯一能感知的差异(日期落在哪一天、桶从几点开始)。
    if (windowOffset === calendarOffset) {
        return { kind: 'none' };
    }
    return { kind: 'calendar', calendarTimezone, windowTimezone };
}

// hourTickLabel 把后端的小时桶标签缩成 X 轴刻度。
//
// 后端给的是 "YYYY-MM-DD HH:MM"。收成 HH:MM 之后日期由相邻刻度体现, 整根轴上就不会挤满重复的日期。
// 只按"长度是 16"判断, 不再假定一定是整点: +05:45 / +08:45 这类非整点偏移会让桶落在 :45 ——
// 那是个真实的时间点, 抹成 :00 就是在改数据。
// 其他长度的标签(按天/周/月的桶就是纯 YYYY-MM-DD)原样返回, 交给下面的 tickFormatter 逻辑。
export function hourTickLabel(date: string): string {
    return date.length === 16 ? date.slice(11) : date;
}

// formatOffset 把 UTC 偏移分钟数转成 "UTC+08:00" / "UTC-05:00" / "UTC+05:45" 形态。
//
// 为什么不用 "GMT+8" 之类: 半小时/三刻钟时区的存在(+05:30 印度、+05:45 尼泊尔、+08:45 部分澳洲)
// 让"整点小时"这个假设不成立, 必须能显示到分钟。
// 非有限数返回空串而不是 "NaN" / "UTC+NaN:NaN": 调用方据此决定是否渲染, 脏数据不该印到界面上。
export function formatOffset(minutes: number): string {
    if (!Number.isFinite(minutes)) return '';
    const sign = minutes < 0 ? '-' : '+';
    // 对绝对值取整: 负偏移的分钟数若直接参与取模, -30 会算出 "UTC-00:-30"。
    const total = Math.abs(Math.round(minutes));
    const hours = String(Math.floor(total / 60)).padStart(2, '0');
    const mins = String(total % 60).padStart(2, '0');
    // 负数里没有 "-0" 这个时区: -0 分钟与 +0 分钟是同一个偏移(UTC), 统一写 +00:00。
    // 上面 minutes < 0 对 -0 判为 false(-0 < 0 不成立), 因此这里天然得到 "+"。
    return `UTC${sign}${hours}:${mins}`;
}
