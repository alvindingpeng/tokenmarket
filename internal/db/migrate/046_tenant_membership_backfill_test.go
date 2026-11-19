package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// tenantBackfillDB 建一个含 users / tenants / user_tenant_mappings 的最小库。
func tenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := conn.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	stmts := []string{
		`CREATE TABLE tenants (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, code TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'active')`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT, role TEXT NOT NULL DEFAULT 'user', tenant_id INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE user_tenant_mappings (user_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL, role TEXT NOT NULL DEFAULT 'member', created_at DATETIME, PRIMARY KEY (user_id, tenant_id))`,
	}
	for _, stmt := range stmts {
		if err := conn.Exec(stmt).Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	if err := conn.Exec(`INSERT INTO tenants (id, name, code, status) VALUES (1, 'Default', 'default', 'active'), (4, 'Four', 'four', 'active')`).Error; err != nil {
		t.Fatalf("seed tenants: %v", err)
	}
	return conn
}

// mappingRoleFor 读出某用户在某租户下的成员角色; 不存在返回空串。
func mappingRoleFor(t *testing.T, conn *gorm.DB, userID, tenantID int) string {
	t.Helper()
	var role string
	if err := conn.Table("user_tenant_mappings").Select("role").
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).Scan(&role).Error; err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	return role
}

func countMappings(t *testing.T, conn *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := conn.Table("user_tenant_mappings").Count(&count).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	return count
}

// TestTenantMembershipBackfillMapsRoles 存量用户必须按角色回填成员关系:
// 没有这一步, user_tenant_mappings 永远是空表, 租户成员列表与成员管理都是摆设。
func TestTenantMembershipBackfillMapsRoles(t *testing.T) {
	conn := tenantBackfillDB(t)
	if err := conn.Exec(`INSERT INTO users (id, username, role, tenant_id) VALUES
		(1, 'root', 'admin', 1),
		(2, 'reseller', 'reseller', 4),
		(3, 'plain', 'user', 4),
		(4, 'orphan', 'user', 0),
		(5, 'ghost', 'user', 99)`).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}

	if err := Migration046_TenantMembershipBackfill(conn); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	cases := []struct {
		userID   int
		tenantID int
		wantRole string
	}{
		{1, 1, "owner"},  // admin -> owner
		{2, 4, "admin"},  // reseller -> admin
		{3, 4, "member"}, // user -> member
		{4, 1, "member"}, // tenant_id 0 -> 默认租户
		{5, 1, "member"}, // 指向不存在的租户 -> 默认租户
	}
	for _, tc := range cases {
		if got := mappingRoleFor(t, conn, tc.userID, tc.tenantID); got != tc.wantRole {
			t.Fatalf("user %d mapping role=%q, want %q", tc.userID, got, tc.wantRole)
		}
	}
	// 每个用户恰好一条关系, 不能因为没有租户就漏掉。
	if got := countMappings(t, conn); got != int64(len(cases)) {
		t.Fatalf("mappings=%d, want %d", got, len(cases))
	}
}

// TestTenantMembershipBackfillIsIdempotent 迁移会被重跑(失败重试/多副本),
// 已存在的关系不能被覆盖, 也不能重复插入。
func TestTenantMembershipBackfillIsIdempotent(t *testing.T) {
	conn := tenantBackfillDB(t)
	if err := conn.Exec(`INSERT INTO users (id, username, role, tenant_id) VALUES (1, 'root', 'admin', 1)`).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := Migration046_TenantMembershipBackfill(conn); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	// 运营手工把它降级成 member: 重跑迁移不该把它改回 owner。
	if err := conn.Exec(`UPDATE user_tenant_mappings SET role = 'member' WHERE user_id = 1`).Error; err != nil {
		t.Fatalf("edit mapping: %v", err)
	}
	if err := Migration046_TenantMembershipBackfill(conn); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if got := mappingRoleFor(t, conn, 1, 1); got != "member" {
		t.Fatalf("second backfill overwrote role: got %q, want member", got)
	}
	if got := countMappings(t, conn); got != 1 {
		t.Fatalf("mappings=%d after rerun, want 1", got)
	}
}
