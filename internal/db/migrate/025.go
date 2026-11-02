package migrate

import (
	"fmt"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 25, Up: addChannelQuotaPause})
}

// Channel-wide quota pauses are runtime state, not an editable enable switch.
func addChannelQuotaPause(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") || db.Migrator().HasColumn("channels", "disabled_until") {
		return nil
	}
	return db.Exec("ALTER TABLE channels ADD COLUMN disabled_until DATETIME").Error
}
