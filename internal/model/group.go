package model

type Group struct {
	ID    int         `json:"id" gorm:"primaryKey"`
	Name  string      `json:"name" gorm:"unique;not null"`
	Items []GroupItem `json:"items,omitempty" gorm:"foreignKey:GroupID"`
}

type GroupItem struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	GroupID   int    `json:"group_id" gorm:"not null;index:idx_group_channel_model,unique"` // 创建时不携带此字段,更新时需要
	ChannelID int    `json:"channel_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ModelName string `json:"model_name" gorm:"not null;index:idx_group_channel_model,unique"`
	Priority  int    `json:"priority" gorm:"default:1"`
}

// GroupUpdateRequest 分组更新请求 - 仅包含变更的数据
type GroupUpdateRequest struct {
	ID            int                      `json:"id" binding:"required"`
	Name          *string                  `json:"name,omitempty"`            // 仅在名称变更时发送
	ItemsToAdd    []GroupItemAddRequest    `json:"items_to_add,omitempty"`    // 新增的 items
	ItemsToUpdate []GroupItemUpdateRequest `json:"items_to_update,omitempty"` // 更新的 items (priority 变更)
	ItemsToDelete []int                    `json:"items_to_delete,omitempty"` // 删除的 item IDs
}

// GroupItemAddRequest 新增 item 请求
type GroupItemAddRequest struct {
	ChannelID int    `json:"channel_id" binding:"required"`
	ModelName string `json:"model_name" binding:"required"`
	Priority  int    `json:"priority" binding:"required"`
}

// GroupItemUpdateRequest 更新 item 请求
type GroupItemUpdateRequest struct {
	ID       int `json:"id" binding:"required"`
	Priority int `json:"priority" binding:"required"`
}
