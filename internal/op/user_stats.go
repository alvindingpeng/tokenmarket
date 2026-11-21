package op

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// UserStatsOverview 查询用户在指定时间区间的调用概览。
// 默认最近 30 天; startDate/endDate 为空时使用默认值。
//
// 两个数据源, 口径不混用:
//   - 调用量/费用/Token 走 billing_records: 计费行含 refunded 行, 与费用口径同源;
//   - 成功率走 relay_requests 的终态: 失败的调用根本不产生计费行(没有用量就没有结算),
//     用计费行算成功率会在没有任何退费时恒等于 100% —— 那不是"稳定", 是把失败整段吞掉了。
func UserStatsOverview(ctx context.Context, userID uint, startDate, endDate string) (model.UserStatsOverview, error) {
	start, end := parseStatsDateRange(startDate, endDate)

	// 费用与词元读「明细 ∪ 日汇总」, 不再只读明细。
	//
	// 只读 billing_records 的后果: 明细按留存期(默认 180 天)清理, 而本读口的时间窗可以是"近一年"
	// 甚至更早。清理之后, 同一账户同一区间在统计页显示的金额/词元/请求数会随清理一天天变小,
	// 而账单页(BillingTotal)并了 billing_rollups —— 两页数字对不上, 且没有任何提示。
	// 用户拿它导出对账时, 差额无法解释。合并规则见 userStatsBillingAggregate。
	billing, err := userStatsBillingAggregate(ctx, userID, start, end)
	if err != nil {
		return model.UserStatsOverview{}, err
	}

	// 成功率另取调用日志: 与计费口径无关, 两者必须各自成对(计数与比率同源)才不会被误读。
	rel, err := queryRequestOutcome(ctx, userID, start, end)
	if err != nil {
		return model.UserStatsOverview{}, err
	}

	// 账单数据本身也有覆盖边界: 明细只留 180 天, 更早的靠日汇总补, 而汇总只从"首次清理"那天起存在。
	// 不把这条边界说出来, 用户会把"谁都补不回来的那段"读成"平台少算了"。
	coverageStart, rollupUntil, modelCoverage, err := userStatsBillingCoverage(ctx, userID, start, end)
	if err != nil {
		return model.UserStatsOverview{}, err
	}

	// 词元四项之和恒等于 total_tokens: 两侧(明细/汇总)都按同一组列相加, 不混用别的口径。
	totalTokens := billing.InputToken + billing.OutputToken + billing.CacheReadToken + billing.CacheWriteToken

	return model.UserStatsOverview{
		TotalRequests:               billing.RequestCount,
		SuccessRate:                 rel.successRate(),
		TotalCost:                   billing.UserCost,
		TotalTokens:                 totalTokens,
		RequestTotal:                rel.Success + rel.Failed + rel.Canceled,
		RequestSuccess:              rel.Success,
		RequestFailed:               rel.Failed,
		RequestCanceled:             rel.Canceled,
		RequestCoverageStart:        rel.coverageStart,
		RequestRetentionDays:        rel.retentionDays,
		InputTokens:                 billing.InputToken,
		OutputTokens:                billing.OutputToken,
		CacheReadTokens:             billing.CacheReadToken,
		CacheWriteTokens:            billing.CacheWriteToken,
		CacheHitRate:                cacheHitRate(billing.CacheReadToken, billing.InputToken),
		BillingCoverageStart:        coverageStart,
		BillingRollupCoveredUntil:   rollupUntil,
		ModelBreakdownCoverageStart: modelCoverage,
	}, nil
}

// UserStatsTrend 查询用户调用趋势（按日/周/月聚合）。
// granularity 为空时默认按天。
// 与概览同源分层: Requests/Cost 来自计费行(含 refunded), SuccessRate 来自调用日志。
// 两个源分别聚合后按桶合并: 超出日志留存期的桶只有计费数据, 成功率留 0 由前端断线, 不能补 100。
// 周桶以周一为一周起点, Date 标签为当周周一(YYYY-MM-DD)。
func UserStatsTrend(ctx context.Context, userID uint, startDate, endDate, granularity string) ([]model.UserStatsTrend, error) {
	start, end := parseStatsDateRange(startDate, endDate)
	if granularity == "" {
		granularity = "day"
	}

	dateFunc := statsBucketExpr(statsDialect(), granularity)

	var rows []struct {
		Date     string
		Requests int64
		Cost     float64
	}

	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(fmt.Sprintf(`
			%s as date,
			COUNT(*) as requests,
			SUM(user_cost) as cost
		`, dateFunc)).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Group(dateFunc).
		Order("date ASC").
		Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query user stats trend: %w", err)
	}

	billingRows := make([]BillingAggregate, 0, len(rows))
	for _, row := range rows {
		billingRows = append(billingRows, BillingAggregate{
			Date:         row.Date,
			RequestCount: row.Requests,
			UserCost:     row.Cost,
		})
	}

	// 曲线也要并上已固化的日汇总: 明细按留存期清理后, 半年前那段会整段从图上消失 ——
	// 用户看到的不是"更长的历史", 而是"去年比今年少了很多", 与账单页的曲线也从此不一致。
	//
	// 小时粒度是唯一例外: 汇总表按天存, 一天的聚合行无法还原"那一天里哪几个小时有量",
	// 硬并进来只能伪造出同一时刻的大额桶。而 hour 只用于单日区间(前端限制), 单日必然落在留存期内,
	// 明细侧本就完整, 所以小时粒度只读明细。两端的整日裁剪与不重复计数见 userStatsRollupBuckets。
	if granularity != "hour" {
		rollupRows, err := userStatsRollupBuckets(ctx, userID, start, end, granularity)
		if err != nil {
			return nil, err
		}
		// 同一周/月桶里可能既有明细日又有汇总日, mergeBillingAggregates 逐桶相加不会重复计数
		// (它按日期键合并, 而明细日与汇总日互不相交, 见其注释)。
		billingRows = mergeBillingAggregates(billingRows, rollupRows)
	}

	// 调用日志按同一套桶表达式聚合, 再按桶并入计价数据。
	outcomes, err := queryRequestOutcomeBuckets(ctx, userID, start, end, granularity)
	if err != nil {
		return nil, err
	}

	// 桶集合取两者的并集。只按计费行建桶会把"那天有调用但一次都没结算"(即全失败)
	// 整段从图上抹掉 —— 恰恰是最该被看到的一天。这类桶的调用量改取调用日志的终态计数:
	// 计费口径的 0 会被读成"当天没有调用", 而事实是调用全部失败了。
	result := make([]model.UserStatsTrend, 0, len(billingRows)+len(outcomes))
	byDate := make(map[string]int, len(billingRows)+len(outcomes))
	for _, row := range billingRows {
		byDate[row.Date] = len(result)
		result = append(result, model.UserStatsTrend{
			Date:     row.Date,
			Requests: row.RequestCount,
			Cost:     row.UserCost,
		})
	}
	for _, outcome := range outcomes {
		if i, ok := byDate[outcome.Date]; ok {
			entry := &result[i]
			entry.SuccessRate = outcome.successRate()
			entry.RequestSuccess = outcome.Success
			entry.RequestFailed = outcome.Failed
			entry.RequestCanceled = outcome.Canceled
			continue
		}
		byDate[outcome.Date] = len(result)
		result = append(result, model.UserStatsTrend{
			Date:            outcome.Date,
			Requests:        outcome.Success + outcome.Failed + outcome.Canceled,
			SuccessRate:     outcome.successRate(),
			RequestSuccess:  outcome.Success,
			RequestFailed:   outcome.Failed,
			RequestCanceled: outcome.Canceled,
		})
	}

	// 补进来的桶可能插在中间, 统一按日期排序以保证趋势线连续。
	sort.SliceStable(result, func(i, j int) bool { return result[i].Date < result[j].Date })
	return result, nil
}

// requestOutcome 是调用日志口径的结局计数。
//
// 只统计终态: running/committed 是仍在飞的请求, 既不算成功也不算失败, 必须排除;
// 被保留下来的是"服务端是否交付了响应"这个事实, 与计费是否产生无关。
type requestOutcome struct {
	Date     string // 仅按桶聚合时有值(YYYY-MM-DD)
	Success  int64
	Failed   int64
	Canceled int64
	// coverageStart 区间内真实存在调用日志的最早一天; 空串表示区间内没有日志
	// (查询窗口完全落在留存期之外, 或者这一段时间确实没有调用)。
	coverageStart string
	retentionDays int
}

// successRate 请求成功率 %: success/(success+failed)。
// canceled 不进分母: 客户端主动断开或超时取消是调用方行为, 不代表服务不可用 ——
// 把它算成失败会让"用户自己中断"拉低平台的可靠性指标。
// 分母为 0 时返回 0, 由调用方结合样本量决定显示"无数据"而不是 0%。
func (o requestOutcome) successRate() float64 {
	denominator := o.Success + o.Failed
	if denominator <= 0 {
		return 0
	}
	return float64(o.Success) * 100.0 / float64(denominator)
}

// RequestOutcome 区间内的请求结局计数与日志覆盖起点, 供只需要"成功/失败笔数"的读口使用。
//
// 单独导出而不是直接暴露内部的 requestOutcome: 内部结构带着 successRate 等统计页专用方法,
// 首页只需要三个计数与覆盖起点, 让调用方拿到它不该碰的东西只会诱使别处再算一套比率。
type RequestOutcome struct {
	Success  int64
	Failed   int64
	Canceled int64
	// CoverageStart 区间内真实存在调用日志的最早一天(YYYY-MM-DD); 空串表示区间内没有日志。
	// 调用日志按留存期滚动删除, 所以它通常远晚于区间起点 —— 调用方必须把它透出去,
	// 否则"区间内请求数"会被读成"从区间起点开始统计"。
	CoverageStart string
	RetentionDays int
}

// RequestOutcomeTotals 汇总某用户在某区间内的请求结局计数。
//
// 数据源是 relay_requests 而不是 billing_records, 理由见 queryRequestOutcome:
// 计费表只为产生了用量的请求落行, 失败请求在它里面没有行, 拿计费行数当成功数会恒等于 100%,
// 且失败数永远是 0。这里只是把内部查询的薄封装导出, 与统计页的概览/趋势**同一条件、
// 同一终态过滤**(status 分别取 success/failed/canceled), 两处口径不会分叉。
func RequestOutcomeTotals(ctx context.Context, userID uint, start, end time.Time) (RequestOutcome, error) {
	out, err := queryRequestOutcome(ctx, userID, start, end)
	if err != nil {
		return RequestOutcome{}, err
	}
	return RequestOutcome{
		Success:       out.Success,
		Failed:        out.Failed,
		Canceled:      out.Canceled,
		CoverageStart: out.coverageStart,
		RetentionDays: out.retentionDays,
	}, nil
}

// queryRequestOutcome 汇总区间内的请求结局计数与日志覆盖起点。
//
// 数据源刻意取 relay_requests: billing_records 只为"产生了用量"的请求落行,
// 失败与未产生用量的调用在计费表里没有行, 用它算成功率会恒等于 100%。
// 代价是调用日志按 LogRetentionDays 滚动删除, 所以必须把覆盖起点一起返回给前端。
func queryRequestOutcome(ctx context.Context, userID uint, start, end time.Time) (requestOutcome, error) {
	out := requestOutcome{retentionDays: LogRetentionDays()}
	var row struct {
		Success  int64
		Failed   int64
		Canceled int64
		Earliest string
	}
	// 覆盖起点取"最早一天的日期"而不是 MIN(started_at): SQLite 的时间列是文本,
	// 小数秒尾零被裁掉后('.85' 与 '.947')按字符串比较会错序; 归桶后的日期是定宽 YYYY-MM-DD,
	// 字典序与时间序一致。
	dayExpr := statsBucketExprOn(statsDialect(), "day", "started_at")
	err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequest{}).
		Select(fmt.Sprintf(`
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as success,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as failed,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as canceled,
			MIN(%s) as earliest
		`, dayExpr), relayStatusSuccess, relayStatusFailed, relayStatusCanceled).
		Where("user_id = ? AND started_at >= ? AND started_at < ?", userID, start, end).
		Scan(&row).Error
	if err != nil {
		return requestOutcome{}, fmt.Errorf("failed to query user request outcome: %w", err)
	}
	out.Success = row.Success
	out.Failed = row.Failed
	out.Canceled = row.Canceled
	out.coverageStart = statsDateOnly(row.Earliest)
	return out, nil
}

// queryRequestOutcomeBuckets 按日/周/月桶汇总区间内的请求结局, 供趋势图与概览同源。
func queryRequestOutcomeBuckets(ctx context.Context, userID uint, start, end time.Time, granularity string) ([]requestOutcome, error) {
	dateFunc := statsBucketExprOn(statsDialect(), granularity, "started_at")
	var rows []requestOutcome
	err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequest{}).
		Select(fmt.Sprintf(`
			%s as date,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as success,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as failed,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as canceled
		`, dateFunc), relayStatusSuccess, relayStatusFailed, relayStatusCanceled).
		Where("user_id = ? AND started_at >= ? AND started_at < ?", userID, start, end).
		Group(dateFunc).
		Order("date ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query user request outcome buckets: %w", err)
	}
	return rows, nil
}

// UserStatsModelBreakdown 查询用户调用按模型分组统计。
//
// 已知上限: 这里**只**读 billing_records 明细, 不并 billing_rollups —— 汇总表只有
// user/owner/channel/apikey/platform 五个维度, 没有 model 维度, 一天的金额被固化后
// 就无法再按模型拆开。也就是说超出留存期(默认 180 天)的区间里:
// 概览的 total_cost 有数(汇总补齐), 这里却查不到对应的模型行 —— 这是数据本身没保留, 不是漏查。
//
// 处理方式:
//   - 绝不按比例把汇总金额摊到现存模型上: 编出来的数字会被用户拿去对账, 比缺数据更有害。
//   - 覆盖起点由 UserStatsModelCoverageStart 单独返回, handler 用响应头告知前端"此日期之前无模型明细",
//     前端据此提示而不是把缺失读成 0 用量; 概览里同一语义的字段是 model_breakdown_coverage_start。
//
// 注意这里保留 status='settled' 过滤(退款行不计入模型拆分), 与概览的全额口径不同 ——
// TestUserStatsRefundKeepsBillingScope 锁的是"模型页只算已结算"这个既有行为, 不要顺手改。
func UserStatsModelBreakdown(ctx context.Context, userID uint, startDate, endDate string) ([]model.UserStatsModel, error) {
	start, end := parseStatsDateRange(startDate, endDate)

	var rows []struct {
		ModelName        string
		Requests         int64
		Cost             float64
		Tokens           int64
		InputTokens      int64
		CacheReadTokens  int64
		CacheWriteTokens int64
	}

	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(`
			model_name,
			COUNT(*) as requests,
			SUM(user_cost) as cost,
			SUM(input_token + output_token + cache_read_token + cache_write_token) as tokens,
			SUM(input_token) as input_tokens,
			SUM(cache_read_token) as cache_read_tokens,
			SUM(cache_write_token) as cache_write_tokens
		`).
		Where("user_id = ? AND created_at >= ? AND created_at < ? AND status = ?",
			userID, start, end, model.BillingStatusSettled).
		Group("model_name").
		Order("cost DESC").
		Limit(50). // 最多 50 个模型
		Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query user stats model breakdown: %w", err)
	}

	result := make([]model.UserStatsModel, len(rows))
	for i, row := range rows {
		result[i] = model.UserStatsModel{
			ModelName:        row.ModelName,
			Requests:         row.Requests,
			Cost:             row.Cost,
			Tokens:           row.Tokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			CacheHitRate:     cacheHitRate(row.CacheReadTokens, row.InputTokens),
		}
	}

	return result, nil
}

// parseStatsDateRange 解析统计日期区间; 空值默认最近 30 天。
func parseStatsDateRange(startDate, endDate string) (time.Time, time.Time) {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -30)

	if startDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", startDate, time.Local); err == nil {
			start = t
		}
	}
	if endDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", endDate, time.Local); err == nil {
			// endDate 是包含的最后一天, 查询到次日 00:00
			end = t.AddDate(0, 0, 1)
		}
	}

	return start, end
}

// ── 计费口径的「明细 ∪ 日汇总」读取 ──────────────────────────────────────────────
//
// 用户统计页与账单页(RETENTION 页)必须给同一个账户同一个区间同样的钱, 否则用户对账时无法解释差额。
// 账单页早已并上 billing_rollups(BillingTotal 的注释: 明细清理后终身口径只能靠汇总表补齐),
// 而统计页此前只读 billing_records —— 超过留存期(默认 180 天)的区间, 详细金额/词元/请求数会随清理
// 一天天变小, 且没有任何提示。下面这层就是把统计页也接到同一份数据上。
//
// 三条不变量(违反任何一条就会算错钱):
//  1. 汇总行只代表**整天**。窗口首尾若不是自然日边界, 那一天只能按明细算, 不能把整天的汇总行拉进来;
//     否则窗口外的半天也被计入(见 userStatsRollupDayRange)。
//  2. 明细与汇总**不会**同一天同时有数: 某天的明细被删之前, 那天可删的明细已整日固化进汇总表;
//     因申诉保护而留下的明细不进汇总(见 billingRecordDeletable 与 billingDayRollups 的读取条件)。
//     所以"明细 + 汇总"是加法而不是去重, mergeBillingAggregates 只需按日期键拼接。
//  3. 退款口径保持"含已退款行"的全额列: 统计页历来把 refunded 行算进总量与费用
//     (TestUserStatsRefundKeepsBillingScope 锁的就是这个行为), 故这里读 refundedOnly=false 的全额列,
//     不能改读净额, 否则同一份明细在两页之间反而对不上。
//
// 已知上限: 汇总表只有 user/owner/channel/apikey/platform 五个维度, **没有 model 维度**,
// 因此超出留存期的「按模型拆分」不可得(UserStatsModelBreakdown 仍只读明细, 并如实返回覆盖起点)。

// userStatsRollupDayRange 算出查询窗口内可安全使用日汇总的「整天」范围(闭区间日键 YYYYMMDD)。
// 返回空串表示窗口内没有任何一整天完全落在区间内(例如只查半天), 此时只能用明细。
//
// 为什么必须裁到整天: 汇总行是一整天的合计, 与半天的明细是不同粒度。窗口从某天正午开始时,
// 那天的汇总若整行计入, 就会把窗口外的前半天也算进总额 —— 用户看到的是"区间首日凭空多出一倍"。
// 处理方式是首日只信明细, 从下一个自然日起才允许用汇总; 尾部同理(end 为半开区间)。
//
// 这个取舍有一个已知代价, 必须写在这里而不是留给下一个人猜: 若窗口首日(或末日)的半天恰好是
// 早已被清理的一天, 那半天既拿不到明细(已删)又不敢用整天汇总(会把窗口外的量算进来), 于是少算。
// 宁可少算一个半天, 也不能多算: 多算出来的金额用户对账时找不到来源, 少算则能被"覆盖起点"提示解释。
// 逆推(把整天汇总按时间比例摊到半天)等于编造数字, 一律不做。
func userStatsRollupDayRange(start, end time.Time) (fromKey, toKey string) {
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return "", ""
	}
	// 起点: 正好在 00:00 才包含当天; 否则从次日开始(当天只有部分落在窗口内)。
	firstDay := dayStart(start)
	if firstDay.Before(start) {
		firstDay = firstDay.AddDate(0, 0, 1)
	}
	// 终点: end 不含, 故最后一个整天是 end 所在日的前一天。
	lastDay := dayStart(end).AddDate(0, 0, -1)
	if !firstDay.Before(dayStart(end)) || lastDay.Before(firstDay) {
		return "", ""
	}
	return dayKey(firstDay), dayKey(lastDay)
}

// userStatsBillingAggregate 读某用户在区间内「明细 ∪ 日汇总」的计费合计(含已退款行)。
//
// 明细侧仍是半开区间 created_at >= start AND created_at < end; 汇总侧按 userStatsRollupDayRange
// 裁到整天, 只补那段窗口内已经不存在的明细。两者相加即账单页 BillingTotal 的分区间版本。
func userStatsBillingAggregate(ctx context.Context, userID uint, start, end time.Time) (BillingAggregate, error) {
	live := BillingAggregate{}
	// 聚合交给数据库: 只取 8 个数字, 不把整行明细搬回内存(理由同 billing.go 的 BillingTotal)。
	// 零行时 SUM 返回 NULL, 必须 COALESCE 兜底, 否则扫 NULL 到 float64 会报错。
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select("COALESCE(SUM(input_token), 0) AS input_token,"+
			" COALESCE(SUM(output_token), 0) AS output_token,"+
			" COALESCE(SUM(cache_read_token), 0) AS cache_read_token,"+
			" COALESCE(SUM(cache_write_token), 0) AS cache_write_token,"+
			" COUNT(*) AS request_count,"+
			" COALESCE(SUM(user_cost), 0) AS user_cost,"+
			" COALESCE(SUM(owner_revenue), 0) AS owner_revenue,"+
			" COALESCE(SUM(platform_revenue), 0) AS platform_revenue").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Scan(&live).Error; err != nil {
		return BillingAggregate{}, fmt.Errorf("failed to query user stats billing aggregate: %w", err)
	}

	fromKey, toKey := userStatsRollupDayRange(start, end)
	if fromKey == "" {
		return live, nil
	}
	rollup, err := billingRollupTotalRange(ctx, model.BillingRollupUser, userID, false, fromKey, toKey)
	if err != nil {
		return BillingAggregate{}, fmt.Errorf("failed to query user stats rollup total: %w", err)
	}
	live.InputToken += rollup.InputToken
	live.OutputToken += rollup.OutputToken
	live.CacheReadToken += rollup.CacheReadToken
	live.CacheWriteToken += rollup.CacheWriteToken
	live.RequestCount += rollup.RequestCount
	live.UserCost += rollup.UserCost
	live.OwnerRevenue += rollup.OwnerRevenue
	live.PlatformRevenue += rollup.PlatformRevenue
	return live, nil
}

// userStatsRollupBuckets 读区间内已固化的日汇总, 按调用方给定粒度归桶(日/周/月), 供趋势曲线并集使用。
//
// 为什么不直接用 billingRollupAggregates: 它只截下界(since), 不裁尾也不裁首日的半天, 会把窗口外
// 的汇总算进来(见 userStatsRollupDayRange 的说明)。这里按日键闭区间精确取, 并在 Go 侧归桶 ——
// 汇总表一天最多 5 行, 一年也就一千多行, 取回成本可忽略, 不值得为此引一处分方言的日期拼接。
//
// 归桶规则必须与 statsBucketExprOn 完全一致(周桶取当周周一, 月桶取当月 1 日), 否则同一张图上
// "明细段"与"汇总段"会按不同的日期切片, 接缝处的桶对不上。
func userStatsRollupBuckets(ctx context.Context, userID uint, start, end time.Time, granularity string) ([]BillingAggregate, error) {
	// 小时桶没有对应的汇总粒度: 一天的汇总行无法还原该日内各小时的分布, 强行并进来只会伪造出
	// "那天所有量都堆在某个小时"的桶。hour 只用于单日区间(必然落在留存期内, 明细完整), 直接返回空。
	if granularity == "hour" {
		return nil, nil
	}
	fromKey, toKey := userStatsRollupDayRange(start, end)
	if fromKey == "" {
		return nil, nil
	}

	rows := make([]model.BillingRollup, 0)
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day >= ? AND day <= ?",
			model.BillingRollupUser, userID, fromKey, toKey).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to query user stats rollup buckets: %w", err)
	}

	byBucket := make(map[string]*BillingAggregate, len(rows))
	keys := make([]string, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		bucket, ok := userStatsBucketLabel(row.Day, granularity)
		if !ok {
			continue
		}
		agg, ok := byBucket[bucket]
		if !ok {
			agg = &BillingAggregate{Date: bucket}
			byBucket[bucket] = agg
			keys = append(keys, bucket)
		}
		agg.InputToken += row.InputToken
		agg.OutputToken += row.OutputToken
		agg.CacheReadToken += row.CacheReadToken
		agg.CacheWriteToken += row.CacheWriteToken
		agg.RequestCount += row.RequestCount
		agg.UserCost += row.UserCost
		agg.OwnerRevenue += row.OwnerRevenue
		agg.PlatformRevenue += row.PlatformRevenue
	}

	sort.Strings(keys)
	out := make([]BillingAggregate, 0, len(keys))
	for _, key := range keys {
		out = append(out, *byBucket[key])
	}
	return out, nil
}

// userStatsBucketLabel 把汇总表的日键 YYYYMMDD 换成统计曲线用的桶标签(YYYY-MM-DD)。
// 周桶取当周周一(与 SQL 侧 '-6 days','weekday 1' 同解: weeklyMondayOffset = (weekday+6)%7),
// 月桶取当月 1 日。日键长度不为 8 的行视为脏数据跳过。
func userStatsBucketLabel(day, granularity string) (string, bool) {
	if len(day) != 8 {
		return "", false
	}
	parsed, err := time.ParseInLocation("20060102", day, time.Local)
	if err != nil {
		return "", false
	}
	switch granularity {
	case "week":
		return parsed.AddDate(0, 0, -((int(parsed.Weekday()) + 6) % 7)).Format("2006-01-02"), true
	case "month":
		return parsed.Format("2006-01") + "-01", true
	default:
		return parsed.Format("2006-01-02"), true
	}
}

// userStatsBillingCoverage 返回「明细 ∪ 汇总」在查询区间内实际可见的账单覆盖起点,
// 以及汇总表在该用户上已固化到的最晚一天(均为 YYYY-MM-DD, 空串表示没有)。
//
// 为什么要把这条边界返回给前端: 统计页现在能显示留存期之外的金额了, 但那份金额来自日汇总,
// 只有金额/词元/请求数, 没有按模型拆分也没有原始明细。前端必须能提示
// "此区间早于 X 的部分来自每日汇总, 不可下钻到单笔", 否则用户点开明细会发现"金额有、记录没有"。
// 同时, 汇总表只覆盖"首次清理之后固化过的那些天" —— 若账户的数据在汇总机制上线前就被清理过,
// 那段既没有明细也没有汇总, 谁都不补回来(这点必须让用户看见, 而不是被读成平台少算)。
func userStatsBillingCoverage(ctx context.Context, userID uint, start, end time.Time) (coverageStart string, rollupCoveredUntil string, modelCoverageStart string, err error) {
	// 明细侧: 区间内最早一天。归桶后取日期(不取 MIN(created_at), 见 queryRequestOutcome 的顺序说明)。
	dayExpr := statsBucketExprOn(statsDialect(), "day", "created_at")
	var live struct {
		Earliest string
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select("COALESCE(MIN("+dayExpr+"), '') AS earliest").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Scan(&live).Error; err != nil {
		return "", "", "", fmt.Errorf("failed to query user stats billing coverage: %w", err)
	}

	fromKey, toKey := userStatsRollupDayRange(start, end)
	var rollup struct {
		Earliest string
		Latest   string
	}
	if fromKey != "" {
		if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
			Select("COALESCE(MIN(day), '') AS earliest, COALESCE(MAX(day), '') AS latest").
			Where("dimension = ? AND dimension_id = ? AND day >= ? AND day <= ?",
				model.BillingRollupUser, userID, fromKey, toKey).
			Scan(&rollup).Error; err != nil {
			return "", "", "", fmt.Errorf("failed to query user stats rollup coverage: %w", err)
		}
	}

	liveStart := statsDateOnly(live.Earliest)
	rollupStart := userStatsDayToDate(rollup.Earliest)
	switch {
	case liveStart == "":
		coverageStart = rollupStart
	case rollupStart == "":
		coverageStart = liveStart
	case rollupStart < liveStart:
		coverageStart = rollupStart
	default:
		coverageStart = liveStart
	}
	rollupCoveredUntil = userStatsDayToDate(rollup.Latest)
	// 按模型拆分只有明细能算(汇总表没有 model 维度), 所以它的覆盖起点就是区间内最早的一条明细。
	// 返回这条边界而不是假装全覆盖: 早于它的金额有数、按模型的拆分无数据, 前端据此提示而不是画成 0。
	modelCoverageStart = liveStart
	return coverageStart, rollupCoveredUntil, modelCoverageStart, nil
}

// userStatsDayToDate 把汇总表的日键 YYYYMMDD 归一成 YYYY-MM-DD; 非法长度返回空串。
func userStatsDayToDate(day string) string {
	if len(day) != 8 {
		return ""
	}
	return day[:4] + "-" + day[4:6] + "-" + day[6:]
}

// UserStatsModelCoverageStart 返回「按模型拆分」在查询区间内的覆盖起点(YYYY-MM-DD, 空串表示无明细),
// 供 handler 在 models 响应里如实标注数据边界。
//
// 为什么单独暴露这条边界: 汇总表只有 user/owner/channel/apikey/platform 五个维度, **没有 model 维度**,
// 所以超过留存期的按模型拆分**不可得** —— 这不是没做, 是数据本身没保留。可得的只有区间内最早一天的
// 明细; 早于它那段的金额仍在概览里(由汇总补), 但拆分不出来。前端据此提示"此区间之前无模型明细",
// 千万不要用"按比例摊到现有模型"之类的办法伪造数据, 那会让用户拿着假数字去对账。
func UserStatsModelCoverageStart(ctx context.Context, userID uint, startDate, endDate string) (string, error) {
	start, end := parseStatsDateRange(startDate, endDate)
	_, _, modelCoverageStart, err := userStatsBillingCoverage(ctx, userID, start, end)
	if err != nil {
		return "", err
	}
	return modelCoverageStart, nil
}

// statsDialect 返回当前连接的数据库方言(sqlite/mysql/postgres)。
func statsDialect() string {
	if d := db.GetDB().Dialector; d != nil {
		return d.Name()
	}
	return "sqlite"
}

// statsBucketExpr 按方言构造把 created_at 归桶到日/周/月的 SQL 表达式, 结果统一为 YYYY-MM-DD 字符串;
// 周桶以周一为一周起点, 标签是当周周一。
//
// SQLite: created_at 以带写入偏移的 RFC3339 文本存储(如 2026-01-05T00:30:00+08:00),
// date 系列函数会先归一到 UTC 再做日历运算 —— 周一凌晨的记录此时 UTC 还是周日,
// 旧的 'weekday 0','-6 days' 会把它并进上一周, 日/月分组同样会偏移一天/一月。
// 这里先用 SUBSTR 取写入的挂钟日期前缀(与 BillingDaily 在 Go 侧按
// record.CreatedAt.Format 归日的口径一致, 且不受机器时区影响), 再做日期运算;
// 周用 '-6 days','weekday 1': weekday 1 前移到下周一但落在周一时不前移, 减 6 天后即当周周一。
// MySQL: DATETIME 按 loc=Local 写入本地挂钟, WEEKDAY() 0=周一, 直接减即周一。
// PostgreSQL: timestamptz 无法保留写入偏移, 按会话 TimeZone 截断
// (DSN 建议带 TimeZone=<部署时区> 使挂钟口径与 BillingDaily 一致), date_trunc('week') 即周一起。
func statsBucketExpr(dialect, granularity string) string {
	return statsBucketExprOn(dialect, granularity, "created_at")
}

// statsBucketExprOn 是 statsBucketExpr 的列名参数化版本: 成功率的记账列是
// relay_requests.started_at, 与计费表的 created_at 不同, 但归桶规则必须完全一致,
// 否则趋势图上"请求量"与"成功率"两条序列会按不同的日期切片。
func statsBucketExprOn(dialect, granularity, column string) string {
	switch dialect {
	case "mysql":
		switch granularity {
		case "hour":
			// 小时桶: 仅"今天"这种短区间使用; 跨天区间会产出同刻重复桶, 前端禁掉这个组合。
			return "DATE_FORMAT(" + column + ", '%Y-%m-%d %H:00')"
		case "week":
			return "DATE_FORMAT(" + column + " - INTERVAL WEEKDAY(" + column + ") DAY, '%Y-%m-%d')"
		case "month":
			return "DATE_FORMAT(" + column + ", '%Y-%m-01')"
		default:
			return "DATE_FORMAT(" + column + ", '%Y-%m-%d')"
		}
	case "postgres", "postgresql":
		switch granularity {
		case "hour":
			return "TO_CHAR(DATE_TRUNC('hour', " + column + "), 'YYYY-MM-DD HH24:00')"
		case "week":
			return "TO_CHAR(DATE_TRUNC('week', " + column + "), 'YYYY-MM-DD')"
		case "month":
			return "TO_CHAR(DATE_TRUNC('month', " + column + "), 'YYYY-MM-DD')"
		default:
			return "TO_CHAR(DATE_TRUNC('day', " + column + "), 'YYYY-MM-DD')"
		}
	default: // sqlite
		switch granularity {
		case "hour":
			// 小时桶必须切**挂钟前缀**, 不能交给 STRFTIME: SQLite 的 date 系列函数会先把带偏移的
			// 时间串归一到 UTC 再做日历运算。库里存的是 "2026-11-21 10:16:00+08:00", STRFTIME 会
			// 算成 02:00(本地 07:30 的请求甚至落到前一天 23:00), 整条 X 轴偏 8 小时, 而同日桶走
			// SUBSTR 保留挂钟 —— 两张图会按不同时区切片, 互相矛盾。
			// SUBSTR(1,13) 取到分钟, 空格分隔与 T 分隔两种驱动形态都恰好截在 "HH:MM",
			// REPLACE 把 T 归一成空格, 再补 ":00" 得到 'YYYY-MM-DD HH:00'。
			return "REPLACE(SUBSTR(" + column + ", 1, 13), 'T', ' ') || ':00'"
		case "week":
			return "DATE(SUBSTR(" + column + ", 1, 10), '-6 days', 'weekday 1')"
		case "month":
			return "DATE(SUBSTR(" + column + ", 1, 10), 'start of month')"
		default:
			return "DATE(SUBSTR(" + column + ", 1, 10))"
		}
	}
}

// cacheHitRate 计算缓存命中率 %: cacheRead/(cacheRead+input)。
// 分母不含 output —— 输出天然不命中缓存, 把它算进去会让命中率普遍偏低且不可横向比较。
// 分母为 0 时返回 0, 避免出现 0/0 的 NaN 传到前端。
func cacheHitRate(cacheRead, input int64) float64 {
	denominator := cacheRead + input
	if denominator <= 0 {
		return 0
	}
	return float64(cacheRead) * 100.0 / float64(denominator)
}

// UserStatsLatency 查询用户调用延迟指标。
//
// 数据源刻意选 relay_requests 而非 billing_records: 耗时字段(首字/流式/总耗时/上游 TTFB)
// 只存在于调用日志里, 计费表只有金额与词元。代价是调用日志按留存期滚动删除,
// 查询窗口可能只有末尾几天有数据 —— 所以返回值必须带 CoverageStart 与 RetentionDays,
// 让前端把"只有最近 3 天数据"讲清楚, 而不是拿 3 天均值冒充 30 天结论。
func UserStatsLatency(ctx context.Context, userID uint, startDate, endDate string) (model.UserStatsLatency, error) {
	start, end := parseStatsDateRange(startDate, endDate)

	var rows []struct {
		SampleCount       int64
		AvgFirstTokenMs   float64
		AvgDurationMs     float64
		AvgUpstreamTTFBMs float64
	}
	// 首字耗时口径: 流式用 FirstTokenDurationNs, 非流式用 ResponseDurationNs。
	// 两者互斥(流式请求的非流式字段为零), 相加即为该请求的首字耗时。
	// 只统计成功请求: 失败请求的耗时是被取消/报错截断的, 混进来会把延迟算得偏低。
	err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequest{}).
		Select(`
			COUNT(*) as sample_count,
			COALESCE(AVG(first_token_duration_ns + response_duration_ns), 0) / 1000000.0 as avg_first_token_ms,
			COALESCE(AVG(duration_ns), 0) / 1000000.0 as avg_duration_ms,
			COALESCE(AVG(ttfb_ns), 0) / 1000000.0 as avg_upstream_ttfb_ms
		`).
		Where("user_id = ? AND status = ? AND started_at >= ? AND started_at < ?",
			userID, relayStatusSuccess, start, end).
		Scan(&rows).Error
	if err != nil {
		return model.UserStatsLatency{}, fmt.Errorf("failed to query user stats latency: %w", err)
	}

	result := model.UserStatsLatency{RetentionDays: LogRetentionDays()}
	if len(rows) == 0 || rows[0].SampleCount == 0 {
		return result, nil
	}
	row := rows[0]
	result.SampleCount = row.SampleCount
	result.AvgFirstTokenMs = row.AvgFirstTokenMs
	result.AvgDurationMs = row.AvgDurationMs
	result.AvgUpstreamTTFBMs = row.AvgUpstreamTTFBMs

	// P95 单独查: 通用 ORM 没有分位函数, 这里用"偏移一个序号再取值"的写法,
	// 三种方言下都用 OFFSET 表达, 避免为此引入方言分支。
	// 最近秩法(nearest-rank): 第 ceil(0.95*N) 个样本即 P95, 偏移量从 0 起算。
	// 用 rank-1 的写法在 N 很小时会退化成 P50 甚至更低, 单用户日志样本普遍不多, 必须按定义算。
	var p95 struct{ Value float64 }
	offset := int64(math.Ceil(0.95*float64(row.SampleCount))) - 1
	if offset < 0 {
		offset = 0
	}
	if err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequest{}).
		Select("COALESCE(first_token_duration_ns + response_duration_ns, 0) / 1000000.0 as value").
		Where("user_id = ? AND status = ? AND started_at >= ? AND started_at < ?",
			userID, relayStatusSuccess, start, end).
		Order("(first_token_duration_ns + response_duration_ns) ASC").
		Offset(int(offset)).
		Limit(1).
		Scan(&p95).Error; err == nil {
		result.P95FirstTokenMs = p95.Value
	}

	// 覆盖起点: 真实有数据的最早一天。调用日志被清理过时它晚于查询起点, 前端据此提示。
	var earliest struct{ Value string }
	if err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequest{}).
		Select("MIN(started_at) as value").
		Where("user_id = ? AND status = ? AND started_at >= ? AND started_at < ?",
			userID, relayStatusSuccess, start, end).
		Scan(&earliest).Error; err == nil {
		result.CoverageStart = statsDateOnly(earliest.Value)
	}
	return result, nil
}

// statsDateOnly 把各驱动的起始时间值归一成 YYYY-MM-DD; 无法解析时返回空串。
func statsDateOnly(raw string) string {
	if raw == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if len(raw) >= 10 {
		return raw[:10]
	}
	return ""
}

// 调用日志的终态字面量。op 包不能引 relay 包(会形成循环依赖), 这里按持久化值比较。
// 与 internal/relay/state.go 的 StatusSuccess/StatusFailed/StatusCanceled 一一对应。
const (
	relayStatusSuccess  = "success"  // 响应已完整交付客户端。
	relayStatusFailed   = "failed"   // 请求以错误结束。
	relayStatusCanceled = "canceled" // 客户端提前断开或取消。
)
