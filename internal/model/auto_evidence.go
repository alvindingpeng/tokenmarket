package model

import "time"

// AutoToolProbeRun stores a probe outcome for a specific upstream grant revision.
type AutoToolProbeRun struct {
	ID         uint64                 `gorm:"primaryKey" json:"id"`
	GrantID    int                    `gorm:"index:idx_auto_probe_grant_id,priority:1" json:"grant_id"`
	Revision   string                 `gorm:"size:64" json:"-"`
	StartedAt  time.Time              `gorm:"index:idx_auto_probe_grant_id,priority:2" json:"started_at"`
	CreatedAt  time.Time              `json:"created_at"`
	Success    bool                   `json:"success"`
	Evaluation []ModelProbeEvaluation `gorm:"serializer:json" json:"evaluation,omitempty"`
}

// AutoSessionBinding stores a scoped digest of an explicit client session.
type AutoSessionBinding struct {
	Key       string `gorm:"primaryKey;size:64"`
	GroupID   int    `gorm:"index"`
	ItemID    int
	Revision  string `gorm:"size:64"`
	StartedAt time.Time
	ExpiresAt time.Time `gorm:"index"`
}
