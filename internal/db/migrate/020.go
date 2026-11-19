package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 20,
		Up:      addModelCapabilities,
	})
}

// addModelCapabilities 建 model_capabilities 表(模型能力登记)。
// 表由 AutoMigrate 建出, 这里只补老库的列: 早期版本可能已存在同名旧表而缺列。
func addModelCapabilities(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("model_capabilities") {
		return nil
	}
	columns := []struct {
		name string
		ddl  string
	}{
		{"input", "ALTER TABLE model_capabilities ADD COLUMN input TEXT NOT NULL DEFAULT ''"},
		{"output", "ALTER TABLE model_capabilities ADD COLUMN output TEXT NOT NULL DEFAULT ''"},
		{"context", "ALTER TABLE model_capabilities ADD COLUMN context INTEGER NOT NULL DEFAULT 0"},
		{"max_output", "ALTER TABLE model_capabilities ADD COLUMN max_output INTEGER NOT NULL DEFAULT 0"},
		{"family", "ALTER TABLE model_capabilities ADD COLUMN family TEXT NOT NULL DEFAULT ''"},
		{"source", "ALTER TABLE model_capabilities ADD COLUMN source TEXT NOT NULL DEFAULT ''"},
		{"updated_at", "ALTER TABLE model_capabilities ADD COLUMN updated_at DATETIME"},
	}
	for _, column := range columns {
		if db.Migrator().HasColumn("model_capabilities", column.name) {
			continue
		}
		if err := db.Exec(column.ddl).Error; err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}
	return nil
}
