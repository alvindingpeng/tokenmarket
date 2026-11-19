package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 45, Up: addUserTenantID})
}

// addUserTenantID 给 users 表加 tenant_id 列并建立默认租户。
func addUserTenantID(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// 先确保有一个默认租户 (id=1)
	if db.Migrator().HasTable("tenants") {
		var count int64
		if err := db.Table("tenants").Count(&count).Error; err == nil && count == 0 {
			// 创建默认租户
			if err := db.Exec("INSERT INTO tenants (id, name, code, status, created_at, updated_at) VALUES (1, 'Default', 'default', 'active', datetime('now'), datetime('now'))").Error; err != nil {
				return fmt.Errorf("create default tenant: %w", err)
			}
			// 同时创建默认租户的钱包和配额
			db.Exec("INSERT INTO tenant_wallets (tenant_id, balance, frozen, total_recharged, total_spent, total_revenue, total_withdrawn, withdraw_frozen, created_at, updated_at) VALUES (1, 0, 0, 0, 0, 0, 0, 0, datetime('now'), datetime('now'))")
			db.Exec("INSERT INTO tenant_quotas (tenant_id, requests_limit, input_tokens_limit, output_tokens_limit, period, reset_day, created_at, updated_at) VALUES (1, 0, 0, 0, 'monthly', 1, datetime('now'), datetime('now'))")
		}
	}

	// 给 users 表加 tenant_id 列
	if db.Migrator().HasTable("users") {
		if !db.Migrator().HasColumn("users", "tenant_id") {
			if err := db.Exec("ALTER TABLE users ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 1").Error; err != nil {
				return fmt.Errorf("add tenant_id to users: %w", err)
			}
			if err := db.Exec("CREATE INDEX idx_users_tenant_id ON users(tenant_id)").Error; err != nil {
				return fmt.Errorf("create users tenant_id index: %w", err)
			}
		}
	}

	return nil
}
