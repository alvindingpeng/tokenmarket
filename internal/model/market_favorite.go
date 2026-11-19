package model

import (
	"time"

)

// MarketFavorite 是用户对广场某个模型名的收藏。
//
// 为什么按模型名而不是渠道模型 ID: 广场的展示单位是"模型名"(一个模型跨多渠道折叠成一行),
// 用户收藏的是"我要用 claude-sonnet-5", 不是"#1001 渠道上的那一条"。渠道上下架、换授权都不该让收藏失效;
// 按渠道模型 ID 收藏则渠道一停用收藏就指向空行, 用户会以为收藏丢了。
type MarketFavorite struct {
	ID     uint   `json:"id" gorm:"primaryKey"`
	UserID uint   `json:"user_id" gorm:"not null;uniqueIndex:idx_market_favorite_user_model,priority:1"`
	Model  string `json:"model" gorm:"size:128;not null;uniqueIndex:idx_market_favorite_user_model,priority:2"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (MarketFavorite) TableName() string { return "market_favorites" }