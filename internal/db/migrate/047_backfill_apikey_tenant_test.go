package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// migrateAPIKeyTenantDB 建一个最小库, 含 users 与 api_keys 两张表。
func migrateAPIKeyTenantDB(t *testing.T) *gorm.DB {
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
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT, tenant_id INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE api_keys (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL DEFAULT 0, tenant_id INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, stmt := range stmts {
		if err := conn.Exec(stmt).Error; err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return conn
}

func TestBackfillAPIKeyTenantAssignsOwnerTenant(t *testing.T) {
	conn := migrateAPIKeyTenantDB(t)

	if err := conn.Exec(`INSERT INTO users (id, username, tenant_id) VALUES (1, 'alice', 7), (2, 'bob', 3), (3, 'carol', 0)`).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := conn.Exec(`INSERT INTO api_keys (id, user_id, tenant_id) VALUES (1, 1, 0), (2, 2, 0), (3, 3, 0)`).Error; err != nil {
		t.Fatalf("seed keys: %v", err)
	}

	if err := backfillAPIKeyTenant(conn); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	cases := []struct {
		keyID int
		want  int
	}{
		{1, 7}, // 属主租户 7
		{2, 3}, // 属主租户 3
		{3, 1}, // 属主无租户 -> 默认租户 1
	}
	for _, tc := range cases {
		var got int
		if err := conn.Table("api_keys").Select("tenant_id").Where("id = ?", tc.keyID).Scan(&got).Error; err != nil {
			t.Fatalf("read key %d: %v", tc.keyID, err)
		}
		if got != tc.want {
			t.Fatalf("key %d tenant_id = %d, want %d", tc.keyID, got, tc.want)
		}
	}
}

func TestBackfillAPIKeyTenantIsIdempotent(t *testing.T) {
	conn := migrateAPIKeyTenantDB(t)

	if err := conn.Exec(`INSERT INTO users (id, username, tenant_id) VALUES (1, 'alice', 7)`).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := conn.Exec(`INSERT INTO api_keys (id, user_id, tenant_id) VALUES (1, 1, 0)`).Error; err != nil {
		t.Fatalf("seed keys: %v", err)
	}
	if err := backfillAPIKeyTenant(conn); err != nil {
		t.Fatalf("first backfill: %v", err)
	}

	// 已回填的行不能被第二次执行覆盖: 属主后来换了租户也不该改写历史 Key 的归属。
	if err := conn.Exec(`UPDATE users SET tenant_id = 99 WHERE id = 1`).Error; err != nil {
		t.Fatalf("move user: %v", err)
	}
	if err := backfillAPIKeyTenant(conn); err != nil {
		t.Fatalf("second backfill: %v", err)
	}

	var got int
	if err := conn.Table("api_keys").Select("tenant_id").Where("id = 1").Scan(&got).Error; err != nil {
		t.Fatalf("read key: %v", err)
	}
	if got != 7 {
		t.Fatalf("key tenant_id = %d, want 7 (unchanged by second run)", got)
	}
}
