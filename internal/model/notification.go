package model

import (
	"time"
)

// NotificationType 通知类型
type NotificationType string

const (
	NotificationTypeSystem         NotificationType = "system"          // 系统通知
	NotificationTypeBalanceLow     NotificationType = "balance_low"     // 余额不足
	NotificationTypeRateLimitHit   NotificationType = "ratelimit_hit"   // 限流触发
	NotificationTypeChannelDown    NotificationType = "channel_down"    // 渠道故障
	NotificationTypeChannelReview  NotificationType = "channel_review"  // 渠道审核
	NotificationTypeWithdrawal     NotificationType = "withdrawal"      // 提现审核
	NotificationTypeDispute        NotificationType = "dispute"         // 争议处理
	NotificationTypeRecharge       NotificationType = "recharge"        // 充值成功
	NotificationTypeTicket         NotificationType = "ticket"          // 工单回复
)

// NotificationStatus 通知状态
type NotificationStatus string

const (
	NotificationStatusUnread NotificationStatus = "unread" // 未读
	NotificationStatusRead   NotificationStatus = "read"   // 已读
)

// Notification 通知记录
type Notification struct {
	ID        uint64             `json:"id" gorm:"primaryKey"`
	UserID    uint               `json:"user_id" gorm:"index:idx_notification_user,priority:1;not null"`
	Type      NotificationType   `json:"type" gorm:"size:32;index;not null"`
	Title     string             `json:"title" gorm:"size:255;not null"`
	Content   string             `json:"content" gorm:"type:text;not null"`
	Link      string             `json:"link" gorm:"size:512"`                                               // 关联跳转链接（可选）
	RelatedID uint64             `json:"related_id" gorm:"index"`                                            // 关联实体 ID（如渠道 ID、工单 ID）
	Status    NotificationStatus `json:"status" gorm:"size:16;index:idx_notification_user,priority:2;not null;default:'unread'"`
	CreatedAt time.Time          `json:"created_at" gorm:"autoCreateTime;index"`
	ReadAt    *time.Time         `json:"read_at,omitempty"`
}

// NotificationListRequest 通知列表请求
type NotificationListRequest struct {
	Status   NotificationStatus `form:"status"`                        // 筛选状态（可选）
	Type     NotificationType   `form:"type"`                          // 筛选类型（可选）
	Page     int                `form:"page" binding:"omitempty,min=1"`
	PageSize int                `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// NotificationMarkReadRequest 标记已读请求
type NotificationMarkReadRequest struct {
	IDs []uint64 `json:"ids" binding:"required,min=1"` // 通知 ID 列表
}

// NotificationUnreadCountResponse 未读数量响应
type NotificationUnreadCountResponse struct {
	UnreadCount int64 `json:"unread_count"`
}
