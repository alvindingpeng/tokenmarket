package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 23,
		Up:      addXunfeiSparkCredentialFields,
	})
}

// addXunfeiSparkCredentialFields 为讯飞星火凭据补 channel_keys 的 app_id / api_secret / domain 三列。
//
// 为什么是独立的新版本而不是塞进旧迁移: 迁移按版本号记账, 已执行过的版本在老库上直接跳过,
// 往旧版本的 Up 里追加列, 生产库永远不会跑到(列虽会被 AutoMigrate 建出, 但拿不到 NOT NULL DEFAULT ”
// 的语义, "这段 DDL 什么时候生效"也变得不可追溯)。新列一律走新版本, 与 019-022 同规矩。
func addXunfeiSparkCredentialFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_keys") {
		// 老库还没有 channel_keys: 新装直接走新表结构, 无需补列。
		return nil
	}
	for _, column := range []string{"app_id", "api_secret", "domain"} {
		if db.Migrator().HasColumn("channel_keys", column) {
			continue
		}
		if err := db.Exec("ALTER TABLE channel_keys ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add %s: %w", column, err)
		}
	}
	return nil
}
