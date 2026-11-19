package model

import "time"

// AlertSilenceWindow 告警静默窗口: 计划内维护/部署期间, 告警照常落库但不外发。
//
// 为什么不做成"丢弃": 静默只是不打扰, 不是否认故障 —— 落库保留记录, 运维页照常可见,
// 事后可以按时间范围回看静默期内发生了什么。外发(Telegram/webhook)才被跳过。
//
// 为什么是窗口而不是开关: 开关只有全局一个状态, 忘了关就是灾难 —— 部署完告警静默到下周。
// 窗口必须带截止时间, 到点自动恢复, 无需人工记得去关。
type AlertSilenceWindow struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// StartAt/EndAt 静默起止(RFC3339 由 API 层校验); EndAt 必须晚于 StartAt。
	StartAt time.Time `json:"start_at" gorm:"not null;index"`
	EndAt   time.Time `json:"end_at" gorm:"not null"`
	// Kind 告警类型过滤; 空串表示静默全部类型(部署场景), 指定类型则只静默该类(如 backup_failed)。
	Kind string `json:"kind" gorm:"size:32;index;not null;default:''"`
	// Note 静默原因(如 "v1.5.22 部署"), 留痕用; 运维页展示。
	Note string `json:"note" gorm:"size:200;not null;default:''"`
	// CreatedBy 创建者用户 ID, 0 表示系统。
	CreatedBy uint      `json:"created_by" gorm:"not null;default:0"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名。
func (AlertSilenceWindow) TableName() string { return "alert_silence_windows" }

// AlertSilenceActive 判断一个窗口当前是否生效。
// 判定用"现在在 [start, end) 内": end 取开区间, 让"到点即恢复"语义干净 ——
// end 恰好等于 now 时窗口已结束, 不需要容忍一秒的边界抖动。
func (w AlertSilenceWindow) AlertSilenceActive(now time.Time) bool {
	return !now.Before(w.StartAt) && now.Before(w.EndAt)
}

// AlertSilenceWindowInput 创建静默窗口的请求体。
type AlertSilenceWindowInput struct {
	StartAt string `json:"start_at" binding:"required,datetime=RFC3339"`
	EndAt   string `json:"end_at" binding:"required,datetime=RFC3339"`
	Kind    string `json:"kind"`
	Note    string `json:"note"`
}

// AlertSilenceMaxActive 同时生效的窗口数上限。
// 足够覆盖"多类告警各自静默 + 一个全局窗口"; 超出它说明有人在滥用静默当开关用。
const AlertSilenceMaxActive = 10
