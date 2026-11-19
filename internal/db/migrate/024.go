package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 24,
		Up:      addUserTOTPFields,
	})
}

// addUserTOTPFields 为 users 补 TOTP 双因素两列: totp_enabled 与 totp_secret_encrypted。
//
// 为什么独立新版本: 迁移按版本号记账, 已执行过的在老库上直接跳过; 往旧版本追加列,
// 生产库永远不会跑到(列虽会被 AutoMigrate 建出, 但拿不到 NOT NULL DEFAULT 的语义)。
// 与 019-023 同规矩: 新列一律走新版本。
func addUserTOTPFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("users") {
		// 新装直接由 AutoMigrate 按模型建出全部列, 无需补。
		return nil
	}
	columns := []struct {
		name string
		ddl  string
	}{
		{"totp_version", "ALTER TABLE users ADD COLUMN totp_version BIGINT NOT NULL DEFAULT 0"},
		{"totp_last_step", "ALTER TABLE users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT -1"},
		{"totp_recovery_hashes", "ALTER TABLE users ADD COLUMN totp_recovery_hashes TEXT NOT NULL DEFAULT ''"},
		{"totp_attempts", "ALTER TABLE users ADD COLUMN totp_attempts INTEGER NOT NULL DEFAULT 0"},
		{"totp_attempt_window", "ALTER TABLE users ADD COLUMN totp_attempt_window BIGINT NOT NULL DEFAULT 0"},
		{"totp_enabled", "ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT 0"},
		{"totp_secret_encrypted", "ALTER TABLE users ADD COLUMN totp_secret_encrypted TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if db.Migrator().HasColumn("users", column.name) {
			continue
		}
		if err := db.Exec(column.ddl).Error; err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}
	return nil
}
