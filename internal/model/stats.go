package model

type StatsMetrics struct {
	InputToken     int64   `json:"input_token" gorm:"bigint"`
	OutputToken    int64   `json:"output_token" gorm:"bigint"`
	InputCost      float64 `json:"input_cost" gorm:"type:real"`
	OutputCost     float64 `json:"output_cost" gorm:"type:real"`
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
	Hour int    `json:"hour" gorm:"primaryKey;autoIncrement:false"`
	Date string `json:"date" gorm:"not null"` // 记录最后更新日期，格式：20060102
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
