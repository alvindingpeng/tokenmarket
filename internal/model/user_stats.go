package model

// UserStatsQuery 用户统计查询参数。
type UserStatsQuery struct {
	StartDate   string `form:"start_date" binding:"omitempty,datetime=2006-01-02"`   // 开始日期 YYYY-MM-DD
	EndDate     string `form:"end_date" binding:"omitempty,datetime=2006-01-02"`     // 结束日期 YYYY-MM-DD
	Granularity string `form:"granularity" binding:"omitempty,oneof=day week month"` // 粒度: day/week/month
}

// UserStatsOverview 用户调用概览: 时间区间内的聚合指标。
//
// 同一份响应里混用两个数据源, 各自口径在字段注释里写死, 不能相互推断:
//   - 费用与词元来自 billing_records: 只有产生了用量才会结算出行, 永久保留。
//   - 请求成功率与请求数来自 relay_requests(调用日志): 失败的调用不产生计费行,
//     用计费行算成功率会恒等于 100%, 只有调用日志能反映真实失败。
//     代价是调用日志按留存期滚动删除, 故必须一并返回覆盖起点与留存天数。
type UserStatsOverview struct {
	// TotalRequests 计费口径调用量(billing_records 行数, 含已退费行), 与费用/词元同源。
	// 它不等于真实请求数: 失败的调用不产生计费行。真实请求数见 RequestTotal。
	TotalRequests int64 `json:"total_requests"`
	// SuccessRate 请求成功率 %: success / (success + failed), 数据源 relay_requests。
	// 已取消(canceled)的请求不计入分母 —— 客户端主动断开或超时取消不代表服务不可用。
	// 查询区间内没有调用日志时返回 0, 前端据此显示"无数据"而不是 0%。
	SuccessRate float64 `json:"success_rate"`
	TotalCost   float64 `json:"total_cost"`   // 总费用（用户支出）
	TotalTokens int64   `json:"total_tokens"` // 总 token 数（输入+输出+缓存）

	// 请求口径计数(来自调用日志): 让成功率可被解释 —— 只有百分比没有样本量等于没有信息。
	RequestTotal    int64 `json:"request_total"`
	RequestSuccess  int64 `json:"request_success"`
	RequestFailed   int64 `json:"request_failed"`
	RequestCanceled int64 `json:"request_canceled"`

	// RequestCoverageStart 调用日志在查询区间内的实际覆盖起点(YYYY-MM-DD); 空串表示区间内无日志。
	// 调用日志只保留 RetentionDays 天, 选"最近 30 天"时成功率可能只基于末尾几天,
	// 页面必须把这层差异讲出来, 不能拿 7 天样本冒充 30 天结论。
	RequestCoverageStart string `json:"request_coverage_start"`
	RequestRetentionDays int    `json:"request_retention_days"`

	// 词元构成: 缓存读写此前被合并进 TotalTokens 后不可见, 用户无法判断"贵在输入还是缓存"。
	// 四项之和等于 TotalTokens, 拆分只为展示, 口径不变。
	InputTokens      int64 `json:"input_tokens"`       // 输入 token（未命中缓存的按量输入）
	OutputTokens     int64 `json:"output_tokens"`      // 输出 token
	CacheReadTokens  int64 `json:"cache_read_tokens"`  // 缓存命中读取的 token
	CacheWriteTokens int64 `json:"cache_write_tokens"` // 写入缓存的 token

	// CacheHitRate 缓存命中率 %, 定义为 cacheRead/(cacheRead+input)。
	// 不把 output 放进分母: 输出本来就不会命中缓存, 计入会把命中率整体压低, 失去比较意义。
	CacheHitRate float64 `json:"cache_hit_rate"`
}

// UserStatsTrend 用户调用趋势: 按时间粒度聚合。
// 与概览同源分层: Requests/Cost 来自计费行, SuccessRate 与请求计数来自调用日志。
type UserStatsTrend struct {
	Date     string  `json:"date"`     // 日期 YYYY-MM-DD
	Requests int64   `json:"requests"` // 计费口径请求数
	Cost     float64 `json:"cost"`     // 费用
	// SuccessRate 请求成功率 %: success / (success + failed), 该桶内没有调用日志时为 0。
	SuccessRate float64 `json:"success_rate"`
	// 该桶的调用日志计数; 桶内无日志(超出留存期)时三项均为 0。
	RequestSuccess  int64 `json:"request_success"`
	RequestFailed   int64 `json:"request_failed"`
	RequestCanceled int64 `json:"request_canceled"`
}

// UserStatsModel 用户调用按模型分组统计。
type UserStatsModel struct {
	ModelName string  `json:"model_name"` // 模型名
	Requests  int64   `json:"requests"`   // 请求数
	Cost      float64 `json:"cost"`       // 费用
	Tokens    int64   `json:"tokens"`     // token 总数
	// 按模型的缓存命中率, 口径与 UserStatsOverview.CacheHitRate 一致。
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
}

// UserStatsLatency 用户调用延迟指标。
//
// 数据源是 relay_requests(按调用日志留存期滚动删除), 不是 billing_records(永久保留)。
// 因此查询窗口可能只有部分天数有数据: CoverageStart 明确告知真实覆盖起点,
// 前端据此提示"延迟只覆盖最近 N 天", 不能把 7 天的均值当成 30 天的结论。
type UserStatsLatency struct {
	SampleCount int64 `json:"sample_count"` // 参与统计的成功请求数
	// AvgFirstTokenMs 平均首字耗时: 流式取首字节, 非流式取完整响应耗时。
	AvgFirstTokenMs float64 `json:"avg_first_token_ms"`
	// P95FirstTokenMs 首字耗时 P95, 反映长尾而不被均值掩盖。
	P95FirstTokenMs float64 `json:"p95_first_token_ms"`
	// AvgDurationMs 端到端平均总耗时(到响应结束)。
	AvgDurationMs float64 `json:"avg_duration_ms"`
	// AvgUpstreamTTFBMs 上游首字节耗时均值, 用于区分"我们慢"还是"上游慢"。
	AvgUpstreamTTFBMs float64 `json:"avg_upstream_ttfb_ms"`
	// CoverageStart 实际有延迟数据的最早时间(YYYY-MM-DD); 空串表示区间内无数据。
	CoverageStart string `json:"coverage_start"`
	// RetentionDays 调用日志留存天数, 用于解释覆盖范围为何短于查询区间。
	RetentionDays int `json:"retention_days"`
}
