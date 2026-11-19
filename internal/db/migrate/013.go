package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 13,
		Up:      dropOwnerScopedUniqueIndexes,
	})
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      backfillMultiUserOwnership,
	})
}

// dropOwnerScopedUniqueIndexes 拆掉渠道名与分组名的全局唯一索引。
// 多用户后两者按归属唯一((user_id, name) 复合唯一由模型声明, AutoMigrate 会建),
// 全局唯一索引不拆则跨用户重名仍会被拒。索引名由 GORM 按列名生成。
func dropOwnerScopedUniqueIndexes(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	for _, index := range []struct{ table, name string }{
		{"channels", "idx_channels_name"},
		{"groups", "idx_groups_name"},
	} {
		if !db.Migrator().HasTable(index.table) {
			continue
		}
		switch db.Dialector.Name() {
		case "mysql":
			// MySQL 的 DROP INDEX 没有 IF EXISTS, 不存在时报错即可忽略。
			_ = db.Exec(fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", index.table, index.name)).Error
		default:
			if err := db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %s", index.name)).Error; err != nil {
				return fmt.Errorf("failed to drop index %s: %w", index.name, err)
			}
		}
	}
	return nil
}

// backfillMultiUserOwnership 把单用户时代的存量数据归属到管理员:
// 老库唯一用户升级为 admin, api_keys / channels / groups 的 user_id 回填为该用户。
// 回填只针对 user_id=0 的行, 重跑安全。
func backfillMultiUserOwnership(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("users") {
		return nil
	}

	// 存量用户全部升级为 admin: 单用户时代只有管理员一个账号。
	if err := db.Exec("UPDATE users SET role = 'admin' WHERE role = '' OR role = 'user'").Error; err != nil {
		return fmt.Errorf("failed to upgrade legacy users to admin: %w", err)
	}

	var adminID uint
	if err := db.Raw("SELECT id FROM users ORDER BY id LIMIT 1").Scan(&adminID).Error; err != nil {
		return fmt.Errorf("failed to locate admin user: %w", err)
	}
	if adminID == 0 {
		return nil
	}

	for _, table := range []string{"api_keys", "channels", "groups"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Exec(fmt.Sprintf("UPDATE %s SET user_id = ? WHERE user_id = 0", table), adminID).Error; err != nil {
			return fmt.Errorf("failed to backfill %s ownership: %w", table, err)
		}
	}
	return nil
}
