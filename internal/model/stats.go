package model

import "time"

type StatsTotal struct {
	ID           int     `gorm:"primaryKey;check:id=1"`
	InputToken   int64   `json:"input_token" gorm:"bigint;default:0"`
	OutputToken  int64   `json:"output_token" gorm:"bigint;default:0"`
	InputCost    float64 `json:"input_cost" gorm:"type:real;default:0.00"`
	OutputCost   float64 `json:"output_cost" gorm:"type:real;default:0.00"`
	WaitTime     int64   `json:"wait_time" gorm:"bigint;default:0"`
	RequestCount int64   `json:"request_count" gorm:"bigint;default:0"`
}

type StatsDaily struct {
	Date         time.Time `json:"date" gorm:"primaryKey"`
	InputToken   int64     `json:"input_token" gorm:"bigint;default:0"`
	OutputToken  int64     `json:"output_token" gorm:"bigint;default:0"`
	InputCost    float64   `json:"input_cost" gorm:"type:real;default:0.00"`
	OutputCost   float64   `json:"output_cost" gorm:"type:real;default:0.00"`
	WaitTime     int64     `json:"wait_time" gorm:"bigint;default:0"`
	RequestCount int64     `json:"request_count" gorm:"bigint;default:0"`
}

type StatsModel struct {
	ID           int     `json:"id" gorm:"primaryKey"`
	Name         string  `json:"name" gorm:"not null"`
	ChannelID    int     `json:"channel_id" gorm:"not null"`
	InputToken   int64   `json:"input_token" gorm:"bigint;default:0"`
	OutputToken  int64   `json:"output_token" gorm:"bigint;default:0"`
	InputCost    float64 `json:"input_cost" gorm:"type:real;default:0.00"`
	OutputCost   float64 `json:"output_cost" gorm:"type:real;default:0.00"`
	RequestCount int64   `json:"request_count" gorm:"bigint;default:0"`
}
