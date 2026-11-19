package model

// ChannelProfitQuery 渠道利润分析查询参数。
// 日期为可选的 YYYY-MM-DD; 缺省由 op 层补成最近 30 天, 格式非法在绑定阶段即被拒绝。
type ChannelProfitQuery struct {
	StartDate string `form:"start_date" binding:"omitempty,datetime=2006-01-02"` // 开始日期(含)。
	EndDate   string `form:"end_date" binding:"omitempty,datetime=2006-01-02"`   // 结束日期(含)。
}

// ChannelProfitTotals 区间汇总口径: 收入=用户支出, 成本=发布者收入, 毛利=平台收入。
// 三者取自同一条计费记录的 user_cost / owner_revenue / platform_revenue, 结算时已对平,
// 这里只做求和, 不重算账务。
type ChannelProfitTotals struct {
	Requests        int64   `json:"requests"`         // 结算请求数。
	UserCost        float64 `json:"user_cost"`        // 收入: 用户支出合计。
	OwnerRevenue    float64 `json:"owner_revenue"`    // 成本: 发布者收入合计。
	PlatformRevenue float64 `json:"platform_revenue"` // 毛利: 平台收入合计。
	MarginRate      float64 `json:"margin_rate"`      // 毛利率 %, 毛利/收入×100, 收入为 0 时记 0。
}

// ChannelProfitDaily 按自然日聚合的单行结果; 只返回有数据的日期, 日期升序。
type ChannelProfitDaily struct {
	Date string `json:"date"` // 日期 YYYY-MM-DD。
	ChannelProfitTotals
}

// ChannelProfit 渠道利润分析只读视图; 汇总与逐日结果都来自 billing_records 的结算行。
type ChannelProfit struct {
	ChannelID   int                  `json:"channel_id"`
	ChannelName string               `json:"channel_name"`
	StartDate   string               `json:"start_date"` // 实际生效的开始日期(含)。
	EndDate     string               `json:"end_date"`   // 实际生效的结束日期(含)。
	Totals      ChannelProfitTotals  `json:"totals"`
	Daily       []ChannelProfitDaily `json:"daily"` // 恒为数组, 无数据时是 [], 不是 null。
}
