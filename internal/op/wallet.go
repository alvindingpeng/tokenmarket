package op

import (
	"context"
	"fmt"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// walletCreditTx 在调用方事务内入账: 增加可用余额并写余额流水, 两者同生同灭。
// 充值(topup)与兑换(redemption)同时累计 TotalRecharged 口径; 其余类型只动余额。
// 任何入账都必须走这里, 旁路直接改 balance 会让流水与余额对不上。
func walletCreditTx(tx *gorm.DB, userID uint, amount float64, kind, note string, actorID uint, actorName string) error {
	if amount <= 0 {
		return fmt.Errorf("credit amount must be positive")
	}
	updates := map[string]any{"balance": gorm.Expr("balance + ?", amount)}
	if kind == model.LedgerKindTopup || kind == model.LedgerKindRedeem {
		updates["total_recharged"] = gorm.Expr("total_recharged + ?", amount)
	}
	result := tx.Model(&model.User{}).Where("id = ?", userID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to credit balance: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("user not found")
	}
	return ledgerRecord(tx, model.BillingLedger{
		UserID:       userID,
		Kind:         kind,
		Amount:       amount,
		BalanceAfter: balanceOf(tx, userID),
		ActorID:      actorID,
		ActorName:    actorName,
		Note:         note,
	})
}

// WalletCredit 入账的对外入口(自带事务); 需要与业务写操作同事务时用 walletCreditTx。
func WalletCredit(ctx context.Context, userID uint, amount float64, kind, note string, actorID uint, actorName string) error {
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return walletCreditTx(tx, userID, amount, kind, note, actorID, actorName)
	}); err != nil {
		return err
	}
	UserTouchBalance(userID)
	return nil
}
