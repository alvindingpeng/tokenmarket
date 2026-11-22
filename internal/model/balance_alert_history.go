package model

import "time"

// BalanceAlertHistory records an actual, dedup-winning balance alert trigger.
// It stores only when it happened and what generated it.
type BalanceAlertHistory struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"index;not null"`
	TriggeredAt time.Time `json:"triggered_at" gorm:"index;not null"`
	Source      string    `json:"source" gorm:"not null"`
}
