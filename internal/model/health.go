package model

import "time"

// ChannelHealthRecord 渠道健康探测结果; 每次探测一行, 保留最近若干条供趋势分析。
type ChannelHealthRecord struct {
	ID          uint64    `json:"id" gorm:"primaryKey"`
	ChannelID   int       `json:"channel_id" gorm:"index"`
	ChannelCode string    `json:"channel_code" gorm:"size:64"`
	LatencyMs   int64     `json:"latency_ms"`
	StatusCode  int       `json:"status_code"`
	Error       string    `json:"error" gorm:"type:text"`
	Healthy     bool      `json:"healthy" gorm:"index"`
	CheckedAt   time.Time `json:"checked_at" gorm:"index"`
}

// TableName 指定表名。
func (ChannelHealthRecord) TableName() string { return "channel_health_records" }

// ChannelAlert 告警记录: 渠道连续异常或恢复时生成。
type ChannelAlert struct {
	ID          uint64    `json:"id" gorm:"primaryKey"`
	ChannelID   int       `json:"channel_id" gorm:"index"`
	ChannelCode string    `json:"channel_code" gorm:"size:64"`
	Kind        string    `json:"kind" gorm:"size:32"`
	Reason      string    `json:"reason" gorm:"type:text"`
	Notified    bool      `json:"notified"`
	CreatedAt   time.Time `json:"created_at" gorm:"index"`
}

// TableName 指定表名。
func (ChannelAlert) TableName() string { return "channel_alerts" }
