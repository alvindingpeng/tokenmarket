package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 47, Up: backfillAPIKeyTenant})
}

// backfillAPIKeyTenant 给存量 API Key 回填归属租户。
//
// 背景: api_keys.tenant_id 列一直存在, 但创建路径从未写入, 存量行全部是 0。
// 中转侧按 api_key_tenant_id 判定租户配额, 0 表示"不属于任何租户", 于是
// 租户配额对全部 API Key 流量静默失效 —— 设了配额, 换个方式调用就能绕过。
//
// 回填口径: 取 Key 属主的 tenants 归属。属主自己没有有效租户时落到默认租户 1,
// 与 users.tenant_id 的默认值保持一致, 避免留下"无租户"的影子 Key。
func backfillAPIKeyTenant(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("api_keys") || !db.Migrator().HasTable("users") {
		return nil
	}
	if !db.Migrator().HasColumn("api_keys", "tenant_id") {
		return nil
	}

	// 一次 UPDATE ... JOIN 式回填: SQLite 用子查询表达, 避免逐行往返。
	// 只动 tenant_id = 0 的行, 重复执行是幂等的。
	err := db.Exec(`
		UPDATE api_keys
		SET tenant_id = COALESCE(
			(SELECT u.tenant_id FROM users u WHERE u.id = api_keys.user_id AND u.tenant_id > 0),
			1
		)
		WHERE tenant_id = 0
	`).Error
	if err != nil {
		return fmt.Errorf("backfill api key tenant_id: %w", err)
	}
	return nil
}
