package op

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// apiKeyUsageBucket 报表里的一个日期桶, 明细侧与汇总侧先各自产出这种形状再合并。
type apiKeyUsageBucket struct {
	Date string // YYYYMMDD, 本地挂钟。
	// UserCostTotal 只用于合并前的中间计算: 合并后对外的 user_cost 是"合计 - 退款"。
	UserCostTotal    float64
	RefundedUserCost float64
	RefundedRecords  int64
	InputToken       int64
	OutputToken      int64
	CacheReadToken   int64
	CacheWriteToken  int64
	RequestCount     int64
}

// addBucket 把另一个同一天的桶并进来。
func (b *apiKeyUsageBucket) addBucket(other apiKeyUsageBucket) {
	b.InputToken += other.InputToken
	b.OutputToken += other.OutputToken
	b.CacheReadToken += other.CacheReadToken
	b.CacheWriteToken += other.CacheWriteToken
	b.RequestCount += other.RequestCount
	b.UserCostTotal += other.UserCostTotal
	b.RefundedUserCost += other.RefundedUserCost
	b.RefundedRecords += other.RefundedRecords
}

// totals 把桶换算成对外口径: 词元与笔数计全部明细, 金额只计未退费部分。
func (b apiKeyUsageBucket) totals() model.APIKeyUsageTotals {
	return model.APIKeyUsageTotals{
		InputToken:       b.InputToken,
		OutputToken:      b.OutputToken,
		CacheReadToken:   b.CacheReadToken,
		CacheWriteToken:  b.CacheWriteToken,
		RequestCount:     b.RequestCount,
		UserCost:         b.UserCostTotal - b.RefundedUserCost,
		RefundedRecords:  b.RefundedRecords,
		RefundedUserCost: b.RefundedUserCost,
	}
}

// apiKeyUsageRecentDays 报表里"近 30 天"小窗口的天数。
const apiKeyUsageRecentDays = 30

// APIKeyUsageReport 汇总某把 API Key 在 [from, until) 窗口内的持久化用量。
//
// 为什么要并两张表: billing_records 是逐笔明细, 只保留 billing_retention_days 天(默认 180),
// 清理前把"当天"的聚合值整日固化进 billing_rollups。只读明细, 一把老 Key 的历史会随清理
// 一天天变少; 只读汇总, 又看不见还没被清理的近段数据。终身口径必须是两者并集。
//
// 为什么两侧不会重复计数: 某天的明细被删之前已整日固化, 同一天不可能既在汇总里又在明细里;
// 因申诉保护而留存的明细不进汇总, 仍在明细表里被算上。所以按日期桶相加即精确。
//
// 本版本上限: 上线前已归档的明细没有 apikey 维度的汇总行(当时只固化 user/owner/channel/
// platform), 更早的 Key 历史无法补回。这是上线顺序决定的, 不用伪造数值填平 ——
// CoverageStart 会如实落在最早可见的那一天, 阅读方据此判断覆盖率从哪天开始。
//
// from 为零值表示不限(从最早一条起); until 为零值表示到现在。日期桶一律走
// billingBucketExpr("day") 的方言实现 —— 它按本地挂钟归日, 绝不能用 DATE()/date():
// SQLite 会先归一到 UTC, 清晨的请求会被算到前一天。
func APIKeyUsageReport(ctx context.Context, apiKeyID int, from, until time.Time) (model.APIKeyUsageReport, error) {
	if apiKeyID == 0 {
		return model.APIKeyUsageReport{}, fmt.Errorf("api key id is required")
	}
	now := time.Now()
	if until.IsZero() {
		until = now
	}
	if !from.IsZero() && !from.Before(until) {
		return model.APIKeyUsageReport{}, fmt.Errorf("invalid usage window: from must be before to")
	}

	buckets := make(map[string]*apiKeyUsageBucket)
	keys := make([]string, 0)

	// ---- 明细侧 ----
	live, err := apiKeyUsageLiveBuckets(ctx, apiKeyID, from, until)
	if err != nil {
		return model.APIKeyUsageReport{}, err
	}
	for _, bucket := range live {
		merged, ok := buckets[bucket.Date]
		if !ok {
			copied := bucket
			buckets[bucket.Date] = &copied
			keys = append(keys, bucket.Date)
			continue
		}
		merged.addBucket(bucket)
	}

	// ---- 汇总侧 ----
	rollups, err := apiKeyUsageRollupBuckets(ctx, apiKeyID, from, until)
	if err != nil {
		return model.APIKeyUsageReport{}, err
	}
	for _, bucket := range rollups {
		merged, ok := buckets[bucket.Date]
		if !ok {
			copied := bucket
			buckets[bucket.Date] = &copied
			keys = append(keys, bucket.Date)
			continue
		}
		merged.addBucket(bucket)
	}

	sort.Strings(keys)

	// 覆盖率起点要跨窗口算: 它是"这把 Key 最早可见的一天", 不能因为本次请求把 from 收窄
	// 就把起点往后挪 —— 那样阅读方会误判成"之前没有用量"。
	coverageStart, err := apiKeyUsageCoverageStart(ctx, apiKeyID)
	if err != nil {
		return model.APIKeyUsageReport{}, err
	}

	report := model.APIKeyUsageReport{
		APIKeyID:      apiKeyID,
		CoverageStart: formatUsageDay(coverageStart),
		RetentionDays: SettingGetIntDefault(model.SettingKeyBillingRetentionDays, billingRetentionDefaultDays),
		Days:          make([]model.APIKeyUsageDay, 0, len(keys)),
	}
	recentBoundary := dayKey(until.AddDate(0, 0, -apiKeyUsageRecentDays))
	// "近 30 天"是**窗口内**的最后 30 天: 窗口比 30 天短时, 界点不能跑到窗口起点之前,
	// 否则 recent_from 会给出一个窗口外的日期, 前端据此画的区间比 total 还大。
	if fromDay := dayKey(from); !from.IsZero() && fromDay > recentBoundary {
		recentBoundary = fromDay
	}
	var total apiKeyUsageBucket
	var recent apiKeyUsageBucket
	for _, key := range keys {
		bucket := buckets[key]
		total.addBucket(*bucket)
		if key >= recentBoundary {
			recent.addBucket(*bucket)
		}
		report.Days = append(report.Days, model.APIKeyUsageDay{Date: key, APIKeyUsageTotals: bucket.totals()})
	}
	report.Total = total.totals()
	report.Recent = recent.totals()

	// To 是窗口内最后一个被包含的自然日: 窗口是左闭右开的, 最后一天 = until 前一个瞬间所在的日期。
	report.To = until.Add(-time.Nanosecond).In(time.Local).Format("2006-01-02")
	report.From = from.In(time.Local).Format("2006-01-02")
	if from.IsZero() {
		// 未指定起点时回填覆盖起点, 而不是留一个 0001-01-01 让前端去猜; 一条数据都没有时给空串,
		// 与 CoverageStart 的空语义保持一致。
		report.From = formatUsageDay(coverageStart)
	}
	report.RecentFrom = formatUsageDay(recentBoundary)
	return report, nil
}

// formatUsageDay 把 YYYYMMDD 换成对外的 YYYY-MM-DD; 非 8 位原样返回。
func formatUsageDay(day string) string {
	if len(day) != 8 {
		return day
	}
	return day[:4] + "-" + day[4:6] + "-" + day[6:]
}

// apiKeyUsageLiveBuckets 明细侧按本地挂钟日桶聚合某把 Key 的用量。
//
// 退款用 SUM(CASE WHEN status = 'refunded' ...) 而非方言专属函数: 三种数据库都支持,
// 也避免为了一个布尔判断引入第二条方言分支。零行时 SUM 返回 NULL, 一律 COALESCE 兜底,
// 否则扫描 NULL 到 int64/float64 会报错。
func apiKeyUsageLiveBuckets(ctx context.Context, apiKeyID int, from, until time.Time) ([]apiKeyUsageBucket, error) {
	dateExpr := billingBucketExpr("day")
	refundedFlag := model.BillingStatusRefunded
	query := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select(dateExpr+" AS date,"+
			" COALESCE(SUM(input_token), 0) AS input_token,"+
			" COALESCE(SUM(output_token), 0) AS output_token,"+
			" COALESCE(SUM(cache_read_token), 0) AS cache_read_token,"+
			" COALESCE(SUM(cache_write_token), 0) AS cache_write_token,"+
			" COUNT(*) AS request_count,"+
			" COALESCE(SUM(user_cost), 0) AS user_cost_total,"+
			" COALESCE(SUM(CASE WHEN status = ? THEN user_cost ELSE 0 END), 0) AS refunded_user_cost,"+
			" COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS refunded_records",
			refundedFlag, refundedFlag).
		Where("api_key_id = ? AND created_at < ?", apiKeyID, until).
		Group(dateExpr).
		Order("date ASC")
	if !from.IsZero() {
		query = query.Where("created_at >= ?", from)
	}
	rows := make([]apiKeyUsageBucket, 0)
	if err := query.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to aggregate api key usage: %w", err)
	}
	return rows, nil
}

// apiKeyUsageRollupBuckets 汇总侧读某把 Key 已固化的逐日用量。
//
// 用 day(YYYYMMDD 定长串)比较区间, 与固化的整天边界一致, 也不需要任何方言日期函数。
func apiKeyUsageRollupBuckets(ctx context.Context, apiKeyID int, from, until time.Time) ([]apiKeyUsageBucket, error) {
	var rows []model.BillingRollup
	query := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day < ?", model.BillingRollupAPIKey, uint(apiKeyID), dayKey(until))
	if !from.IsZero() {
		query = query.Where("day >= ?", dayKey(from))
	}
	if err := query.Order("day ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to read api key usage rollups: %w", err)
	}
	out := make([]apiKeyUsageBucket, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		out = append(out, apiKeyUsageBucket{
			Date:             row.Day,
			UserCostTotal:    row.UserCost,
			RefundedUserCost: row.RefundedUserCost,
			RefundedRecords:  row.RefundedRecords,
			InputToken:       row.InputToken,
			OutputToken:      row.OutputToken,
			CacheReadToken:   row.CacheReadToken,
			CacheWriteToken:  row.CacheWriteToken,
			RequestCount:     row.RequestCount,
		})
	}
	return out, nil
}

// apiKeyUsageCoverageStart 返回这把 Key 最早可见数据的日键(YYYYMMDD), 无数据返回空串。
//
// 取两张表的极小值: 汇总侧是已固化的日子, 明细侧是还没被清理的日子。任一为空就由另一方定。
// 注意它**不含**"上线前已归档的日子" —— 那时没有 apikey 维度, 那些天确实不可见, 不能假装有。
func apiKeyUsageCoverageStart(ctx context.Context, apiKeyID int) (string, error) {
	var rollupDay string
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ?", model.BillingRollupAPIKey, uint(apiKeyID)).
		Select("COALESCE(MIN(day), '')").Scan(&rollupDay).Error; err != nil {
		return "", fmt.Errorf("failed to read api key usage coverage: %w", err)
	}
	var liveDay string
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Where("api_key_id = ?", apiKeyID).
		Select("COALESCE(MIN(" + billingBucketExpr("day") + "), '')").Scan(&liveDay).Error; err != nil {
		return "", fmt.Errorf("failed to read api key usage coverage: %w", err)
	}
	return earliestUsageDay(rollupDay, liveDay), nil
}

// earliestUsageDay 返回两个日键里较早的那个; 空串表示该侧没有数据。
func earliestUsageDay(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case a < b:
		return a
	default:
		return b
	}
}
