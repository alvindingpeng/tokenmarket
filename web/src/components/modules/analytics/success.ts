import type { UserStatsOverview, UserStatsTrend } from '@/api/user-stats';

// 成功率展示口径的纯函数集中在这里, 不放在 index.tsx 里:
// 组件文件只导出组件才能保住 Fast Refresh(改样式不丢页面状态),
// 而这些规则(分母、哨兵值、覆盖判定)恰恰是最该被单测钉死的部分。

// summarizeSuccess 成功率卡片的展示口径: 百分比与样本量必须来自同一对计数。
// 分母取 success + failed —— 与后端 success_rate 完全同口径(canceled 不进分母),
// 否则界面会出现"成功率 98.5%"配"成功 9850 / 总数 10012"这种自己都对不上的数字。
// 一次样本都没有时(区间内没有调用日志, 或日志已超出留存期), 后端给的 success_rate=0 只是哨兵值:
// 显示 "—" 而不是 0.0% —— 0.0% 会被读成"全部请求都失败了"。
export function summarizeSuccess(overview: UserStatsOverview): { attempts: number; value: string } {
    const attempts = overview.request_success + overview.request_failed;
    return { attempts, value: attempts > 0 ? `${overview.success_rate.toFixed(1)}%` : '—' };
}

// isCoverageLimited 判断调用日志是否只覆盖了查询区间的后半段。
// 只有覆盖起点非空(区间内确实有日志)且严格晚于查询起点时才算"覆盖不全";
// 起点为空表示根本没有日志(那是"无样本", 由 summarizeSuccess 表达), 不需要说"仅覆盖 X 起"。
export function isCoverageLimited(coverageStart: string, queryStart: string | undefined): boolean {
    if (coverageStart === '' || !queryStart) return false;
    // YYYY-MM-DD 定长零填充, 字符串序即日期序, 不必再解析成 Date。
    return coverageStart > queryStart;
}

// buildTrendChartData 把趋势点转成图表口径: 没有日志样本的桶, 成功率换成 null。
// 后端在没有样本时给的是 0, recharts 会照着画一条跌到 0 的线 —— 用户看到的是"成功率崩了",
// 而真相只是那天已经超出日志留存期。null + connectNulls={false} 才能让它断在那里。
// 注意"全失败"是真实的 0(分母 5), 与"没有样本"(null)完全不同, 不能一并抹平。
export function buildTrendChartData(points: UserStatsTrend[]): (Omit<UserStatsTrend, 'success_rate'> & { success_rate: number | null })[] {
    return points.map((point) => ({
        ...point,
        success_rate: point.request_success + point.request_failed > 0 ? point.success_rate : null,
    }));
}

// hasSuccessSamples 判断整张趋势图有没有任何成功率样本。
// 一个都没有时连成功率轴和折线都不画: 留一条贴着 0 的空折线, 比不画更容易被误读。
export function hasSuccessSamples(points: UserStatsTrend[]): boolean {
    return points.some((point) => point.request_success + point.request_failed > 0);
}
