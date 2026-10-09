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
	Hour int    `json:"hour" gorm:"primaryKey"`
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
