package model

import (
	"time"
)

// Tenant 租户/组织实体。多租户 SaaS 的核心隔离单元。
type Tenant struct {
	ID        uint         `json:"id" gorm:"primaryKey"`
	Name      string       `json:"name" gorm:"unique;not null"`              // 租户显示名称
	Code      string       `json:"code" gorm:"uniqueIndex;size:32;not null"` // 租户代码, 用于子域名/路由标识, 仅字母数字连字符
	Status    TenantStatus `json:"status" gorm:"not null;default:active;index"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// TenantStatus 租户状态。
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"   // 正常使用
	TenantStatusDisabled TenantStatus = "disabled" // 禁用, 该租户下所有用户/资源不可用
	TenantStatusPending  TenantStatus = "pending"  // 待审批(如自助注册租户)
)

// TenantQuota 租户级配额池：从平台视角分配给租户的总额度，租户内再分发给成员。
type TenantQuota struct {
	TenantID          uint      `json:"tenant_id" gorm:"primaryKey"`
	RequestsLimit     int64     `json:"requests_limit" gorm:"default:0"`                // 周期内请求数上限, 0=不限
	InputTokensLimit  int64     `json:"input_tokens_limit" gorm:"default:0"`            // 周期内输入 token 上限
	OutputTokensLimit int64     `json:"output_tokens_limit" gorm:"default:0"`           // 周期内输出 token 上限
	Period            string    `json:"period" gorm:"size:16;not null;default:monthly"` // 重置周期: daily/weekly/monthly
	ResetDay          int       `json:"reset_day" gorm:"default:1"`                     // 月重置日(1-28), 周重置周几(0-6)
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TenantWallet 租户级钱包：余额/冻结/充值/提现在租户层汇总，用户层改为额度分配。
type TenantWallet struct {
	TenantID       uint      `json:"tenant_id" gorm:"primaryKey"`
	Balance        float64   `json:"balance" gorm:"type:decimal(18,6);not null;default:0"`         // 可用余额
	Frozen         float64   `json:"frozen" gorm:"type:decimal(18,6);not null;default:0"`          // 预扣冻结
	TotalRecharged float64   `json:"total_recharged" gorm:"type:decimal(18,6);not null;default:0"` // 累计充值
	TotalSpent     float64   `json:"total_spent" gorm:"type:decimal(18,6);not null;default:0"`     // 累计支出(用户价口径)
	TotalRevenue   float64   `json:"total_revenue" gorm:"type:decimal(18,6);not null;default:0"`   // 累计收入(供货价口径, 渠道商收益池)
	TotalWithdrawn float64   `json:"total_withdrawn" gorm:"type:decimal(18,6);not null;default:0"` // 累计提现
	WithdrawFrozen float64   `json:"withdraw_frozen" gorm:"type:decimal(18,6);not null;default:0"` // 提现冻结
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TenantPricingPolicy 租户级定价策略：基础上浮 + 分模型/分渠道覆盖。
type TenantPricingPolicy struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	TenantID    uint      `json:"tenant_id" gorm:"uniqueIndex:idx_tenant_pricing_tenant_model_channel;notnull"`
	ChannelID   *int      `json:"channel_id,omitempty" gorm:"uniqueIndex:idx_tenant_pricing_tenant_model_channel;default:null"` // null=全渠道生效
	ModelName   string    `json:"model_name,omitempty" gorm:"uniqueIndex:idx_tenant_pricing_tenant_model_channel;size:128"`     // 空=全模型生效
	MarkupRatio float64   `json:"markup_ratio" gorm:"notnull;default:-1"`                                                       // <0 表示继承上级(平台/租户默认), >=0 显式覆盖
	Currency    string    `json:"currency" gorm:"size:8"`                                                                       // 结算币种, 空=跟随渠道/平台
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UserTenantMapping 用户-租户关系（支持用户属于多个租户，但当前简化为单租户）。
// 后续若需多租户成员，可在此表加 role 字段。
type UserTenantMapping struct {
	UserID    uint      `json:"user_id" gorm:"primaryKey"`
	TenantID  uint      `json:"tenant_id" gorm:"primaryKey"`
	Role      string    `json:"role" gorm:"size:32;notnull;default:'member'"` // member/admin/owner
	CreatedAt time.Time `json:"created_at"`
}

func (Tenant) TableName() string              { return "tenants" }
func (TenantQuota) TableName() string         { return "tenant_quotas" }
func (TenantWallet) TableName() string        { return "tenant_wallets" }
func (TenantPricingPolicy) TableName() string { return "tenant_pricing_policies" }
func (UserTenantMapping) TableName() string   { return "user_tenant_mappings" }
