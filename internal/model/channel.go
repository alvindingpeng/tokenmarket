package model

import "github.com/alvindingpeng/switchyard/internal/transformer/outbound"

type Channel struct {
	ID          int                   `json:"id" gorm:"primaryKey"`
	Name        string                `json:"name" gorm:"unique;not null"`
	Type        outbound.OutboundType `json:"type"`
	Enabled     bool                  `json:"enabled" gorm:"default:true"`
	BaseURL     string                `json:"base_url" gorm:"not null"`
	Key         string                `json:"key" gorm:"not null"`
	Model       string                `json:"model"`
	CustomModel string                `json:"custom_model"`
	Proxy       bool                  `json:"proxy" gorm:"default:false"`
	AutoSync    bool                  `json:"auto_sync" gorm:"default:true"`
	AutoGroup   bool                  `json:"auto_group" gorm:"default:false"`
	Stats       *StatsChannel         `json:"stats,omitempty" gorm:"foreignKey:ChannelID"`
}
