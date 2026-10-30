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
func UserStatsTrend(ctx context.Context, userID uint, startDate, endDate, granularity string) ([]model.UserStatsTrend, error) {
	start, end := parseStatsDateRange(startDate, endDate)
	if granularity == "" {
		granularity = "day"
	}
	
	// SQLite date 函数; MySQL/PostgreSQL 语法类似但需要按实际方言调整。
	dateFunc := "DATE(created_at)"
	switch granularity {
	case "week":
		dateFunc = "DATE(created_at, 'weekday 0', '-6 days')" // 周一起
	case "month":
		dateFunc = "DATE(created_at, 'start of month')"
	}
	
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
