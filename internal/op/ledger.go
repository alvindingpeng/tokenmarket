package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// ledgerRecord 在调用方的事务内写一条余额流水。
// 与余额更新同事务: 流水与余额必须同生同灭, 否则对账会出现无法解释的差额。
func ledgerRecord(tx *gorm.DB, entry model.BillingLedger) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if err := tx.Create(&entry).Error; err != nil {
		return fmt.Errorf("failed to write balance ledger: %w", err)
	}
	return nil
}

// balanceOf 读取事务内某用户的当前可用余额; 读不到按 0 记, 不影响余额本身。
func balanceOf(tx *gorm.DB, userID uint) float64 {
	var balance float64
	if err := tx.Model(&model.User{}).Where("id = ?", userID).Select("balance").Scan(&balance).Error; err != nil {
		return 0
	}
	return balance
}

// userTenantOf 读取事务内某用户所属租户; 读不到按 0(无租户)返回。
// 0 表示这笔资金不参与租户级闸门 —— 与租户 ID 空间无歧义, 因为租户 ID 从 1 开始。
func userTenantOf(tx *gorm.DB, userID uint) uint {
	var tenantID uint
	if err := tx.Model(&model.User{}).Where("id = ?", userID).Select("tenant_id").Scan(&tenantID).Error; err != nil {
		return 0
	}
	return tenantID
}

// tenantBalanceOf 读取事务内某租户的当前可用余额(扣冻结后); 读不到按 0 记。
func tenantBalanceOf(tx *gorm.DB, tenantID uint) float64 {
	if tenantID == 0 {
		return 0
	}
	var balance float64
	if err := tx.Model(&model.TenantWallet{}).Where("tenant_id = ?", tenantID).
		Select("balance").Scan(&balance).Error; err != nil {
		return 0
	}
	return balance
}

// LedgerQuery 余额流水查询条件; 零值表示不限。
type LedgerQuery struct {
	UserID uint
	Kind   string
	From   time.Time
	To     time.Time
	Limit  int
	Offset int
}

// LedgerList 倒序分页返回余额流水与命中总数。
func LedgerList(ctx context.Context, query LedgerQuery) ([]model.BillingLedger, int64, error) {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	build := func() *gorm.DB {
		statement := db.GetDB().WithContext(ctx).Model(&model.BillingLedger{})
		if query.UserID != 0 {
			statement = statement.Where("user_id = ?", query.UserID)
		}
		if query.Kind != "" {
			statement = statement.Where("kind = ?", query.Kind)
		}
		if !query.From.IsZero() {
			statement = statement.Where("created_at >= ?", query.From)
		}
		if !query.To.IsZero() {
			statement = statement.Where("created_at <= ?", query.To)
		}
		return statement
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count balance ledgers: %w", err)
	}
	entries := []model.BillingLedger{}
	if err := build().Order("id DESC").Limit(limit).Offset(offset).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list balance ledgers: %w", err)
	}
	return entries, total, nil
}
