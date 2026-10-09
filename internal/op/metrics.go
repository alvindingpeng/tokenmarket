package op

import (
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// OpsStats 运维统计口径: 请求量按终态分列, 用量与费用求和。
type OpsStats struct {
	Requests     int64   `json:"requests"`
	Success      int64   `json:"success"`
	Failed       int64   `json:"failed"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

// StatsSince 统计时间窗内的调用汇总; since 为零值时统计全量。
func StatsSince(since time.Time) (OpsStats, error) {
	var row struct {
		Requests int64
		Success  int64
		Failed   int64
		Input    int64
		Output   int64
		Cost     float64
	}
	query := db.GetDB().Model(&model.RelayRequest{})
	if !since.IsZero() {
		query = query.Where("started_at >= ?", since)
	}
	err := query.Select(
		"count(*) as requests, " +
			"coalesce(sum(case when status = 'success' then 1 else 0 end), 0) as success, " +
			"coalesce(sum(case when status in ('failed','canceled') then 1 else 0 end), 0) as failed, " +
			"coalesce(sum(prompt_tokens), 0) as input, " +
			"coalesce(sum(completion_tokens), 0) as output, " +
			"coalesce(sum(cost), 0) as cost",
	).Scan(&row).Error
	if err != nil {
		return OpsStats{}, err
	}
	return OpsStats{
		Requests:     row.Requests,
		Success:      row.Success,
		Failed:       row.Failed,
		InputTokens:  row.Input,
		OutputTokens: row.Output,
		Cost:         row.Cost,
	}, nil
}

// MetricsTotals 全量累计指标: /metrics 抓取与运维概览共用。
type MetricsTotals struct {
	RequestsByStatus map[string]int64 `json:"requests_by_status"`
	InputTokens      int64            `json:"input_tokens"`
	OutputTokens     int64            `json:"output_tokens"`
	Cost             float64          `json:"cost"`
	Users            int64            `json:"users"`
	Balance          float64          `json:"balance"`
	Channels         int64            `json:"channels"`
	ChannelsEnabled  int64            `json:"channels_enabled"`
	Alerts24h        int64            `json:"alerts_24h"`
}

// MetricsTotals 汇总全量累计指标; 各维度独立查询, 单项失败不阻塞其余。
func CollectMetrics() MetricsTotals {
	database := db.GetDB()
	totals := MetricsTotals{RequestsByStatus: map[string]int64{}}
	var statusRows []struct {
		Status string
		Count  int64
	}
	if err := database.Model(&model.RelayRequest{}).Select("status, count(*) as count").Group("status").Scan(&statusRows).Error; err == nil {
		for _, row := range statusRows {
			totals.RequestsByStatus[row.Status] = row.Count
		}
	}
	var tokenAgg struct {
		Input  int64
		Output int64
		Cost   float64
	}
	if err := database.Model(&model.RelayRequest{}).Select("coalesce(sum(prompt_tokens),0) as input, coalesce(sum(completion_tokens),0) as output, coalesce(sum(cost),0) as cost").Scan(&tokenAgg).Error; err == nil {
		totals.InputTokens = tokenAgg.Input
		totals.OutputTokens = tokenAgg.Output
		totals.Cost = tokenAgg.Cost
	}
	var userAgg struct {
		Users   int64
		Balance float64
	}
	if err := database.Model(&model.User{}).Select("count(*) as users, coalesce(sum(balance),0) as balance").Scan(&userAgg).Error; err == nil {
		totals.Users = userAgg.Users
		totals.Balance = userAgg.Balance
	}
	var channelAgg struct {
		Total   int64
		Enabled int64
	}
	if err := database.Model(&model.Channel{}).Select("count(*) as total, coalesce(sum(case when enabled then 1 else 0 end),0) as enabled").Scan(&channelAgg).Error; err == nil {
		totals.Channels = channelAgg.Total
		totals.ChannelsEnabled = channelAgg.Enabled
	}
	_ = database.Model(&model.ChannelAlert{}).Where("created_at >= ?", time.Now().Add(-24*time.Hour)).Count(&totals.Alerts24h).Error
	return totals
}
