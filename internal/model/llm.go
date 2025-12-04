package model

type LLMPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type LLMInfo struct {
	Name string `json:"name" gorm:"primaryKey;not null"`
	LLMPrice
}

type LLMChannel struct {
	Name        string `json:"name"`
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
}
