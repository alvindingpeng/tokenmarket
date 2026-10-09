package op

import (
	"context"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
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
