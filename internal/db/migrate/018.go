package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 18,
		Up:      addQuotaAlertRotationFields,
	})
}

// addQuotaAlertRotationFields 为 API Key 配额、告警通知、Key 轮换功能增加必要字段。
func addQuotaAlertRotationFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// API Key 配额字段
	if db.Migrator().HasTable("apikeys") {
		if !db.Migrator().HasColumn("apikeys", "quota_requests") {
			if err := db.Exec("ALTER TABLE apikeys ADD COLUMN quota_requests INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add quota_requests: %w", err)
			}
		}
		if !db.Migrator().HasColumn("apikeys", "quota_input_tokens") {
			if err := db.Exec("ALTER TABLE apikeys ADD COLUMN quota_input_tokens INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add quota_input_tokens: %w", err)
			}
		}
		if !db.Migrator().HasColumn("apikeys", "quota_output_tokens") {
			if err := db.Exec("ALTER TABLE apikeys ADD COLUMN quota_output_tokens INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add quota_output_tokens: %w", err)
			}
		}
		if !db.Migrator().HasColumn("apikeys", "quota_reset_hour") {
			if err := db.Exec("ALTER TABLE apikeys ADD COLUMN quota_reset_hour INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add quota_reset_hour: %w", err)
			}
		}
	}

	// Channel Key 轮换策略字段
	if db.Migrator().HasTable("channels") {
		if !db.Migrator().HasColumn("channels", "key_rotation_strategy") {
			if err := db.Exec("ALTER TABLE channels ADD COLUMN key_rotation_strategy TEXT NOT NULL DEFAULT 'roundrobin'").Error; err != nil {
				return fmt.Errorf("add key_rotation_strategy: %w", err)
			}
		}
	}

	// Channel Key 故障追踪字段
	if db.Migrator().HasTable("channel_keys") {
		if !db.Migrator().HasColumn("channel_keys", "priority") {
			if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN priority INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add priority: %w", err)
			}
		}
		if !db.Migrator().HasColumn("channel_keys", "error_count") {
			if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN error_count INTEGER NOT NULL DEFAULT 0").Error; err != nil {
				return fmt.Errorf("add error_count: %w", err)
			}
		}
		if !db.Migrator().HasColumn("channel_keys", "last_error_at") {
			if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN last_error_at DATETIME").Error; err != nil {
				return fmt.Errorf("add last_error_at: %w", err)
			}
		}
		if !db.Migrator().HasColumn("channel_keys", "disabled_until") {
			if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN disabled_until DATETIME").Error; err != nil {
				return fmt.Errorf("add disabled_until: %w", err)
			}
		}
	}

	return nil
}
