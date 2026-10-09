package migrate

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 16,
		Up:      backfillBalanceLedger,
	})
}

// backfillBalanceLedger 用存量预扣与计费明细回填余额流水, 让升级后用户的流水页不留空洞。
// 回填行只还原"冻结/解冻/实际费用"这几个金额事实: 历史余额快照无法重建, 故 balance_after 留 0(界面显示为未知)。
// 幂等: 表中已有任何流水即整体跳过, 重复启动不会写重复行。
func backfillBalanceLedger(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("balance_reservations") || !db.Migrator().HasTable("balance_ledgers") {
		return nil
	}
	var existing int64
	if err := db.Table("balance_ledgers").Count(&existing).Error; err != nil {
		return fmt.Errorf("failed to count balance ledgers: %w", err)
	}
	if existing > 0 {
		return nil
	}

	type reservationRow struct {
		ID        uint64
		UserID    uint
		RequestID uint64
		Amount    float64
		Settled   bool
		CreatedAt time.Time
	}
	var reservations []reservationRow
	if err := db.Table("balance_reservations").Order("id ASC").Find(&reservations).Error; err != nil {
		return fmt.Errorf("failed to load reservations: %w", err)
	}
	if len(reservations) == 0 {
		return nil
	}

	requestIDs := make([]uint64, 0, len(reservations))
	for _, reservation := range reservations {
		requestIDs = append(requestIDs, reservation.RequestID)
	}
	type settlementRow struct {
		RequestID    uint64
		UserID       uint
		GroupModel   string
		ModelName    string
		UserCost     float64
		OwnerRevenue float64
	}
	var settlements []settlementRow
	if err := db.Table("billing_records").
		Select("request_id, user_id, group_model, model_name, user_cost, owner_revenue").
		Where("request_id IN ?", requestIDs).Find(&settlements).Error; err != nil {
		return fmt.Errorf("failed to load billing records: %w", err)
	}
	byRequest := make(map[uint64]settlementRow, len(settlements))
	for _, settlement := range settlements {
		byRequest[settlement.RequestID] = settlement
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, reservation := range reservations {
			reserve := map[string]any{
				"user_id":       reservation.UserID,
				"kind":          "reserve",
				"amount":        -reservation.Amount,
				"frozen":        reservation.Amount,
				"cost":          0,
				"balance_after": 0,
				"request_id":    reservation.RequestID,
				"model_name":    byRequest[reservation.RequestID].ModelName,
				"actor_id":      0,
				"actor_name":    "",
				"note":          "历史回填",
				"created_at":    reservation.CreatedAt,
			}
			if err := tx.Table("balance_ledgers").Create(reserve).Error; err != nil {
				return fmt.Errorf("failed to backfill reserve ledger: %w", err)
			}
			settlement, settled := byRequest[reservation.RequestID]
			if !settled {
				if reservation.Settled {
					release := map[string]any{
						"user_id":       reservation.UserID,
						"kind":          "release",
						"amount":        reservation.Amount,
						"frozen":        -reservation.Amount,
						"cost":          0,
						"balance_after": 0,
						"request_id":    reservation.RequestID,
						"model_name":    "",
						"actor_id":      0,
						"actor_name":    "",
						"note":          "历史回填",
						"created_at":    reservation.CreatedAt,
					}
					if err := tx.Table("balance_ledgers").Create(release).Error; err != nil {
						return fmt.Errorf("failed to backfill release ledger: %w", err)
					}
				}
				continue
			}
			settle := map[string]any{
				"user_id":       settlement.UserID,
				"kind":          "settle",
				"amount":        reservation.Amount - settlement.UserCost,
				"frozen":        -reservation.Amount,
				"cost":          settlement.UserCost,
				"balance_after": 0,
				"request_id":    reservation.RequestID,
				"model_name":    settlement.ModelName,
				"actor_id":      0,
				"actor_name":    "",
				"note":          "历史回填",
				"created_at":    reservation.CreatedAt,
			}
			if err := tx.Table("balance_ledgers").Create(settle).Error; err != nil {
				return fmt.Errorf("failed to backfill settle ledger: %w", err)
			}
		}
		return nil
	})
}
