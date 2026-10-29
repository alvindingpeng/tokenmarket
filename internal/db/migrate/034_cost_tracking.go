package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 34,
		Up:      Migration034_CostTracking,
	})
}

// Migration034_CostTracking 增加成本追踪字段：供货成本、用户实付、毛利。
func Migration034_CostTracking(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// 1. relay_requests 表增加成本字段
	if db.Migrator().HasTable("relay_requests") {
		if !db.Migrator().HasColumn("relay_requests", "supply_cost") {
			if err := db.Exec("ALTER TABLE relay_requests ADD COLUMN supply_cost DECIMAL(20,6) DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add supply_cost to relay_requests: %w", err)
			}
		}
		if !db.Migrator().HasColumn("relay_requests", "user_charge") {
			if err := db.Exec("ALTER TABLE relay_requests ADD COLUMN user_charge DECIMAL(20,6) DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add user_charge to relay_requests: %w", err)
			}
		}
		if !db.Migrator().HasColumn("relay_requests", "profit") {
			if err := db.Exec("ALTER TABLE relay_requests ADD COLUMN profit DECIMAL(20,6) DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add profit to relay_requests: %w", err)
			}
		}
	}

	// 注: channel_keys/models/grants 的 InputCost/OutputCost 字段本身就是供货成本(传入 SupplyPrice 计算),
	// 不需要额外的 cost_usd 字段。

	return nil
}
