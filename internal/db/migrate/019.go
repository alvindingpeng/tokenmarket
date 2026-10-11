package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 19,
		Up:      addChannelModelBreakerFields,
	})
}

// addChannelModelBreakerFields 为 channel_models 添加故障熔断字段, 支持渠道模型级熔断(区分于整个 Key 熔断)。
// 凭据失效/余额不足仍在 Key 级熔断(影响该 Key 所有模型); 过载/临时故障在渠道模型级熔断(隔离故障面)。
func addChannelModelBreakerFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasColumn("channel_models", "error_count") {
		if err := db.Exec("ALTER TABLE channel_models ADD COLUMN error_count INTEGER NOT NULL DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add error_count: %w", err)
		}
	}
	if !db.Migrator().HasColumn("channel_models", "last_error_at") {
		if err := db.Exec("ALTER TABLE channel_models ADD COLUMN last_error_at DATETIME").Error; err != nil {
			return fmt.Errorf("add last_error_at: %w", err)
		}
	}
	if !db.Migrator().HasColumn("channel_models", "disabled_until") {
		if err := db.Exec("ALTER TABLE channel_models ADD COLUMN disabled_until DATETIME").Error; err != nil {
			return fmt.Errorf("add disabled_until: %w", err)
		}
	}
	return nil
}
