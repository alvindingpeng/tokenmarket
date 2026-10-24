package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestBillingRecoveryRoutesRequireAdministrator(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "recovery-api.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// 测试库的 admin 是测试自建的(非默认 admin/admin): 翻转改密标记, 闸门由专门用例覆盖。
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// Register this group explicitly: RegisterAll consumes the global startup registry.
	group := engine.Group("/api/v1/ops/billing-recovery", middleware.Auth(), middleware.RequireRole(model.RoleAdmin))
	group.GET("/list", listBillingRecovery)
	group.POST("/run", runBillingRecovery)
	var adminToken, userToken, resellerToken string
	for _, spec := range []struct {
		name  string
		role  model.Role
		token *string
	}{
		{"recovery-admin", model.RoleAdmin, &adminToken}, {"recovery-user", model.RoleUser, &userToken}, {"recovery-reseller", model.RoleReseller, &resellerToken},
	} {
		user, err := op.UserCreate(spec.name, "", "password123", spec.role, model.StatusActive)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = op.UserDelete(user.ID) })
		token, _, err := auth.GenerateJWTToken(user, 3600)
		if err != nil {
			t.Fatal(err)
		}
		*spec.token = token
	}
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/ops/billing-recovery/list"},
		{http.MethodPost, "/api/v1/ops/billing-recovery/run"},
	} {
		for _, actor := range []struct {
			token string
			code  int
		}{{"", 401}, {userToken, 403}, {resellerToken, 403}, {adminToken, 200}} {
			req := httptest.NewRequest(endpoint.method, endpoint.path, nil)
			if actor.token != "" {
				req.AddCookie(&http.Cookie{Name: "auth", Value: actor.token})
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != actor.code {
				t.Fatalf("%s %s: got %d want %d: %s", endpoint.method, endpoint.path, w.Code, actor.code, w.Body.String())
			}
		}
	}
	var audits int64
	if err := db.GetDB().Model(&model.AuditLog{}).Where("action = ?", "billing.recovery.run").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("only authorized recovery run should be audited: got %d", audits)
	}
	callback := "test:reject_recovery_audit"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_logs" {
			tx.AddError(errors.New("audit unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Create().Remove(callback) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ops/billing-recovery/run", nil)
	req.AddCookie(&http.Cookie{Name: "auth", Value: adminToken})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "run was not started") {
		t.Fatalf("audit failure did not fail closed: %d %s", w.Code, w.Body.String())
	}
	for _, limit := range []string{"201", "0", "-1", "invalid"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ops/billing-recovery/list?limit="+limit, nil)
		req.AddCookie(&http.Cookie{Name: "auth", Value: adminToken})
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("invalid limit %s returned %d", limit, w.Code)
		}
	}
}
