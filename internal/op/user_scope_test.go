package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// userScopeTestSetup 隔离库 + 清空进程级用户缓存, 并建两个租户与各自的成员。
func userScopeTestSetup(t *testing.T) (model.Tenant, model.Tenant, []model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "user-scope.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()

	tenants := []model.Tenant{
		{Name: "scope-a", Code: "scope-a", Status: model.TenantStatusActive},
		{Name: "scope-b", Code: "scope-b", Status: model.TenantStatusActive},
	}
	if err := db.GetDB().Create(&tenants).Error; err != nil {
		t.Fatalf("create tenants: %v", err)
	}
	created := make([]model.User, 0, 4)
	for _, spec := range []struct {
		username string
		role     model.Role
		tenant   uint
	}{
		{"a-admin", model.RoleAdmin, tenants[0].ID},
		{"a-user", model.RoleUser, tenants[0].ID},
		{"b-admin", model.RoleAdmin, tenants[1].ID},
		{"b-user", model.RoleUser, tenants[1].ID},
	} {
		user, err := UserCreate(spec.username, "", "password123", spec.role, model.StatusActive, spec.tenant)
		if err != nil {
			t.Fatalf("create %s: %v", spec.username, err)
		}
		created = append(created, user)
	}
	return tenants[0], tenants[1], created
}

// TestUserListScopedHidesOtherTenants 租户管理员的账号列表必须只含本租户成员:
// 跨租户用户列表会把别租户的用户名与角色一并泄露, 是租户隔离上最直接的横向通道。
func TestUserListScopedHidesOtherTenants(t *testing.T) {
	tenantA, _, users := userScopeTestSetup(t)

	scope := model.Scope{ID: users[0].ID, Role: model.RoleAdmin, TenantID: tenantA.ID}
	views := UserListScoped(scope)

	if len(views) != 2 {
		t.Fatalf("tenant admin saw %d users, want 2 (own tenant only)", len(views))
	}
	for _, view := range views {
		if view.ID != users[0].ID && view.ID != users[1].ID {
			t.Fatalf("tenant admin saw foreign user %q (id=%d)", view.Username, view.ID)
		}
	}
}

// TestUserListScopedPlatformAdminSeesAll 平台级管理员(tenant 0)必须看得到全部租户的账号,
// 否则运维就无法在平台上做统一的账号盘点。
func TestUserListScopedPlatformAdminSeesAll(t *testing.T) {
	tenantA, _, users := userScopeTestSetup(t)
	_ = tenantA

	scope := model.Scope{ID: users[0].ID, Role: model.RoleAdmin, TenantID: 0}
	if got := len(UserListScoped(scope)); got != 4 {
		t.Fatalf("platform admin saw %d users, want 4", got)
	}
}

// TestUserListScopedTenantMemberSeesOnlySelf 普通成员不是租户管理员, 只应看到自己:
// 同租户的其它账号属于管理面信息, 不该因为同租户就一并开放。
func TestUserListScopedTenantMemberSeesOnlySelf(t *testing.T) {
	tenantA, _, users := userScopeTestSetup(t)

	scope := model.Scope{ID: users[1].ID, Role: model.RoleUser, TenantID: tenantA.ID}
	views := UserListScoped(scope)
	if len(views) != 1 || views[0].ID != users[1].ID {
		t.Fatalf("tenant member saw %d users, want only self", len(views))
	}
}
