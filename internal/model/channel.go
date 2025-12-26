package model

import "github.com/alvindingpeng/switchyard/internal/transformer/outbound"

type AutoGroupType int

const (
	AutoGroupTypeNone  AutoGroupType = 0 //不自动分组
	AutoGroupTypeFuzzy AutoGroupType = 1 //模糊匹配
	AutoGroupTypeExact AutoGroupType = 2 //准确匹配
	AutoGroupTypeRegex AutoGroupType = 3 //正则匹配
)

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
	AutoSync    bool                  `json:"auto_sync" gorm:"default:false"`
	AutoGroup   AutoGroupType         `json:"auto_group" gorm:"default:0"`
	Stats       *StatsChannel         `json:"stats,omitempty" gorm:"foreignKey:ChannelID"`
}
