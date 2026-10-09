package model

import "time"

// BillingRecord 一次调用的计费明细。
// 三方账目在同一行对平: UserCost = OwnerRevenue + PlatformRevenue;
// OwnerRevenue 按供货价计入渠道归属者(渠道归属 admin 时也记 owner, 平台收入为上浮部分)。
type BillingRecord struct {
	ID         uint64 `json:"id" gorm:"primaryKey"`
	RequestID  uint64 `json:"request_id" gorm:"index"`
	UserID     uint   `json:"user_id" gorm:"index"` // 付费方: API Key 归属用户。
	APIKeyID   int    `json:"api_key_id"`
	GroupID    int    `json:"group_id"`
	GroupModel string `json:"group_model"` // 客户端模型名, 即分组名。
	ChannelID  int    `json:"channel_id"`
	OwnerID    uint   `json:"owner_id" gorm:"index"` // 发布者: 渠道归属用户, 收入按供货价计入。
	ShareCode  string `json:"share_code"`
	ModelName  string `json:"model_name"`

	SupplyPrice LLMPrice `json:"supply_price" gorm:"serializer:json"` // 供货价快照(读/写/缓存读/缓存写)。
	UserPrice   LLMPrice `json:"user_price" gorm:"serializer:json"`   // 用户价快照(供货价上浮后)。
	MarkupRatio float64  `json:"markup_ratio"`                        // 结算时的上浮比例快照, 供账单复核; 改价不影响历史解释。
	PriceSource string   `json:"price_source" gorm:"size:32"`         // 价格口径: channel_model 表示渠道模型发布价。

	InputToken      int64 `json:"input_token"`
	OutputToken     int64 `json:"output_token"`
	CacheReadToken  int64 `json:"cache_read_token"`
	CacheWriteToken int64 `json:"cache_write_token"`

	// 媒体计量与定价快照; 仅媒体形态模型(image/video)的行非零, token 四列保持 0。
	// 独立成列而不复用 SupplyPrice: 旧账单行的 supply_price 仍是 token 四类快照, 反序列化不受影响。
	MediaSupply MediaPrice `json:"media_supply,omitempty" gorm:"serializer:json"` // 媒体供货价快照(每张/每秒/分档)。
	MediaUser   MediaPrice `json:"media_user,omitempty" gorm:"serializer:json"`   // 媒体用户价快照(上浮后)。
	MediaUnits  MediaUnits `json:"media_units,omitempty" gorm:"serializer:json"`  // 媒体用量(张数/秒数/档位)。

	UserCost        float64 `json:"user_cost" gorm:"type:decimal(18,6)"`        // 用户支出(上浮后价格), 媒体行含按张/按秒部分。
	OwnerRevenue    float64 `json:"owner_revenue" gorm:"type:decimal(18,6)"`    // 发布者收入(供货价)。
	PlatformRevenue float64 `json:"platform_revenue" gorm:"type:decimal(18,6)"` // 平台收入(上浮部分)。

	Status    string    `json:"status"` // settled | refunded
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

// BalanceReservation 余额预扣记录; 请求结束时结算, 超时未结算由定时任务回滚冻结额。
type BalanceReservation struct {
	ID        uint64    `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"index"`
	RequestID uint64    `json:"request_id" gorm:"index"`
	Amount    float64   `json:"amount" gorm:"type:decimal(18,6)"`
	Settled   bool      `json:"settled" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
}

// BillingStatus 计费记录状态。
const (
	BillingStatusSettled  = "settled"
	BillingStatusRefunded = "refunded"
)
