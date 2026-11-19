package model

import "time"

// EmailCodePurpose 验证码用途: 注册与找回密码各自独立, 防止跨用途挪用验证码。
const (
	EmailCodePurposeRegister = "register"
	EmailCodePurposeReset    = "reset"
)

// EmailCode 邮箱验证码: 只存哈希不存明文(备份导出含全表, 明文验证码等于随备份外泄)。
// 一邮箱一用途同一时刻仅一条有效, 校验成功即作废。
type EmailCode struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	Email     string     `json:"email" gorm:"size:254;not null;index"`
	Purpose   string     `json:"purpose" gorm:"size:16;not null"`
	CodeHash  string     `json:"-" gorm:"size:64;not null"`
	Attempts  int        `json:"attempts" gorm:"not null;default:0"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at"`
}
