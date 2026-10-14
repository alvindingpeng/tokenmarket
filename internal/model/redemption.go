package model

import "time"

// RedemptionBatch 兑换码批次: 一次生成的一组同面额兑换码, 便于按活动发放、限量与追溯。
type RedemptionBatch struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	Name         string     `json:"name" gorm:"size:64;not null"`
	Credits      float64    `json:"credits" gorm:"type:decimal(18,6);not null"`                    // 每张面额(余额单位)。
	Count        int        `json:"count" gorm:"not null"`                                          // 生成张数。
	Prefix       string     `json:"prefix" gorm:"size:8"`                                           // 码前缀, 便于识别活动来源。
	PerUserLimit int        `json:"per_user_limit" gorm:"not null;default:0"`                       // 单用户可兑张数上限; 0 表示不限。
	ExpiresAt    *time.Time `json:"expires_at"`                                                     // 过期时间; 空表示不过期。
	CreatedBy    uint       `json:"created_by" gorm:"index"`
	CreatedAt    time.Time  `json:"created_at"`
}

// 兑换码状态: enabled 可兑, used 已兑, disabled 已禁用。
const (
	RedemptionStatusEnabled  = "enabled"
	RedemptionStatusUsed     = "used"
	RedemptionStatusDisabled = "disabled"
)

// RedemptionCode 兑换码: 兑换以状态条件更新抢占, 并发重复兑换至多一次成功。
type RedemptionCode struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	BatchID   uint       `json:"batch_id" gorm:"index"`
	Code      string     `json:"code" gorm:"size:40;not null;uniqueIndex"`
	Credits   float64    `json:"credits" gorm:"type:decimal(18,6);not null"`
	Status    string     `json:"status" gorm:"size:12;not null;default:enabled;index"`
	UsedBy    *uint      `json:"used_by" gorm:"index"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}
