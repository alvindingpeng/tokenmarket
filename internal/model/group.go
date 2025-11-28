package model

type Group struct {
	ID    int         `json:"id" gorm:"primaryKey"`
	Name  string      `json:"name" gorm:"unique;not null"`
	Model string      `json:"model" gorm:"not null"`
	Items []GroupItem `json:"items,omitempty" gorm:"foreignKey:GroupID"`
}

type GroupItem struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	GroupID   int    `json:"group_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ChannelID int    `json:"channel_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ModelName string `json:"model_name" gorm:"not null;index:idx_group_channel_model,unique"`
	Priority  int    `json:"priority" gorm:"default:1"`
}
