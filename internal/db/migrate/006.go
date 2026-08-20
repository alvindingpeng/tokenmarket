package migrate

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 6,
		Up:      migrateDropLegacyGroupColumns,
	})
}

// migrateDropLegacyGroupColumns 移除旧版分组模式字段，这些字段已不再使用。
// 旧版 groups 表包含 mode、match_regex、first_token_time_out、session_keep_time，
// 其中 mode 为 NOT NULL 且无默认值，新版模型不再写入该列，导致创建分组时触发 NOT NULL 约束错误。
// group_items 表同样遗留了 weight 字段。
func migrateDropLegacyGroupColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	if db.Migrator().HasTable("groups") {
		legacyGroupColumns := []string{"mode", "match_regex", "first_token_time_out", "session_keep_time"}
		for _, column := range legacyGroupColumns {
			if err := dropColumnIfExists(db, &model.Group{}, "groups", column); err != nil {
				return err
			}
		}
	}

	if db.Migrator().HasTable("group_items") {
		if err := dropColumnIfExists(db, &model.GroupItem{}, "group_items", "weight"); err != nil {
			return err
		}
	}

	return nil
}
