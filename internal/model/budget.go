package model

import (
	"strconv"
	"time"
)

// SpendBudget 用户/Key 级花费预算: 按 user_cost 口径对每个周期设上限, 超出时警告或拦截。
//
// 为什么不复用 API Key 的 max_cost: max_cost 是 Key 的终身上限(到达即永久停用, 管理员手动处理),
// 预算是周期性的(每天/每月自动重置), 由用户或管理员自助设定 —— 语义是"这个月我打算花多少",
// 不是"这个 Key 一生最多花多少"。
//
// 为什么分 warn/block 两档: block 拦在请求前(预扣之前), warn 只发一条去重通知不打扰请求 ——
// 预算应"接近"就提醒, 不该到 100% 才让用户从失败请求里得知。
type SpendBudget struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// UserID 用户级预算; 与 APIKeyID 二选一, 两者都为 0 无效。
	UserID uint `json:"user_id" gorm:"index;not null;default:0"`
	// APIKeyID Key 级预算; billing_records 按 api_key_id 聚合。
	APIKeyID int `json:"api_key_id" gorm:"index;not null;default:0"`
	// Period 周期: day | month。月按自然月(本地时区), 日按自然日, 与 quota 的重置口径一致。
	Period string `json:"period" gorm:"size:8;not null;default:'day'"`
	// LimitAmount 上限(user_cost 口径, 精确到 6 位小数, 与账目一致)。0 表示无限制。
	LimitAmount float64 `json:"limit_amount" gorm:"type:decimal(18,6);not null;default:0"`
	// Action 达限行为: warn(只通知) | block(拦截新请求)。
	Action string `json:"action" gorm:"size:8;not null;default:'warn'"`
	// WarnRatio 触发警告的比例(0-100), 默认 80: 花费达到上限的 80% 即通知。
	WarnRatio int       `json:"warn_ratio" gorm:"not null;default:80"`
	Enabled   bool      `json:"enabled" gorm:"not null;default:true"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (SpendBudget) TableName() string { return "spend_budgets" }

// 预算周期与行为闭集。
const (
	BudgetPeriodDay   = "day"
	BudgetPeriodMonth = "month"

	BudgetActionWarn  = "warn"
	BudgetActionBlock = "block"
)

// ValidBudgetPeriod / ValidBudgetAction 闭集校验, 创建与更新共用。
func ValidBudgetPeriod(p string) bool { return p == BudgetPeriodDay || p == BudgetPeriodMonth }
func ValidBudgetAction(a string) bool { return a == BudgetActionWarn || a == BudgetActionBlock }

// BudgetExceeded 预算拦截错误: relay 侧与 QuotaExceeded 同样处理(直接拒绝请求)。
type BudgetExceeded struct {
	Scope   string // user:<id> 或 api_key:<id>
	Current float64
	Limit   float64
}

func (e BudgetExceeded) Error() string {
	return "budget exceeded: " + e.Scope + " spent " + strconv.FormatFloat(e.Current, 'f', 2, 64) + "/" + strconv.FormatFloat(e.Limit, 'f', 2, 64)
}
