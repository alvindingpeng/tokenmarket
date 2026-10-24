package model

import (
	"fmt"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role 用户角色: 管理员保留全部功能; 渠道商拥有渠道管理与共享发布; 用户为终端消费者。
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReseller Role = "reseller"
	RoleUser     Role = "user"
)

// UserStatus 账号状态: 注册默认关闭, 开启后按设置决定是否需要审批。
type UserStatus string

const (
	StatusActive   UserStatus = "active"
	StatusPending  UserStatus = "pending"
	StatusDisabled UserStatus = "disabled"
)

// PasswordMinLen 密码最短长度: 注册/改密/找回密码统一收紧到 8 位。
const PasswordMinLen = 8

// emailPattern 邮箱基本格式; 不追求 RFC 全集, 只挡明显非法输入。
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ValidEmail 判断邮箱基本格式。
func ValidEmail(email string) bool {
	return emailPattern.MatchString(email)
}

// User 平台账号; 余额三字段承担计费: Balance 可用余额, Frozen 预扣冻结, 其余为累计口径。
type User struct {
	ID       uint   `json:"id" gorm:"primaryKey"`
	Username string `json:"username" gorm:"unique;not null"`
	Password string `json:"-" gorm:"not null"`
	// Email 注册邮箱(注册必填, 找回密码凭据); 空串表示老账号未绑定, 唯一性由应用层保证。
	Email  string     `json:"email" gorm:"size:254;not null;default:'';index"`
	Role   Role       `json:"role" gorm:"not null;default:user;index"`
	Status UserStatus `json:"status" gorm:"not null;default:active;index"`
	// TokenVersion 随改密/禁用递增, 使既有 JWT 全部失效。
	TokenVersion int `json:"-" gorm:"not null;default:0"`
	// 计费口径: Balance 可用余额; Frozen 预扣冻结额(可用余额 = Balance - Frozen 的语义由扣费 SQL 保证);
	// TotalSpent 用户累计支出(上浮后价格); TotalRevenue 渠道商累计收入(供货价口径)。
	Balance      float64 `json:"balance" gorm:"type:decimal(18,6);not null;default:0"`
	Frozen       float64 `json:"frozen" gorm:"type:decimal(18,6);not null;default:0"`
	TotalSpent   float64 `json:"total_spent" gorm:"type:decimal(18,6);not null;default:0"`
	TotalRevenue float64 `json:"total_revenue" gorm:"type:decimal(18,6);not null;default:0"`
	// TotalRecharged 预留: 充值功能上线后累计充值额。
	TotalRecharged float64 `json:"total_recharged" gorm:"type:decimal(18,6);not null;default:0"`
	// 收益池提现口径(P3): 可提 = TotalRevenue - TotalWithdrawn - WithdrawFrozen;
	// 提现只在这三个字段之间迁移, 永不动 Balance(充值款不可提现, 防套现)。
	TotalWithdrawn float64   `json:"total_withdrawn" gorm:"type:decimal(18,6);not null;default:0"`
	WithdrawFrozen float64   `json:"withdraw_frozen" gorm:"type:decimal(18,6);not null;default:0"`
	CreatedAt      time.Time `json:"created_at"`
}

type UserLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Expire   int    `json:"expire"`
}

// UserRegister 注册请求; 不收 role, 角色由注册端点决定, 防止自提权。
// 密码输入两遍(Password/PasswordConfirm), 邮箱验证码(EmailCode)校验通过才允许注册。
type UserRegister struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
	Email           string `json:"email"`
	EmailCode       string `json:"email_code"`
	Reason          string `json:"reason"` // 渠道商注册的申请说明。
}

// UserEmailCode 发送邮箱验证码请求; purpose: register(注册) | reset(找回密码)。
type UserEmailCode struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

// UserForgotPassword 找回密码请求: 邮箱验证码通过后重设密码, 同样输入两遍。
type UserForgotPassword struct {
	Email           string `json:"email"`
	EmailCode       string `json:"email_code"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

type UserChangePassword struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type UserChangeUsername struct {
	NewUsername string `json:"new_username"`
}

// UserManageRequest 管理员用户管理请求; 字段按操作语义选用。
// Balance 为指针: 未提交表示不调整余额, 提交 0 表示清零。
type UserManageRequest struct {
	ID       uint       `json:"id"`
	Username string     `json:"username"`
	Email    string     `json:"email"`
	Role     Role       `json:"role"`
	Status   UserStatus `json:"status"`
	Password string     `json:"password"`
	Balance  *float64   `json:"balance"`
}

// UserView 对外展示的账号信息, 不含密码与令牌版本。
type UserView struct {
	ID             uint       `json:"id"`
	Username       string     `json:"username"`
	Email          string     `json:"email"`
	Role           Role       `json:"role"`
	Status         UserStatus `json:"status"`
	Balance        float64    `json:"balance"`
	Frozen         float64    `json:"frozen"`
	TotalSpent     float64    `json:"total_spent"`
	TotalRevenue   float64    `json:"total_revenue"`
	TotalWithdrawn float64    `json:"total_withdrawn"`
	WithdrawFrozen float64    `json:"withdraw_frozen"`
	// MustChangePassword 仅登录/状态响应携带: 默认管理员未改初始密码时为真, 前端据此强制引导改密。
	MustChangePassword bool      `json:"must_change_password,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// View 返回账号的对外副本。
func (u *User) View() UserView {
	return UserView{
		ID:             u.ID,
		Username:       u.Username,
		Email:          u.Email,
		Role:           u.Role,
		Status:         u.Status,
		Balance:        u.Balance,
		Frozen:         u.Frozen,
		TotalSpent:     u.TotalSpent,
		TotalRevenue:   u.TotalRevenue,
		TotalWithdrawn: u.TotalWithdrawn,
		WithdrawFrozen: u.WithdrawFrozen,
		CreatedAt:      u.CreatedAt,
	}
}

func (u *User) HashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	u.Password = string(hashedPassword)
	return nil
}

func (u *User) ComparePassword(password string) error {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password))
}
