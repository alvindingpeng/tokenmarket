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

	var result struct {
		TotalRequests    int64
		TotalCost        float64
		TotalTokens      int64
		InputTokens      int64
		OutputTokens     int64
		CacheReadTokens  int64
		CacheWriteTokens int64
	}

	// 词元按类拆分, 与总口径同源: 四项之和恒等于 total_tokens。
	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(`
			COUNT(*) as total_requests,
			SUM(user_cost) as total_cost,
			SUM(input_token + output_token + cache_read_token + cache_write_token) as total_tokens,
			SUM(input_token) as input_tokens,
			SUM(output_token) as output_tokens,
			SUM(cache_read_token) as cache_read_tokens,
			SUM(cache_write_token) as cache_write_tokens
		`).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Scan(&result).Error

	if err != nil {
		return model.UserStatsOverview{}, fmt.Errorf("failed to query user stats overview: %w", err)
	}

	// 成功率另取调用日志: 与计费口径无关, 两者必须各自成对(计数与比率同源)才不会被误读。
	rel, err := queryRequestOutcome(ctx, userID, start, end)
	if err != nil {
		return model.UserStatsOverview{}, err
	}

	return model.UserStatsOverview{
		TotalRequests:        result.TotalRequests,
		SuccessRate:          rel.successRate(),
		TotalCost:            result.TotalCost,
		TotalTokens:          result.TotalTokens,
		RequestTotal:         rel.Success + rel.Failed + rel.Canceled,
		RequestSuccess:       rel.Success,
		RequestFailed:        rel.Failed,
		RequestCanceled:      rel.Canceled,
		RequestCoverageStart: rel.coverageStart,
		RequestRetentionDays: rel.retentionDays,
		InputTokens:          result.InputTokens,
		OutputTokens:         result.OutputTokens,
		CacheReadTokens:      result.CacheReadTokens,
		CacheWriteTokens:     result.CacheWriteTokens,
		CacheHitRate:         cacheHitRate(result.CacheReadTokens, result.InputTokens),
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

	// 调用日志按同一套桶表达式聚合, 再按桶并入计价数据。
	outcomes, err := queryRequestOutcomeBuckets(ctx, userID, start, end, granularity)
	if err != nil {
		return nil, err
	}

	// 桶集合取两者的并集。只按计费行建桶会把"那天有调用但一次都没结算"(即全失败)
	// 整段从图上抹掉 —— 恰恰是最该被看到的一天。这类桶的调用量改取调用日志的终态计数:
	// 计费口径的 0 会被读成"当天没有调用", 而事实是调用全部失败了。
	result := make([]model.UserStatsTrend, 0, len(rows)+len(outcomes))
	byDate := make(map[string]int, len(rows)+len(outcomes))
	for _, row := range rows {
		byDate[row.Date] = len(result)
		result = append(result, model.UserStatsTrend{
			Date:     row.Date,
			Requests: row.Requests,
			Cost:     row.Cost,
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
			return "STRFTIME('%Y-%m-%d %H:00', " + column + ")"
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
