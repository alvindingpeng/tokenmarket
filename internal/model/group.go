package model

type Group struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	Name      string `json:"name" gorm:"not null"`
	ChannelID int    `json:"channel_id" gorm:"not null"`
	ModelName string `json:"model_name" gorm:"not null"`
	Priority  int    `json:"priority" gorm:"default:1"`
}

type GroupItem struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	ChannelID int    `json:"channel_id" gorm:"not null"`
	ModelName string `json:"model_name" gorm:"not null"`
	Priority  int    `json:"priority" gorm:"default:1"`
}

type GroupResponse struct {
	Name  string      `json:"name"`
	Items []GroupItem `json:"items"`
}
