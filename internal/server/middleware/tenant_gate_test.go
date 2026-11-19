package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/gin-gonic/gin"
)

// tenantGateSetup 建一个带租户与用户的测试库, 返回路由与签发令牌的工具。
func tenantGateSetup(t *testing.T) (*gin.Engine, func(user model.User) string) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "tenant.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatalf("user init: %v", err)
	}
	// 默认管理员改密闸门会盖掉租户判定, 先把它翻成已改密。
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "1"); err != nil {
		t.Fatalf("setting: %v", err)
	}

	router := gin.New()
	router.Use(Auth())
	router.GET("/probe", func(c *gin.Context) {
		// 回显中间件注入的作用域, 测试据此断言覆盖是否生效。
		c.JSON(http.StatusOK, gin.H{
			"tenant_id":       c.GetInt("tenant_id"),
			"tenant_override": c.GetBool("tenant_override"),
		})
	})

	tokenFor := func(user model.User) string {
		t.Helper()
		token, _, err := auth.GenerateJWTToken(user, 0)
		if err != nil {
			t.Fatalf("generate token: %v", err)
		}
		return token
	}
	return router, tokenFor
}

// setUserTenant 迁租并把缓存副本一并刷新 —— 认证中间件读的是缓存,
// 只改库不改缓存会让测试断言落到旧租户上(生产里的迁租接口同样必须走这条路径)。
func setUserTenant(user model.User, tenantID uint) error {
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", user.ID).Update("tenant_id", tenantID).Error; err != nil {
		return err
	}
	// UserTouchBalance 从库重建缓存副本; 直接 UserGetByID 只读缓存, 刷不出新值。
	op.UserTouchBalance(user.ID)
	return nil
}

// performWithTenant 用给定令牌与可选覆盖头发一次探测请求。
func performWithTenant(router *gin.Engine, token, overrideTenant string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	if overrideTenant != "" {
		request.Header.Set(tenantOverrideHeader, overrideTenant)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthRejectsUsersInDisabledTenant(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	tenant, err := op.TenantCreate("Disabled Co", "disabled-co", "disabled")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user, err := op.UserCreate("disabled-user", "", "password123", model.RoleUser, model.StatusActive, 1)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	user.TenantID = tenant.ID
	if err := setUserTenant(user, tenant.ID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}

	recorder := performWithTenant(router, tokenFor(user), "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("disabled tenant user: got %d, want 403", recorder.Code)
	}
}

func TestAuthRejectsUsersInPendingTenant(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	tenant, err := op.TenantCreate("Pending Co", "pending-co", "pending")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user, err := op.UserCreate("pending-user", "", "password123", model.RoleUser, model.StatusActive, 1)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	user.TenantID = tenant.ID
	if err := setUserTenant(user, tenant.ID); err != nil {
		t.Fatalf("set tenant: %v", err)
	}

	recorder := performWithTenant(router, tokenFor(user), "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("pending tenant user: got %d, want 403", recorder.Code)
	}
}

func TestAuthAllowsActiveTenantUser(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	user, err := op.UserCreate("active-user", "", "password123", model.RoleUser, model.StatusActive, 1)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	recorder := performWithTenant(router, tokenFor(user), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("active tenant user: got %d, want 200", recorder.Code)
	}
}

func TestAdminTenantOverrideSwitchesScope(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	target, err := op.TenantCreate("Target Co", "target-co", "active")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	admin, err := op.UserVerify("admin", "admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}

	recorder := performWithTenant(router, tokenFor(admin), strconv.Itoa(int(target.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin override: got %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "\"tenant_id\":"+strconv.Itoa(int(target.ID))) {
		t.Fatalf("admin override tenant_id not applied: %s", body)
	}
	if !strings.Contains(body, "\"tenant_override\":true") {
		t.Fatalf("override flag missing: %s", body)
	}
}

func TestNonAdminTenantOverrideIgnored(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	target, err := op.TenantCreate("Target Co 2", "target-co-2", "active")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user, err := op.UserCreate("plain-user", "", "password123", model.RoleUser, model.StatusActive, 1)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 普通用户带覆盖头必须无效: 否则任何用户都能把自己的数据作用域改到别人租户。
	recorder := performWithTenant(router, tokenFor(user), strconv.Itoa(int(target.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plain user: got %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "\"tenant_override\":true") {
		t.Fatalf("non-admin override must be ignored: %s", body)
	}
	if !strings.Contains(body, "\"tenant_id\":"+strconv.Itoa(int(user.TenantID))) {
		t.Fatalf("plain user should keep own tenant: %s", body)
	}
}

func TestAdminOverrideToDisabledTenantRejected(t *testing.T) {
	router, tokenFor := tenantGateSetup(t)

	disabled, err := op.TenantCreate("Disabled Target", "disabled-target", "disabled")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	admin, err := op.UserVerify("admin", "admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}

	// 切到被禁用租户必须拒绝, 否则管理员能绕过停服栅栏查看该租户数据。
	recorder := performWithTenant(router, tokenFor(admin), strconv.Itoa(int(disabled.ID)))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("override to disabled tenant: got %d, want 403", recorder.Code)
	}
}

func TestTenantStatusCacheInvalidatedOnUpdate(t *testing.T) {
	_, _ = tenantGateSetup(t)

	tenant, err := op.TenantCreate("Cache Co", "cache-co", "active")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if usable, _ := op.TenantUsable(tenant.ID); !usable {
		t.Fatal("freshly created active tenant should be usable")
	}
	// 改成 disabled 后必须立即不可用: 缓存要在这里失效, 不能等 TTL。
	if _, err := op.TenantUpdate(int(tenant.ID), "", "", "disabled"); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if usable, _ := op.TenantUsable(tenant.ID); usable {
		t.Fatal("disabled tenant still reported usable after update")
	}
	// 改回 active 同样要立即生效。
	if _, err := op.TenantUpdate(int(tenant.ID), "", "", "active"); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if usable, _ := op.TenantUsable(tenant.ID); !usable {
		t.Fatal("re-activated tenant still reported unusable")
	}

	op.TenantStatusInvalidateAll()
}
