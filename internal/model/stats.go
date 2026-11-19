package model

type StatsMetrics struct {
	InputToken     int64   `json:"input_token" gorm:"bigint"` // 毛输入词元(prompt, 含缓存读/写); 总词元=输入+输出, 与上游 total_tokens 对齐。
	OutputToken    int64   `json:"output_token" gorm:"bigint"`
	InputCost      float64 `json:"input_cost" gorm:"type:real"`  // 成本金额: 请求级统计传用户价(含加价), 渠道侧统计传供货价。
	OutputCost     float64 `json:"output_cost" gorm:"type:real"` // 成本金额: 请求级统计传用户价(含加价), 渠道侧统计传供货价。
	WaitTime       int64   `json:"wait_time" gorm:"bigint"`
	RequestSuccess int64   `json:"request_success" gorm:"bigint"`
	RequestFailed  int64   `json:"request_failed" gorm:"bigint"`
}

type StatsTotal struct {
	ID int `gorm:"primaryKey"`
	StatsMetrics
}

type StatsHourly struct {
	// Hour 是 0-23 的小时刻度, 由代码显式给出, 绝不能交给数据库自增:
	// 整数主键默认按自增处理时, GORM 会把零值(午夜 0 点)当成"未设置"而从 INSERT 里省略该列,
	// 于是午夜桶每次都落成新行而不是更新, 表里既有脏行又永远没有 hour=0。
	//
	// 主键是 (Date, Hour) 而不是单独的 Hour: 只以小时为主键时, 每天同一小时的桶只有一行,
	// 第二天第一次写入就把昨天那一小时的值覆盖掉了 —— 小时曲线永远只剩"今天", 跨天既无法回看,
	// 也无法和日统计对账。改成按天留存后每天各 24 行, 历史小时分布可直接查询。
	Hour int    `json:"hour" gorm:"primaryKey;autoIncrement:false"` // 0-23 小时刻度。
	Date string `json:"date" gorm:"primaryKey;not null;index"`      // 归属日, 格式 20060102。
	StatsMetrics
}

type StatsDaily struct {
	Date string `json:"date" gorm:"primaryKey"`
	StatsMetrics
}

type StatsAPIKey struct {
	APIKeyID int `json:"api_key_id" gorm:"primaryKey"`
	StatsMetrics
}

// StatsChannelDaily 是渠道维度的按天累计统计, 供渠道页展示成功率与平均延迟的逐日变化。
type StatsChannelDaily struct {
	ChannelID int    `json:"channel_id" gorm:"primaryKey"`          // 渠道主键。
	Date      string `json:"date" gorm:"primaryKey;not null;index"` // 日期, 格式 20060102。
	StatsMetrics
}

// StatsChannelModelDaily 是渠道模型维度的按天累计统计, 与渠道维度同口径。
type StatsChannelModelDaily struct {
	ChannelModelID int    `json:"channel_model_id" gorm:"primaryKey"`    // 渠道模型主键。
	Date           string `json:"date" gorm:"primaryKey;not null;index"` // 日期, 格式 20060102。
	StatsMetrics
}

// Add aggregates another StatsMetrics into the current one.
func (s *StatsMetrics) Add(delta StatsMetrics) {
	s.InputToken += delta.InputToken
	s.OutputToken += delta.OutputToken
	s.InputCost += delta.InputCost
	s.OutputCost += delta.OutputCost
	s.WaitTime += delta.WaitTime
	s.RequestSuccess += delta.RequestSuccess
	s.RequestFailed += delta.RequestFailed
}
