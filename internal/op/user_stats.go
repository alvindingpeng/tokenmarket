package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// UserStatsOverview 查询用户在指定时间区间的调用概览。
// 默认最近 30 天; startDate/endDate 为空时使用默认值。
// 口径与既有实现一致, 不做改动: 总量/费用/Token 含 refunded 行, 成功数仅计 settled;
// SuccessRate = settled 计费行占总行数的百分比(计费口径, 并非请求成功率)。
func UserStatsOverview(ctx context.Context, userID uint, startDate, endDate string) (model.UserStatsOverview, error) {
	start, end := parseStatsDateRange(startDate, endDate)

	var result struct {
		TotalRequests int64
		SuccessCount  int64
		TotalCost     float64
		TotalTokens   int64
	}

	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(`
			COUNT(*) as total_requests,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as success_count,
			SUM(user_cost) as total_cost,
			SUM(input_token + output_token + cache_read_token + cache_write_token) as total_tokens
		`, model.BillingStatusSettled).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Scan(&result).Error

	if err != nil {
		return model.UserStatsOverview{}, fmt.Errorf("failed to query user stats overview: %w", err)
	}

	successRate := 0.0
	if result.TotalRequests > 0 {
		successRate = float64(result.SuccessCount) * 100.0 / float64(result.TotalRequests)
	}

	return model.UserStatsOverview{
		TotalRequests: result.TotalRequests,
		SuccessRate:   successRate,
		TotalCost:     result.TotalCost,
		TotalTokens:   result.TotalTokens,
	}, nil
}

// UserStatsTrend 查询用户调用趋势（按日/周/月聚合）。
// granularity 为空时默认按天。
// 口径与 UserStatsOverview 一致: requests/cost 含 refunded 行, SuccessRate 为 settled 占比。
// 周桶以周一为一周起点, Date 标签为当周周一(YYYY-MM-DD)。
func UserStatsTrend(ctx context.Context, userID uint, startDate, endDate, granularity string) ([]model.UserStatsTrend, error) {
	start, end := parseStatsDateRange(startDate, endDate)
	if granularity == "" {
		granularity = "day"
	}

	dateFunc := statsBucketExpr(statsDialect(), granularity)

	var rows []struct {
		Date         string
		Requests     int64
		SuccessCount int64
		Cost         float64
	}

	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(fmt.Sprintf(`
			%s as date,
			COUNT(*) as requests,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as success_count,
			SUM(user_cost) as cost
		`, dateFunc), model.BillingStatusSettled).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Group(dateFunc).
		Order("date ASC").
		Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query user stats trend: %w", err)
	}

	result := make([]model.UserStatsTrend, len(rows))
	for i, row := range rows {
		successRate := 0.0
		if row.Requests > 0 {
			successRate = float64(row.SuccessCount) * 100.0 / float64(row.Requests)
		}
		result[i] = model.UserStatsTrend{
			Date:        row.Date,
			Requests:    row.Requests,
			Cost:        row.Cost,
			SuccessRate: successRate,
		}
	}

	return result, nil
}

// UserStatsModelBreakdown 查询用户调用按模型分组统计。
func UserStatsModelBreakdown(ctx context.Context, userID uint, startDate, endDate string) ([]model.UserStatsModel, error) {
	start, end := parseStatsDateRange(startDate, endDate)

	var rows []struct {
		ModelName string
		Requests  int64
		Cost      float64
		Tokens    int64
	}

	err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(`
			model_name,
			COUNT(*) as requests,
			SUM(user_cost) as cost,
			SUM(input_token + output_token + cache_read_token + cache_write_token) as tokens
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
			ModelName: row.ModelName,
			Requests:  row.Requests,
			Cost:      row.Cost,
			Tokens:    row.Tokens,
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
	switch dialect {
	case "mysql":
		switch granularity {
		case "week":
			return "DATE_FORMAT(created_at - INTERVAL WEEKDAY(created_at) DAY, '%Y-%m-%d')"
		case "month":
			return "DATE_FORMAT(created_at, '%Y-%m-01')"
		default:
			return "DATE_FORMAT(created_at, '%Y-%m-%d')"
		}
	case "postgres", "postgresql":
		switch granularity {
		case "week":
			return "TO_CHAR(DATE_TRUNC('week', created_at), 'YYYY-MM-DD')"
		case "month":
			return "TO_CHAR(DATE_TRUNC('month', created_at), 'YYYY-MM-DD')"
		default:
			return "TO_CHAR(DATE_TRUNC('day', created_at), 'YYYY-MM-DD')"
		}
	default: // sqlite
		switch granularity {
		case "week":
			return "DATE(SUBSTR(created_at, 1, 10), '-6 days', 'weekday 1')"
		case "month":
			return "DATE(SUBSTR(created_at, 1, 10), 'start of month')"
		default:
			return "DATE(SUBSTR(created_at, 1, 10))"
		}
	}
}
