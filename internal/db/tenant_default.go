package db

import (
	"fmt"

	"gorm.io/gorm"
)

// ensureDefaultTenant 确保默认租户(id=1)存在。
func ensureDefaultTenant(db *gorm.DB) error {
	var count int64
	if err := db.Table("tenants").Where("id = ?", 1).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		if err := db.Exec("INSERT INTO tenants (id, name, code, status, created_at, updated_at) VALUES (1, 'Default', 'default', 'active', datetime('now'), datetime('now'))").Error; err != nil {
			return fmt.Errorf("create default tenant: %w", err)
		}
		// Create default quota, wallet, and pricing policy for tenant 1
		if err := db.Exec("INSERT INTO tenant_quotas (tenant_id, requests_limit, input_tokens_limit, output_tokens_limit, period, reset_day, created_at, updated_at) VALUES (1, 0, 0, 0, 'monthly', 1, datetime('now'), datetime('now'))").Error; err != nil {
			return fmt.Errorf("create default tenant quota: %w", err)
		}
		if err := db.Exec("INSERT INTO tenant_wallets (tenant_id, balance, frozen, total_recharged, total_spent, total_revenue, total_withdrawn, withdraw_frozen, created_at, updated_at) VALUES (1, 0, 0, 0, 0, 0, 0, 0, datetime('now'), datetime('now'))").Error; err != nil {
			return fmt.Errorf("create default tenant wallet: %w", err)
		}
		if err := db.Exec("INSERT INTO tenant_pricing_policies (tenant_id, channel_id, model_name, markup_ratio, currency, created_at, updated_at) VALUES (1, NULL, '', -1, '', datetime('now'), datetime('now'))").Error; err != nil {
			return fmt.Errorf("create default tenant pricing policy: %w", err)
		}
	}
	return nil
}
