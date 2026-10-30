package model

// UserStatsQuery 用户统计查询参数。
type UserStatsQuery struct {
	StartDate  string `form:"start_date" binding:"omitempty,datetime=2006-01-02"`  // 开始日期 YYYY-MM-DD
	EndDate    string `form:"end_date" binding:"omitempty,datetime=2006-01-02"`    // 结束日期 YYYY-MM-DD
	Granularity string `form:"granularity" binding:"omitempty,oneof=day week month"` // 粒度: day/week/month
}

// UserStatsOverview 用户调用概览: 时间区间内的聚合指标。
type UserStatsOverview struct {
	TotalRequests int64   `json:"total_requests"`  // 总调用量
	SuccessRate   float64 `json:"success_rate"`    // 成功率 %
	TotalCost     float64 `json:"total_cost"`      // 总费用（用户支出）
	TotalTokens   int64   `json:"total_tokens"`    // 总 token 数（输入+输出+缓存）
}

// UserStatsTrend 用户调用趋势: 按时间粒度聚合。
type UserStatsTrend struct {
	Date        string  `json:"date"`         // 日期 YYYY-MM-DD
	Requests    int64   `json:"requests"`     // 请求数
	Cost        float64 `json:"cost"`         // 费用
	SuccessRate float64 `json:"success_rate"` // 成功率 %
}

// UserStatsModel 用户调用按模型分组统计。
type UserStatsModel struct {
	ModelName string  `json:"model_name"` // 模型名
	Requests  int64   `json:"requests"`   // 请求数
	Cost      float64 `json:"cost"`       // 费用
	Tokens    int64   `json:"tokens"`     // token 总数
}
