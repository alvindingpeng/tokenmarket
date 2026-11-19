package model

import "time"

// DisputeStatus 交易申诉/退款状态
const (
	DisputeStatusPending  = "pending"  // 待审核
	DisputeStatusApproved = "approved" // 已批准并退款
	DisputeStatusRejected = "rejected" // 已驳回
)

// Dispute 交易申诉单: 针对调用计费异常发起的退款申诉
type Dispute struct {
	ID         uint64     `json:"id" gorm:"primaryKey"`
	RecordID   uint64     `json:"record_id" gorm:"index;not null"`  // 关联 BillingRecord.ID
	RequestID  uint64     `json:"request_id" gorm:"index;not null"` // 关联 RelayRequest.ID
	UserID     uint       `json:"user_id" gorm:"index;not null"`    // 申诉人 (付费用户)
	OwnerID    uint       `json:"owner_id" gorm:"index;not null"`   // 渠道发布者
	ChannelID  int        `json:"channel_id"`                       // 渠道 ID
	ModelName  string     `json:"model_name" gorm:"size:128"`       // 模型名
	UserCost   float64    `json:"user_cost" gorm:"type:decimal(18,6);not null"`
	Reason     string     `json:"reason" gorm:"size:512;not null"` // 申诉原因/问题描述
	Status     string     `json:"status" gorm:"size:16;not null;default:'pending';index"`
	ReviewNote string     `json:"review_note" gorm:"size:512;not null;default:''"` // 审核备注
	ReviewedBy uint       `json:"reviewed_by" gorm:"not null;default:0"`           // 审核管理员 ID
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at" gorm:"index"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (Dispute) TableName() string { return "disputes" }
