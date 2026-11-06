package model

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// 上游账单对单: 渠道商把上游供应商的账单(通常按月出, 带原币成本)导入系统,
// 与本平台按 billing_records 记的 owner_revenue 逐渠道逐模型核对。
// 为什么只登记差异不改数: 上游账单的口径(含税/阶梯折扣/取整)平台无法复原,
// 任何自动"修正"都是在猜供应商的意图; 对单的价值在于发现量级错误(币种配错/价格没更新),
// 量级对了的小差异由人工判断是否可接受。

// 上游账单行的对单结论。
const (
	UpstreamBillStatusPending   = "pending"   // 待对单(刚导入)。
	UpstreamBillStatusMatched   = "matched"   // 差异在容差内。
	UpstreamBillStatusMismatch  = "mismatch"  // 超出容差, 待人工核查。
	UpstreamBillStatusExplained = "explained" // 差异已定性(折扣/税费), 留说明。
)

// UpstreamBillDiff 容差: 相对差异(差异/账单额)超过它才算 mismatch。
// 上游账单常带零头取整与汇率尾差, 1% 是"量级正确"的分界。
const UpstreamBillDiffTolerance = 0.01

// UpstreamBill 一行 = 供应商账单里一个模型一行的成本(原币)。
type UpstreamBill struct {
	ID        uint64    `json:"id" gorm:"primaryKey"`
	ChannelID int       `json:"channel_id" gorm:"not null;index:idx_upstream_bill_unique,priority:1"`          // 本平台渠道 ID(账单按渠道导入)。
	Period    string    `json:"period" gorm:"not null;index:idx_upstream_bill_unique,priority:2;size:16"`      // 账期 YYYYMM。
	ModelName string    `json:"model_name" gorm:"not null;index:idx_upstream_bill_unique,priority:3;size:255"` // 上游模型名(与本平台模型名同源)。
	Currency  string    `json:"currency" gorm:"size:8;not null"`                                               // 账单成本币种。
	Cost      float64   `json:"cost" gorm:"type:decimal(18,6);not null"`                                       // 账单原币成本。
	CostUSD   float64   `json:"cost_usd" gorm:"type:decimal(18,6)"`                                            // 折算后 USD 成本(导入时按牌价快照折)。
	FxRate    float64   `json:"fx_rate" gorm:"default:1"`                                                      // 导入时所用汇率(1 原币折 USD), 供复核。
	OwnerRev  float64   `json:"owner_rev" gorm:"type:decimal(18,6)"`                                           // 同期同模型本平台 owner_revenue 合计(USD)。
	DiffRatio float64   `json:"diff_ratio" gorm:"type:decimal(10,6)"`                                          // (OwnerRev-CostUSD)/CostUSD; CostUSD=0 时不计。
	Status    string    `json:"status" gorm:"size:16;not null;default:pending"`                                // pending|matched|mismatch|explained。
	Note      string    `json:"note" gorm:"type:text"`                                                         // 人工说明(解释差异时写)。
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (UpstreamBill) TableName() string { return "upstream_bills" }

// ValidateUpstreamBillAmount 账单成本必须是非负有限数; 负成本几乎必然是抄错符号。
func ValidateUpstreamBillAmount(cost float64) error {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 || cost > 1e9 {
		return fmt.Errorf("bill cost must be a finite non-negative number")
	}
	return nil
}

// NormalizeUpstreamBillPeriod 账期归一: 接受 YYYY-MM 或 YYYYMM, 统一成 YYYYMM。
func NormalizeUpstreamBillPeriod(period string) string {
	p := strings.TrimSpace(strings.ReplaceAll(period, "-", ""))
	return p
}
