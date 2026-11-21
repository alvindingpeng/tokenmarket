package model

// APIKeyUsageTotals 一把 API Key 在一个窗口内的用量合计。
//
// 口径刻意分成两组: tokens 与 request_count 计**全部**明细(含已退费行) —— 用量确实发生过,
// 退的是钱不是流量; 而 user_cost 只计未退费部分(实付), 退款单独落在 refunded_* 两列。
// 两组并存才能既回答"这把 Key 调了多少"又回答"实际花了多少", 用一组必然答错其中一问。
type APIKeyUsageTotals struct {
	InputToken      int64 `json:"input_token"`       // 输入 token, 含已退费行: 用量确实发生过。
	OutputToken     int64 `json:"output_token"`      // 输出 token, 含已退费行。
	CacheReadToken  int64 `json:"cache_read_token"`  // 缓存读 token, 含已退费行。
	CacheWriteToken int64 `json:"cache_write_token"` // 缓存写 token, 含已退费行。
	RequestCount    int64 `json:"request_count"`     // 请求笔数, 含已退费行。
	// UserCost 未退费部分, 即实付金额; 等于 WindowTotal - RefundedUserCost。
	UserCost         float64 `json:"user_cost"`
	RefundedRecords  int64   `json:"refunded_records"`   // 其中已退费的笔数。
	RefundedUserCost float64 `json:"refunded_user_cost"` // 其中已退费的金额。
}

// APIKeyUsageDay 一把 API Key 某一天的用量。
type APIKeyUsageDay struct {
	Date string `json:"date"` // 本地挂钟日 YYYYMMDD, 与 billing_rollups.day 同一口径。
	APIKeyUsageTotals
}

// APIKeyUsageReport 按 API Key 的持久化用量报表。
//
// 为什么需要它: 明细表按留存期(默认 180 天)清理, 清理前会把整天聚合固化进 billing_rollups,
// 因此"终身口径 = 汇总表 ∪ 明细表"。只读明细的话, 一把老 Key 的历史会随清理一天天变少,
// 而内存里的实时计数(stats_api_keys)重启即归零 —— 两者都不是可以回答"这把 Key 一共用了多少"的口径。
//
// 上限说明: 本报表上线**之前**已归档的明细没有 apikey 维度的汇总行(那时只固化 user/owner/
// channel/platform 四维)。更早的 Key 历史无法补回 —— 那是上线顺序决定的上限, 不用伪造数值填平:
// CoverageStart 会如实落在最早有数据的那一天, 阅读方据此判断"覆盖率从哪天开始"。
type APIKeyUsageReport struct {
	APIKeyID int `json:"api_key_id"` // API Key 主键; 不含明文与指纹。
	// From/To 实际查询窗口的闭区间(YYYY-MM-DD): From 是窗口起点(未指定时回填最早有数据的那天),
	// To 是窗口末日且**含当天**。
	From string `json:"from"`
	To   string `json:"to"`
	// CoverageStart 最早有数据的日期(YYYY-MM-DD); 完全没有数据时为空串。
	// 它不是"这把 Key 的创建日": 只反映本报表能看见的最早一条账。
	CoverageStart string `json:"coverage_start"`
	// RetentionDays 明细表当前留存天数; 报表本身不受它影响, 但阅读方据此知道"明细还能查多久"。
	RetentionDays int               `json:"retention_days"`
	Total         APIKeyUsageTotals `json:"total"`       // 整个窗口的合计。
	Recent        APIKeyUsageTotals `json:"recent"`      // 窗口内最后 30 天的合计。
	RecentFrom    string            `json:"recent_from"` // 最近 30 天窗口的起点(YYYY-MM-DD)。
	Days          []APIKeyUsageDay  `json:"days"`        // 逐日明细, 日期升序; 无数据时是非 nil 空切片。
}
