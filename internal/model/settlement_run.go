package model

import "time"

// SettlementRun 是已成功生成的日账期 checkpoint。没有记录表示待补偿,
// 包括生成失败、checkpoint 写入失败以及没有运行过的账期。
// 空日也会记录成功; 不用于手动生成, 不记录审核或付款状态。
type SettlementRun struct {
	Period      string    `json:"period" gorm:"primaryKey;size:8;not null"`
	SucceededAt time.Time `json:"succeeded_at" gorm:"not null"`
}

func (SettlementRun) TableName() string { return "settlement_runs" }
