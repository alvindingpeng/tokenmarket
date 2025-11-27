package model

type ChannelType int8

const (
	ChannelTypeOpenAIChat ChannelType = iota
	ChannelTypeOpenAIResponse
	ChannelTypeAnthropic
	ChannelTypeOneAPI
)

type Channel struct {
	ID      int           `json:"id" gorm:"primaryKey"`
	Name    string        `json:"name" gorm:"unique;not null"`
	Type    ChannelType   `json:"type"`
	Enabled bool          `json:"enabled" gorm:"default:true"`
	BaseURL string        `json:"base_url" gorm:"not null"`
	Key     string        `json:"key" gorm:"not null"`
	Model   string        `json:"model" gorm:"not null"`
	Proxy   bool          `json:"proxy" gorm:"default:false"`
	Stats   *StatsChannel `json:"stats,omitempty" gorm:"foreignKey:ChannelID"`
}
