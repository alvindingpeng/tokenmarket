package model

import "time"

// AuditLog 管理员操作审计记录: 谁在何时对什么目标做了什么动作、变更明细为何。
type AuditLog struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	UserID    uint      `json:"user_id" gorm:"index"` // 操作者账号 ID。
	Username  string    `json:"username"`             // 操作者用户名快照, 账号改名后历史仍可读。
	Action    string    `json:"action" gorm:"index"`  // 动作标识, 如 user.update、setting.set。
	Target    string    `json:"target"`               // 目标描述, 如 username#id、设置键、渠道号。
	Detail    string    `json:"detail"`               // 变更明细文本。
	CreatedAt time.Time `json:"created_at"`
}
