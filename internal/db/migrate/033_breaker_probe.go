package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 33,
		Up:      Migration033_BreakerProbe,
	})
}

// Migration033_BreakerProbe 增加熔断探测字段（自动恢复验证）
func Migration033_BreakerProbe(db *gorm.DB) error {
	// channel_keys: 增加探测相关字段
	if !db.Migrator().HasColumn("channel_keys", "probe_count") {
		if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN probe_count INT DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add probe_count to channel_keys: %w", err)
		}
	}
	if !db.Migrator().HasColumn("channel_keys", "probe_at") {
		if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN probe_at DATETIME").Error; err != nil {
			return fmt.Errorf("add probe_at to channel_keys: %w", err)
		}
	}

	// channel_models: 增加探测相关字段
	if !db.Migrator().HasColumn("channel_models", "probe_count") {
		if err := db.Exec("ALTER TABLE channel_models ADD COLUMN probe_count INT DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add probe_count to channel_models: %w", err)
		}
	}
	if !db.Migrator().HasColumn("channel_models", "probe_at") {
		if err := db.Exec("ALTER TABLE channel_models ADD COLUMN probe_at DATETIME").Error; err != nil {
			return fmt.Errorf("add probe_at to channel_models: %w", err)
		}
	}

	return nil
}
