package handlers

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
	"github.com/gin-gonic/gin"
)

// isolationSetup 建隔离库 + 两个租户各一个管理员, 并返回构造请求的辅助函数。
func isolationSetup(t *testing.T) (model.User, model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "isolation.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatalf("user init: %v", err)
	}

	tenants := []model.Tenant{
		{Name: "iso-a", Code: "iso-a", Status: model.TenantStatusActive},
		{Name: "iso-b", Code: "iso-b", Status: model.TenantStatusActive},
	}
	if err := db.GetDB().Create(&tenants).Error; err != nil {
		t.Fatalf("create tenants: %v", err)
	}

	adminA, err := op.UserCreate("admin-a", "", "password123", model.RoleAdmin, model.StatusActive, tenants[0].ID)
	if err != nil {
		t.Fatalf("create admin a: %v", err)
	}
	adminB, err := op.UserCreate("admin-b", "", "password123", model.RoleAdmin, model.StatusActive, tenants[1].ID)
	if err != nil {
		t.Fatalf("create admin b: %v", err)
	}
	// 租户 B 还有一个普通成员, 作为「别人租户里的目标账号」。
	if _, err := op.UserCreate("victim", "", "password123", model.RoleUser, model.StatusActive, tenants[1].ID); err != nil {
		t.Fatalf("create victim: %v", err)
	}
	return adminA, adminB
}

// scopedEngine 把目标 handler 挂在带作用域注入的路由下。
func scopedEngine(caller model.User, method string, path string, handler gin.HandlerFunc) *gin.Engine {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("user_id", int(caller.ID))
		c.Set("role", string(caller.Role))
		c.Set("username", caller.Username)
		c.Set("tenant_id", int(caller.TenantID))
	})
	engine.Handle(method, path, handler)
	return engine
}

// TestDeleteUserRejectsOtherTenant 跨租户删号必须被拦住:
// 删号会把对方租户的账号连同其数据一并抹掉, 是隔离上最危险的横向写操作。
func TestDeleteUserRejectsOtherTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminA, _ := isolationSetup(t)

	var victim model.User
	if err := db.GetDB().Where("username = ?", "victim").First(&victim).Error; err != nil {
		t.Fatalf("load victim: %v", err)
	}

	engine := scopedEngine(adminA, http.MethodDelete, "/delete/:id", deleteUser)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/delete/"+strconv.Itoa(int(victim.ID)), nil)
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: got %d, want 404", recorder.Code)
	}
	// 账号必须仍然存在: 404 不能是"删了但报找不到"。
	if _, err := op.UserGetByID(victim.ID); err != nil {
		t.Fatalf("victim was deleted despite 404: %v", err)
	}
}

// TestUpdateUserRejectsOtherTenant 跨租户改角色/改余额同样必须拒绝。
func TestUpdateUserRejectsOtherTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminA, _ := isolationSetup(t)

	var victim model.User
	if err := db.GetDB().Where("username = ?", "victim").First(&victim).Error; err != nil {
		t.Fatalf("load victim: %v", err)
	}

	engine := scopedEngine(adminA, http.MethodPost, "/update", updateUser)
	body := `{"id":` + strconv.Itoa(int(victim.ID)) + `,"role":"admin"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant update: got %d, want 404", recorder.Code)
	}
	// 角色不能被提升: 否则租户 A 的管理员可以给租户 B 的账号提权。
	fresh, err := op.UserGetByID(victim.ID)
	if err != nil {
		t.Fatalf("reload victim: %v", err)
	}
	if fresh.Role != model.RoleUser {
		t.Fatalf("victim role changed to %q despite rejection", fresh.Role)
	}
}

// TestUpdateUserAllowsOwnTenant 同租户内必须仍然可以正常管理, 否则隔离会误伤正常运维。
func TestUpdateUserAllowsOwnTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminA, _ := isolationSetup(t)

	var own model.User
	if err := db.GetDB().Where("username = ?", "admin-a").First(&own).Error; err != nil {
		t.Fatalf("load own: %v", err)
	}

	engine := scopedEngine(adminA, http.MethodPost, "/update", updateUser)
	body := `{"id":` + strconv.Itoa(int(own.ID)) + `,"status":"disabled"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("same-tenant update: got %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	if _, err := op.UserGetByID(own.ID); err != nil {
		t.Fatalf("reload own: %v", err)
	}
}
