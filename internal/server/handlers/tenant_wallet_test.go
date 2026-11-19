package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

// tenantWalletEngine 构造只挂充值/调账路由的引擎(跳过鉴权中间件, 专注业务校验)。
func tenantWalletEngine(t *testing.T) (*gin.Engine, model.Tenant) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "wallet-handler.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tenant := model.Tenant{Name: "handler-tenant", Code: "handler-tenant", Status: model.TenantStatusActive}
	if err := db.GetDB().Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if _, err := op.TenantWalletEnsure(tenant.ID); err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}

	engine := gin.New()
	engine.POST("/api/v1/admin/tenant/:id/wallet/recharge", rechargeTenantWallet)
	engine.POST("/api/v1/admin/tenant/:id/wallet/adjust", adjustTenantWallet)
	engine.GET("/api/v1/admin/tenant/:id/wallet/ledger", listTenantLedger)
	return engine, tenant
}

// doJSON 发送一次 JSON 请求并返回状态码与解析后的响应体。
func doJSON(t *testing.T, engine *gin.Engine, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var parsed map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	return w.Code, parsed
}

// TestRechargeTenantWalletRejectsBadAmount 非法金额必须 400 且不改动余额:
// 负充值会把客户余额打成负值, 之后所有预扣都会失败, 只能人工修库。
func TestRechargeTenantWalletRejectsBadAmount(t *testing.T) {
	engine, tenant := tenantWalletEngine(t)
	path := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/recharge"

	for _, body := range []string{`{"amount":0}`, `{"amount":-10}`} {
		if code, _ := doJSON(t, engine, http.MethodPost, path, body); code != http.StatusBadRequest {
			t.Fatalf("body=%s code=%d, want 400", body, code)
		}
	}
	wallet, err := op.TenantWalletEnsure(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 0 {
		t.Fatalf("balance=%v after rejected recharges, want 0", wallet.Balance)
	}
}

// TestRechargeTenantWalletHappyPath 合法充值必须入账并写流水。
func TestRechargeTenantWalletHappyPath(t *testing.T) {
	engine, tenant := tenantWalletEngine(t)
	path := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/recharge"

	code, _ := doJSON(t, engine, http.MethodPost, path, `{"amount":100,"note":"topup"}`)
	if code != http.StatusOK {
		t.Fatalf("code=%d, want 200", code)
	}
	wallet, err := op.TenantWalletEnsure(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 100 || wallet.TotalRecharged != 100 {
		t.Fatalf("wallet=%+v, want balance=100 total_recharged=100", wallet)
	}
}

// TestAdjustTenantWalletRejectsOverdraw 调账不得把余额打成负数。
func TestAdjustTenantWalletRejectsOverdraw(t *testing.T) {
	engine, tenant := tenantWalletEngine(t)
	rechargePath := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/recharge"
	adjustPath := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/adjust"

	if code, _ := doJSON(t, engine, http.MethodPost, rechargePath, `{"amount":10}`); code != http.StatusOK {
		t.Fatalf("recharge code=%d, want 200", code)
	}
	if code, _ := doJSON(t, engine, http.MethodPost, adjustPath, `{"amount":-20}`); code != http.StatusBadRequest {
		t.Fatalf("overdraw code=%d, want 400", code)
	}
	wallet, err := op.TenantWalletEnsure(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 10 {
		t.Fatalf("balance=%v, want 10 (unchanged)", wallet.Balance)
	}
}

// TestTenantLedgerEndpointListsEntries 流水端点必须返回充值记录。
func TestTenantLedgerEndpointListsEntries(t *testing.T) {
	engine, tenant := tenantWalletEngine(t)
	rechargePath := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/recharge"
	ledgerPath := "/api/v1/admin/tenant/" + strconv.Itoa(int(tenant.ID)) + "/wallet/ledger"

	if code, _ := doJSON(t, engine, http.MethodPost, rechargePath, `{"amount":50}`); code != http.StatusOK {
		t.Fatalf("recharge code=%d, want 200", code)
	}
	code, body := doJSON(t, engine, http.MethodGet, ledgerPath, "")
	if code != http.StatusOK {
		t.Fatalf("ledger code=%d, want 200", code)
	}
	// 响应统一包在 data 里: 直接读 body["total"] 永远是 nil。
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data=%v, want object", body["data"])
	}
	if total, ok := data["total"].(float64); !ok || total != 1 {
		t.Fatalf("total=%v, want 1", data["total"])
	}
}
