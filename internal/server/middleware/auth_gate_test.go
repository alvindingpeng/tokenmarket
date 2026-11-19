package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/gin-gonic/gin"
)

func gateTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "gate.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

func TestAuthBlocksAdminUntilDefaultPasswordChanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gateTestDB(t)

	// 空库初始化出默认 admin; 未改密前管理面请求应被 403 拦下。
	if err := op.UserInit(); err != nil {
		t.Fatalf("user init: %v", err)
	}
	if op.AdminDefaultPasswordChanged() {
		t.Fatal("fresh database must report default password unchanged")
	}
	admin, err := op.UserVerify("admin", "admin")
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}
	token, _, err := auth.GenerateJWTToken(admin, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	router := gin.New()
	router.Use(Auth())
	router.GET("/api/v1/setting/list", func(c *gin.Context) { c.Status(http.StatusOK) })

	perform := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "auth", Value: token})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	if got := perform("/api/v1/setting/list").Code; got != http.StatusForbidden {
		t.Fatalf("admin endpoint before password change: got %d, want 403", got)
	}
	if got := perform("/api/v1/user/change-password").Code; got == http.StatusForbidden {
		t.Fatal("change-password must stay reachable before the change")
	}
	if got := perform("/api/v1/user/status").Code; got == http.StatusForbidden {
		t.Fatal("status must stay reachable before the change")
	}

	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatalf("flip flag: %v", err)
	}
	if got := perform("/api/v1/setting/list").Code; got != http.StatusOK {
		t.Fatalf("admin endpoint after password change: got %d, want 200", got)
	}
}
