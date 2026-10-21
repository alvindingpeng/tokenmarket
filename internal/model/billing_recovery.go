package model

import "time"

const (
	BillingSettlementStatusPending   = "pending"
	BillingSettlementStatusRetry     = "retry"
	BillingSettlementStatusManual    = "manual"
	BillingSettlementStatusCompleted = "completed"
)

// BillingSettlementTask keeps the original economic snapshot; never replace Snapshot on conflict.
type BillingSettlementTask struct {
	ID            uint64    `json:"id" gorm:"primaryKey"`
	ReservationID uint64    `json:"reservation_id" gorm:"uniqueIndex"`
	RequestID     uint64    `json:"request_id,string"`
	UserID        uint      `json:"user_id" gorm:"index"`
	Status        string    `json:"status" gorm:"index"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"next_attempt_at" gorm:"index"`
	LastError     string    `json:"last_error" gorm:"type:text"`
	Snapshot      string    `json:"-" gorm:"type:text"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
