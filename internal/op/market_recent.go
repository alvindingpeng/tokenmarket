package op

import (
	"sort"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// ChannelModelRecentMetrics 返回若干渠道模型近 days 天的日均成绩, 供广场在 relay 没有观测时回落。
// 它产出的是持久化统计里的日均值, 与进程内 EMA 不同源, 故整段换源而非逐字段拼接。
//
// 为什么需要这条回落: 成功率与延迟来自 relay 的进程内 EMA。进程重启**不会**丢失成绩
// (relay.RestorePersist 会从 route_metrics 表恢复), 所以回落解决的不是"重启清零" ——
// 实测生产数据证明, 重启后那批有历史成绩的模型依然标 live。
//
// 它真正补的是另一种空白: 某个渠道模型**从未经任何分组转发过**, 于是 relay 里没有它的观测。
// 典型情形是模型刚上架、或刚被加进某个分组: 它在按天统计里可能有历史(换路由前跑过),
// 但内存里一行都没有。没有回落, 广场会把它显示成"—", 用户读到的是"没数据"。
//
// 口径(设计稿 §5.2 的警告在此落实): WaitTime 是首帧等待的**累计值**, 与 EMA 的单次均值
// 不同源, 因此回落时整段换源, 绝不与 live 数字逐字段拼接。成功率 =
// RequestSuccess/(RequestSuccess+RequestFailed), 延迟 = WaitTime/(成功+失败)。
//
// 只回模型级聚合(days 天求和后再除), 不逐日返回: 广场只回答"近一周怎么样",
// 逐日曲线是渠道详情页的事, 两处各取所需。
//
// days <= 0 时取 7 天(与设计稿一致); 上限 90 天, 防调用方传一个扫全表的值。
func ChannelModelRecentMetrics(channelModelIDs []int, days int) map[int]model.StatsMetrics {
	out := make(map[int]model.StatsMetrics, len(channelModelIDs))
	if len(channelModelIDs) == 0 {
		return out
	}
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	// 与 ChannelDailyStatsGet 同一写法: 日期是 20060102 文本, 直接字符串比较即可。
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("20060102")

	var rows []model.StatsChannelModelDaily
	if err := db.GetDB().Where("channel_model_id IN ? AND date >= ?", channelModelIDs, since).
		Order("date ASC").Find(&rows).Error; err != nil {
		// 回落是锦上添花: 读失败时返回空表, 让调用方退回到"—", 不能把广场整个接口打挂。
		return out
	}
	for _, row := range rows {
		metrics := out[row.ChannelModelID]
		metrics.Add(row.StatsMetrics)
		out[row.ChannelModelID] = metrics
	}
	return out
}

// RecentMetricsOf 把一份累计指标折算成"日均"的展示值: 成功率与平均首帧耗时。
//
// 与 relay 的 EMA 口径的区别在这里收口: EMA 是逐次请求的滑动平均, 这里是区间算术平均。
// 两者都合理但不可混算 —— 一旦混算, 同一个模型在重启前后会给出两个互相矛盾的成功率。
// 样本数取 RequestSuccess+RequestFailed(真实转发次数), 探测与本地错误不计入。
func RecentMetricsOf(metrics model.StatsMetrics) (samples int, success float64, waitMs float64) {
	total := metrics.RequestSuccess + metrics.RequestFailed
	if total <= 0 {
		return 0, 0, 0
	}
	return int(total), float64(metrics.RequestSuccess) / float64(total), float64(metrics.WaitTime) / float64(total)
}

// RecentMetricsOrder 按样本数降序返回主键, 供调用方稳定地构造查询与测试断言。
func RecentMetricsOrder(ids []int) []int {
	unique := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id <= 0 {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	sort.Ints(unique)
	return unique
}
