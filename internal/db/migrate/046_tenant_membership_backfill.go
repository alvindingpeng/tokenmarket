package migrate

import (
	"fmt"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 46, Up: Migration046_TenantMembershipBackfill})
}

// Migration046_TenantMembershipBackfill 为全部存量用户回填一条租户成员关系。
//
// 背景: user_tenant_mappings 这张表存在但从未被任何代码写入 —— GetUserTenants 因此恒为空,
// 租户成员关系在实际数据里并不存在。迁移 045 只给 users 加了 tenant_id 列(默认 1),
// 存量账号在库里各自有归属值, 但没有一处成员关系能让管理端看到"这个租户里有哪些人"。
//
// 角色映射与 op.TenantRoleForUser 完全一致: admin→owner, reseller→admin, 其余→member。
// 用 SQL CASE 而不是逐行读模型, 是为了让本迁移只看两列(id/tenant_id/role), 不依赖
// 当前 User 结构体的其它字段形状 —— 迁移必须能在任意历史形状的 users 表上跑。
//
// 三条硬约束:
//   - 幂等: WHERE NOT EXISTS 让第二次执行命中 0 行, 已存在的成员关系(含后续人工改成
//     其它租户的行)一律不覆盖 —— 回填只补空缺, 不改写运行期已产生的真实关系。
//   - 不建新租户: tenant_id 为 0 或指向已删除租户的账号统一落到默认租户 1。
//     指向不存在的租户会让认证按"租户不可用"把账号永久锁死在 403, 回填不能制造这种账号。
//   - 跨方言: 用 INSERT ... SELECT + NOT EXISTS(GORM Exec), 不写 SQLite 专属语法。
func Migration046_TenantMembershipBackfill(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("users") || !db.Migrator().HasTable("user_tenant_mappings") {
		return nil
	}
	if !db.Migrator().HasTable("tenants") {
		// 没有租户表就没有可信的目标租户, 更不能凭空造出 id=1 之外的归属。
		return nil
	}
	if !db.Migrator().HasColumn("users", "tenant_id") {
		// 045 未跑或本库仍是最早形状: 没有归属列就没有可回填的输入, 跳过而不是报错。
		return nil
	}

	// 目标租户表达式: 账号 tenant_id 存在且对应租户仍在, 就用它; 否则回落到默认租户 1。
	// 用子查询而不是 LEFT JOIN 是为了与 INSERT...SELECT 的列顺序一一对应, 便于阅读与复核。
	tenantExpr := "CASE WHEN u.tenant_id IS NULL OR u.tenant_id = 0 OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = u.tenant_id) THEN 1 ELSE u.tenant_id END"

	result := db.Exec(`
INSERT INTO user_tenant_mappings (user_id, tenant_id, role, created_at)
SELECT u.id,
       ` + tenantExpr + `,
       CASE u.role
           WHEN 'admin' THEN 'owner'
           WHEN 'reseller' THEN 'admin'
           ELSE 'member'
       END,
       CURRENT_TIMESTAMP
FROM users u
WHERE NOT EXISTS (
    SELECT 1 FROM user_tenant_mappings m WHERE m.user_id = u.id
)`)
	if result.Error != nil {
		return fmt.Errorf("backfill tenant memberships: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Infof("migrate 46: backfilled %d tenant memberships", result.RowsAffected)
	}
	return nil
}
