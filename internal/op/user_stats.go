package op

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
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
	// 窗口边界按用户日历解释(见 parseStatsDateRange), 而日/周/月分组与留存仍是服务器日历。
	// 两套日历的差异在下面 5 个时区字段里如实披露, 不靠调用方猜。
	// loc 与 tzSource 取自同一次计算: 响应标称的时区必须就是实际算窗口用的那个。
	loc, tzSource := statsUserTimezone(userID)
	start, end := parseStatsDateRange(startDate, endDate, loc)

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

	// 时区口径字段: 一次性算齐(同一个"当前时刻"), 免得两个偏移各自取时撞上 DST 切换那一刻。
	tz := statsTimezones(loc, tzSource, time.Now())

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

		// 时区口径: 5 个字段一次性算齐, 与上面实际使用的 loc 同源, 不许另算一遍 ——
		// 两处各判一次时区就会出现"标着 user 却用服务器窗口"的响应, 且不会有任何报错。
		StatsTimezone:                      tz.windowName,
		StatsTimezoneOffsetMinutes:         tz.windowOffsetMinutes,
		StatsTimezoneSource:                tz.source,
		StatsCalendarTimezone:              tz.calendarName,
		StatsCalendarTimezoneOffsetMinutes: tz.calendarOffsetMinutes,
	}, nil
}

// UserStatsTrend 查询用户调用趋势（按日/周/月聚合）。
// granularity 为空时默认按天。
// 与概览同源分层: Requests/Cost 来自计费行(含 refunded), SuccessRate 来自调用日志。
// 两个源分别聚合后按桶合并: 超出日志留存期的桶只有计费数据, 成功率留 0 由前端断线, 不能补 100。
// 周桶以周一为一周起点, Date 标签为当周周一(YYYY-MM-DD)。
func UserStatsTrend(ctx context.Context, userID uint, startDate, endDate, granularity string) ([]model.UserStatsTrend, error) {
	// 区间边界与概览同一套时区, 否则四个接口会各自解释同一个 start_date, 前端把两组数字并排一放就矛盾。
	loc := statsUserLocation(userID)
	start, end := parseStatsDateRange(startDate, endDate, loc)
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

	// 排序**之后**再把小时标签换算到用户时区: 排序依赖标签的字典序=时间序, 而换算后的标签
	// 在 DST 切换那周可能与相邻桶逆序(本地那一小时重复)。先排序再换算, 顺序仍由服务器挂钟决定,
	// 曲线不会因为夏时制而出现"时间倒流"。
	//
	// 日/周/月标签不动: 它们的桶与 billing_rollups 的服务器自然日同源(见 UserStatsOverview 的
	// stats_calendar_timezone), 换到用户日历会让"统计页金额 = 账单页金额"这条约束失效。
	// 小时桶是唯一例外, 因为它不参与任何汇总/留存口径, 只是单日区间里的 X 轴刻度。
	if granularity == "hour" {
		for i := range result {
			result[i].Date = statsHourLabelIn(result[i].Date, loc)
		}
	}
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

// UserStatsModelBreakdown 查询用户调用按模型分组统计, 口径是「明细 ∪ model 日汇总」。
//
// 为什么必须并上汇总(迁移 45 之前的形态是只读明细): 明细按留存期(默认 180 天)逐日清理,
// 只读明细时, 超出留存期的区间里概览的 total_cost 有数(由 user 维度汇总补齐), 这里却一行都查不到 ——
// 用户看到"总金额有, 按模型的拆分没了", 而且没有任何字段能解释这个缺口。
// 汇总表新增 model 维度后, 两者才活得一样久。
//
// 合并规则(与 userStatsBillingAggregate 同一套不变量, 违反任一条就会算错钱):
//   - 只在**整天**上并汇总: 窗口首尾若不是自然日边界, 那一天只按明细算(见 userStatsRollupDayRange)。
//     否则窗口外的半天会被整天的汇总行带进来。
//   - 明细与 model 汇总**不会包含同一笔**明细: 某天的明细被删之前, 那天"可删的"明细已整日固化;
//     因未驳回申诉留下的明细不进汇总, 所以两段可以相加, 是加法而不是去重。
//   - 按模型分桶**不跨天求和之外的任何折算**: 逐日桶相加, 不按比例摊、不把某天的汇总摊给现存模型。
//
// 口径仍是 status='settled'(退款行不计入模型拆分), 与概览的全额口径不同 ——
// TestUserStatsRefundKeepsBillingScope 锁的是"模型页只算已结算"这个既有行为, 不要顺手改。
// 为了在明细被删之后仍然精确, model 维度的汇总行**只累加 settled 行**(见 billingDayRollups 里
// 对 model 维度的判断): 汇总行在 model 维度上的 refunded_* 列恒为 0, 因为退款行本来就不进模型拆分。
// 若改成按"合计"固化, 明细一删, 同一区间会突然多出一笔退款额 —— 按模型费用会在留存期边界上跳变。
//
// 已知上限(数据本身没保留, 不是漏查): 迁移 45 之前就已固化的日子没有 model 维度行, 那段的按模型
// 拆分补不回来。覆盖起点由 UserStatsModelCoverageStart 如实返回, handler 用响应头告知前端
// "此日期之前无模型数据", 前端据此提示而不是把缺失读成 0 用量(概览里同语义字段是
// model_breakdown_coverage_start)。绝不按比例摊: 编出来的数字会被用户拿去对账, 比缺数据更有害。
func UserStatsModelBreakdown(ctx context.Context, userID uint, startDate, endDate string) ([]model.UserStatsModel, error) {
	loc := statsUserLocation(userID)
	start, end := parseStatsDateRange(startDate, endDate, loc)

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
		Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query user stats model breakdown: %w", err)
	}

	// 两侧用同一个累加器累加后再产出结果: 明细侧与汇总侧的口径不同(SQL 聚合 vs 汇总列),
	// 但"按模型"要的四个数字是一样的, 用中间结构才能保证两边的加法完全对称。
	byModel := make(map[string]*userStatsModelAcc, len(rows))
	keys := make([]string, 0, len(rows))
	ref := func(name string) *userStatsModelAcc {
		entry, ok := byModel[name]
		if !ok {
			entry = &userStatsModelAcc{name: name}
			byModel[name] = entry
			keys = append(keys, name)
		}
		return entry
	}
	for _, row := range rows {
		entry := ref(row.ModelName)
		entry.requests += row.Requests
		entry.cost += row.Cost
		entry.tokens += row.Tokens
		entry.cacheRead += row.CacheReadTokens
		entry.cacheWrite += row.CacheWriteTokens
		// 明细侧 SQL 直接把 input_token 求和带回来了, 不必再减。
		entry.input += row.InputTokens
	}

	// 汇总侧: 只取整个自然日都落在窗口内的那段, 且只取 model 维度(其余维度不是"按模型")。
	// 明细与汇总同一天不会同时有数(见函数注释), 所以这里是加法而不是去重。
	fromKey, toKey := userStatsRollupDayRange(start, end)
	if fromKey != "" {
		rollupRows := make([]model.BillingRollup, 0)
		if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
			Where("dimension = ? AND dimension_id = ? AND day >= ? AND day <= ?",
				model.BillingRollupModel, userID, fromKey, toKey).
			Find(&rollupRows).Error; err != nil {
			return nil, fmt.Errorf("failed to query user stats model rollups: %w", err)
		}
		for i := range rollupRows {
			row := &rollupRows[i]
			entry := ref(row.ModelName)
			entry.requests += row.RequestCount
			entry.cost += row.UserCost
			entry.tokens += row.InputToken + row.OutputToken + row.CacheReadToken + row.CacheWriteToken
			entry.cacheRead += row.CacheReadToken
			entry.cacheWrite += row.CacheWriteToken
			// 汇总行同样有 input_token 列(与明细同名列同语义), 直接累加即可, 不必用
			// "总量减其余三项"去还原 —— 那个还原虽然也精确, 但多一层推导就多一处可能算错的地方。
			entry.input += row.InputToken
		}
	}

	sort.Strings(keys)
	result := make([]model.UserStatsModel, 0, len(keys))
	for _, key := range keys {
		entry := byModel[key]
		result = append(result, model.UserStatsModel{
			ModelName:        entry.name,
			Requests:         entry.requests,
			Cost:             entry.cost,
			Tokens:           entry.tokens,
			CacheReadTokens:  entry.cacheRead,
			CacheWriteTokens: entry.cacheWrite,
			// 命中率在两侧都加完之后才算: 中途算出来的值会被后续累加作废。
			CacheHitRate: cacheHitRate(entry.cacheRead, entry.input),
		})
	}
	// Cost 排序与既有行为一致(前端按费用从高到低展示); 同额时按模型名保证稳定顺序。
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Cost != result[j].Cost {
			return result[i].Cost > result[j].Cost
		}
		return result[i].ModelName < result[j].ModelName
	})
	if len(result) > userStatsModelBreakdownLimit {
		result = result[:userStatsModelBreakdownLimit]
	}
	return result, nil
}

// userStatsModelAcc 是"按模型"两侧合并时的中间累加器。
// 不复用 model.UserStatsModel 的理由: 那个结构没有 input_token 字段(它靠 CacheHitRate 呈现),
// 而命中率的分母在合并过程中必须一直带着; 硬塞进导出结构等于为了内部实现改 API 形状。
type userStatsModelAcc struct {
	name       string
	requests   int64
	cost       float64
	tokens     int64
	input      int64
	cacheRead  int64
	cacheWrite int64
}

// userStatsModelBreakdownLimit 是"按模型"结果的条数上限(与改造前一致)。
// 保留上限的理由不是性能: 前端那张表超过 50 行就没有可读性, 且长尾模型多半来自
// 拼接出来的脏模型名。截断只影响展示, 不影响上面已经算完的合计。
const userStatsModelBreakdownLimit = 50

// statsUserLocation 返回该用户统计用的时区; 未设置或非法时回落到服务器时区。
//
// 「未设置」有两种等价形态: 偏好为空串(老账号与从未改过的账号), 或偏好值不是一个能被
// time.LoadLocation 解析的 IANA 名。两种都必须安静回落到服务器时区而不是报错:
// 一个存坏的偏好值不该让整个统计页 500 —— 那会把"账号设置有问题"变成"数据看不了"。
//
// 注意这里**只**用于窗口边界与小时标签; 日/周/月分组、日汇总与留存日历一律仍是服务器时区
// (见 parseStatsDateRange 与 UserStatsOverview 的时区字段说明)。
func statsUserLocation(userID uint) *time.Location {
	loc, _ := statsUserTimezone(userID)
	return loc
}

// statsUserTimezone 一次算出窗口该用的时区**与**它的来源, 让"用了哪个时区"和"标称哪个来源"
// 不可能分叉 —— 两处各判一次时区(一处 TrimSpace 一处不 Trim, 一处查 LoadLocation 一处不查)
// 就会出现"标着 user 却用服务器窗口"的响应: 前端据此不提示, 而窗口其实是错的,
// 这种错不报错、不 panic, 只让用户看到一个月的账对不上。
func statsUserTimezone(userID uint) (*time.Location, string) {
	name := strings.TrimSpace(UserTimezoneByID(userID))
	if name == "" {
		return time.Local, statsTimezoneSourceServer
	}
	loc, err := time.LoadLocation(name)
	if err != nil || loc == nil {
		// 存坏了(拼错、写了个偏移串、或该版本 tzdata 里没有这个区)一律回落, 不报错。
		return time.Local, statsTimezoneSourceServer
	}
	return loc, statsTimezoneSourceUser
}

// statsTimezoneFacts 是概览响应里那 5 个时区字段的计算结果。
//
// 打包成一个结构而不是 5 个各自计算的函数: 偏移必须来自**同一个当前时刻**(DST 切换那一刻
// 前后偏移会变, 分两次取 time.Now 就会给出"名字不同但偏移本该相等"的假差异),
// 而且 5 个字段必须与窗口实际使用的 loc 同源, 否则前端会收到"标着 user 却是服务器窗口"的响应。
type statsTimezoneFacts struct {
	windowName            string // 窗口边界所用的时区名。
	windowOffsetMinutes   int    // 该时区在 at 时刻的 UTC 偏移分钟数。
	calendarName          string // 日/周/月分组与日汇总所用(服务器)时区名。
	calendarOffsetMinutes int    // 服务器时区在 at 时刻的 UTC 偏移分钟数。
	source                string // "user" / "server"。
}

// statsTimezones 算出概览要披露的时区事实。
//
// loc 与 source 必须是调用方实际用于解析窗口的那一对(取自同一个 statsUserTimezone 返回值),
// 不是这里另查一遍 —— 否则响应里的"是什么时区"与"实际按哪个时区算"会分叉,
// 而前端据此判断是否提示, 于是错的窗口被静悄悄地说成"没问题"。
func statsTimezones(loc *time.Location, source string, at time.Time) statsTimezoneFacts {
	if loc == nil {
		loc = time.Local
		source = statsTimezoneSourceServer
	}
	facts := statsTimezoneFacts{
		windowName:            statsLocationName(loc),
		windowOffsetMinutes:   statsOffsetMinutes(loc, at),
		calendarName:          serverTimezoneName(),
		calendarOffsetMinutes: statsOffsetMinutes(time.Local, at),
		source:                source,
	}
	// 回落到服务器时区时, 窗口名必须与日历名字面一致: 前端有"两个名字相同 ⇒ 一定不提示"的判据,
	// 这里若填了 Local 的偏移名而日历填的是 IANA 名, 偏移相同却名字不同, 会触发毫无意义的提示。
	if source == statsTimezoneSourceServer {
		facts.windowName = facts.calendarName
	}
	return facts
}

// statsLocationName 返回时区名: 能用 IANA 名就用, 否则退化为 "UTC+08:00" 形态, 绝不返回 "Local"。
func statsLocationName(loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	if name := loc.String(); name != "" && name != "Local" {
		return name
	}
	return serverTimezoneFallbackName(loc)
}

// statsTimezoneSource 返回窗口边界所用时区是"用户选的"还是"跟随服务器";
// 判据与 statsUserTimezone 完全同一份实现(故不会分叉), 仅供测试与调用方自检使用。
func statsTimezoneSource(userID uint) string {
	_, source := statsUserTimezone(userID)
	return source
}

const (
	statsTimezoneSourceUser   = "user"   // 用户偏好生效: 窗口边界按用户本地日历解释。
	statsTimezoneSourceServer = "server" // 偏好为空/非法: 窗口边界跟随服务器时区。
)

// serverTimezoneName 返回服务器时区名: TZ 环境变量优先, 否则读 /etc/localtime 软链取 IANA 名,
// 再退化为 "UTC+08:00" 形态; 绝不返回 "Local"(它不是一个时区名, 无法在另一台机器上复现)。
func serverTimezoneName() string {
	// TZ 显式指定时它就是进程实际生效的时区(Go 的 time.Local 会先看它)。
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
			return tz
		}
	}

	// /etc/localtime 通常是 zoneinfo 的软链, 软链路径末段即 IANA 名(也可能是 /usr/share/zoneinfo/... )。
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if name, ok := zoneinfoNameFromPath(target); ok {
			return name
		}
	}
	// 有些发行版把时区名写在这里(/etc/localtime 是拷贝而非软链时只能靠它)。
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(raw)); name != "" {
			if loc, err := time.LoadLocation(name); err == nil && loc != nil {
				return name
			}
		}
	}

	// 都拿不到(或时区是写死的偏移)时退化为偏移名: "Local" 不可复现, 偏移名至少能自证。
	return serverTimezoneFallbackName(time.Local)
}

// zoneinfoNameFromPath 从 zoneinfo 路径里取出 IANA 名。
// 只接受在最后一个 "zoneinfo/" 之后的整段(如 /usr/share/zoneinfo/Asia/Shanghai → Asia/Shanghai),
// 前缀部分可能是任何安装路径, 不去猜。返回的文件名不在 IANA 形态时 ok=false。
func zoneinfoNameFromPath(target string) (string, bool) {
	const marker = "zoneinfo/"
	idx := strings.LastIndex(target, marker)
	if idx < 0 {
		return "", false
	}
	name := strings.Trim(target[idx+len(marker):], "/")
	if name == "" || strings.Contains(name, "..") {
		return "", false
	}
	// 必须真的能被解析, 否则它只是个文件名而不是时区名(zone.tab/learcut 之类会落进来)。
	if loc, err := time.LoadLocation(name); err != nil || loc == nil {
		return "", false
	}
	return name, true
}

// serverTimezoneFallbackName 把时区退化成 "UTC+08:00"/"UTC-05:30" 形态的名字; 拿不到偏移时返回 "UTC"。
func serverTimezoneFallbackName(loc *time.Location) string {
	if loc == nil {
		return "UTC"
	}
	_, offsetSeconds := time.Now().In(loc).Zone()
	if offsetSeconds == 0 {
		return "UTC"
	}
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours, minutes := offsetSeconds/3600, (offsetSeconds%3600)/60
	return fmt.Sprintf("UTC%s%02d:%02d", sign, hours, minutes)
}

// statsOffsetMinutes 返回时区在当前时刻的 UTC 偏移分钟数(东正西负)。
//
// 用一个公共的"当前时刻"取偏移而不是各算一遍: DST 切换前后同一天里偏移会变,
// 响应里两个偏移必须来自同一时刻才能比较, 否则会出现"名字不同但偏移本应相等"的假提示。
func statsOffsetMinutes(loc *time.Location, at time.Time) int {
	if loc == nil {
		return 0
	}
	_, offsetSeconds := at.In(loc).Zone()
	return offsetSeconds / 60
}

// parseStatsDateRange 解析统计日期区间; 空值默认最近 30 天。
//
// loc 是**窗口边界**的日历(通常是用户时区, 见 statsUserLocation), 于是用户说的「11-21」被解释成
// 他本地 11-21 00:00 → 11-22 00:00, 而不是服务器的那 24 小时。
//
// 但返回值在返回前统一 .In(time.Local): 下游的整日裁剪(userStatsRollupDayRange)、日键生成(dayKey)
// 与 SQL 比较全部建立在服务器挂钟上 —— 尤其 SQLite 按挂钟文本比较时间串。传一个别的时区下去
// 会让边界与既有的整日裁剪错位, 反而制造出"半个服务器日"的错算窗口。
// 换算成同一瞬时、同一服务器挂钟表示之后, 下游代码一行都不用改。
//
// 「最近 30 天」的默认值也按 loc 的今天算: 用户在自己时区里说的"今天"不该由服务器日历决定。
func parseStatsDateRange(startDate, endDate string, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -30)

	if startDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", startDate, loc); err == nil {
			start = t
		}
	}
	if endDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", endDate, loc); err == nil {
			// endDate 是包含的最后一天, 查询到次日 00:00
			end = t.AddDate(0, 0, 1)
		}
	}

	return start.In(time.Local), end.In(time.Local)
}

// statsHourLabelIn 把服务器挂钟的小时桶标签(YYYY-MM-DD HH:MM)换算到 loc 时区。
//
// 只换算展示标签, 不重算分桶: 桶本身是服务器挂钟的整点切片, 平移到用户时区后
// 起点可能是 :30/:45(如 Asia/Kathmandu +05:45), 此时如实显示分钟位, 不要四舍五入 ——
// 四舍五入会让标签与它代表的那个桶错位。
//
// DST 切换周里可能出现两个桶得到同一标签: 这是真实的(本地那一小时重复了),
// 照实保留, 不要因此去改桶或去重(去重会吞掉一个小时的量)。
// 解析失败时原样返回: 标签是展示用的, 宁可显示旧标签也不要把整条曲线丢掉。
func statsHourLabelIn(label string, loc *time.Location) string {
	if loc == nil {
		return label
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04", label, time.Local)
	if err != nil {
		return label
	}
	return parsed.In(loc).Format("2006-01-02 15:04")
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
// 已知上限: 这层(概览/趋势)读的是 user 维度, 与「按模型」是两条独立的合并线 ——
// 按模型那条走 model 维度(迁移 45 之后才有), 且只在 settled 口径上合并, 见 UserStatsModelBreakdown。
// 两条线的覆盖起点不同(模型维度上线晚于 user 维度), 不能互相推断, 也不能把 user 的边界当模型的边界。

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
// 汇总表一天最多几行(每个维度一行), 一年也就一千多行, 取回成本可忽略, 不值得为此引一处分方言的日期拼接。
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

	// 按模型的覆盖起点要单独看 model 维度: 它和 user 维度不是同时上线的 —— 迁移 45 之前固化的
	// 日子只有 user 维度行, 那些天的"总额"有数、"按模型拆分"补不回来。若直接拿 user 维度的
	// 边界当模型边界, 前端会把"确实没有模型数据"的那段当"有数据但为 0"展示, 等于把缺失画成 0。
	var modelRollup struct {
		Earliest string
	}
	if fromKey != "" {
		if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
			Select("COALESCE(MIN(day), '') AS earliest").
			Where("dimension = ? AND dimension_id = ? AND day >= ? AND day <= ? AND model_name <> ''",
				model.BillingRollupModel, userID, fromKey, toKey).
			Scan(&modelRollup).Error; err != nil {
			return "", "", "", fmt.Errorf("failed to query user stats model rollup coverage: %w", err)
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
	// 按模型的覆盖起点 = min(最早明细, 最早的 model 维度汇总日): 两者都是"按模型能算得出来"的数据。
	// 但**不是** min(user 维度汇总): model 维度上线前的汇总是没有拆分的, 那段即便有钱也得算缺失。
	// 返回这条边界而不是假装全覆盖: 早于它的金额有数、按模型的拆分无数据, 前端据此提示而不是画成 0。
	// 两者都为空(区间内既无明细也无 model 汇总)= 该区间按模型确实无数据, 仍返回空串由前端提示。
	modelLiveStart := liveStart
	modelRollupStart := userStatsDayToDate(modelRollup.Earliest)
	switch {
	case modelLiveStart == "":
		modelCoverageStart = modelRollupStart
	case modelRollupStart == "":
		modelCoverageStart = modelLiveStart
	case modelRollupStart < modelLiveStart:
		modelCoverageStart = modelRollupStart
	default:
		modelCoverageStart = modelLiveStart
	}
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
// 为什么单独暴露这条边界: 按模型拆分现在读「明细 ∪ model 日汇总」, 所以它能比明细留存期活得更久 ——
// 但它活得比"总额"晚: 迁移 45 之前固化的日子只有 user 维度行, 没有 model 维度行, 那些天的总额有数、
// 拆分补不回来(明细当时就删了)。这条边界的两种空值语义必须分清:
//   - 区间内既无明细也无 model 汇总 ⇒ 返回空串, 前端显示"暂无按模型数据";
//   - 区间前段只有总额没有拆分 ⇒ 返回那时的分界日, 前端提示"此日期之前无按模型数据"。
//
// 早于它的金额仍在概览里(由 user 维度汇总补), 但拆分不出来。前端据此提示,
// 千万不要用"按比例摊到现有模型"之类的办法伪造数据, 那会让用户拿着假数字去对账。
func UserStatsModelCoverageStart(ctx context.Context, userID uint, startDate, endDate string) (string, error) {
	loc := statsUserLocation(userID)
	start, end := parseStatsDateRange(startDate, endDate, loc)
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
	loc := statsUserLocation(userID)
	start, end := parseStatsDateRange(startDate, endDate, loc)

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
