package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/gin-gonic/gin"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// TestCashflowExportRoutes 资金导出路由: 鉴权(匿名 401/非管理员 403)、CSV 表头/行内容、
// status 过滤与公式注入清洗。落库两条不同状态的单据验证过滤真的生效。
func TestCashflowExportRoutes(t *testing.T) {
	userToken, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	op.FxRateCacheReset()
	t.Cleanup(op.FxRateCacheReset)
	day := "20260101"
	rows := []model.FxRate{{From: model.LedgerCurrency, To: "CNY", Day: day, Rate: 7.2, Source: "test"}}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	recharge := model.RechargeOrder{OrderNo: "=cmd|danger", UserID: 1, Provider: "epay", Method: "alipay",
		AmountCents: 1000, Currency: "CNY", PayCents: 1000, PayCurrency: "CNY",
		Rate: 7.2, RateDay: day, RateSource: "test", Credits: 100, Status: model.RechargeStatusPaid}
	withdraw := model.Withdrawal{UserID: 1, Amount: 60, Fee: 0.6, Actual: 59.4, Rate: 7.2,
		ActualCents: 427680000, Currency: "JPY", RateDay: day, RateSource: "test",
		Method: "alipay", AccountName: "张三", AccountNo: "6222021234567890", Status: model.WithdrawStatusPending}
	if err := db.GetDB().Create(&recharge).Error; err != nil {
		t.Fatalf("seed recharge: %v", err)
	}
	if err := db.GetDB().Create(&withdraw).Error; err != nil {
		t.Fatalf("seed withdraw: %v", err)
	}

	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/recharge/export", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous recharge export: %d", w.Code)
	}
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/recharge/export", userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("user recharge export: %d", w.Code)
	}
	w := cashflowStepUpAPI(t, engine, http.MethodGet, "/api/v1/admin/recharge/export?status=paid", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("admin recharge export: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "'=cmd|danger") {
		t.Fatalf("formula injection not sanitized: %s", body)
	}
	if !strings.Contains(body, "rate_day") || !strings.Contains(body, day) {
		t.Fatalf("snapshot columns missing: %s", body)
	}
	if w := cashflowStepUpAPI(t, engine, http.MethodGet, "/api/v1/admin/recharge/export?status=expired", adminToken); strings.Contains(w.Body.String(), "=cmd") {
		t.Fatalf("status filter not applied: %s", w.Body.String())
	}
	if w := cashflowStepUpAPI(t, engine, http.MethodGet, "/api/v1/admin/recharge/export?from=bad-date", adminToken); w.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d %s", w.Code, w.Body.String())
	}

	w = cashflowStepUpAPI(t, engine, http.MethodGet, "/api/v1/admin/withdrawal/export", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("admin withdrawal export: %d %s", w.Code, w.Body.String())
	}
	body = w.Body.String()
	if !strings.Contains(body, "actual_currency") || !strings.Contains(body, "JPY") {
		t.Fatalf("withdrawal payout columns missing: %s", body)
	}
	if strings.Contains(body, "6222021234567890") {
		t.Fatalf("account number must not appear in export: %s", body)
	}
}

// cashflowStepUpAPI 带二次验证发请求: 先用管理员口令换 step-up 凭证, 再挂 X-Step-Up-Token。
func cashflowStepUpAPI(t *testing.T, engine *gin.Engine, method, path, adminToken string) *httptest.ResponseRecorder {
	t.Helper()
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/user/step-up", adminToken, `{"password":"password123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("step-up: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.Data["token"].(string) == "" {
		t.Fatalf("step-up token: %v %s", err, w.Body.String())
	}
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: "auth", Value: adminToken})
	req.Header.Set(middleware.StepUpTokenHeader, payload.Data["token"].(string))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestCashflowExportLimit 标记: 上限常量与审计导出一致, 防止有人悄悄放宽。
func TestCashflowExportLimit(t *testing.T) {
	if cashflowExportLimit != 5000 {
		t.Fatalf("cashflowExportLimit = %d, want 5000", cashflowExportLimit)
	}
}

func TestBatchWithdrawalPartialFailures(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	order := model.Withdrawal{UserID: 1, Amount: 10, Actual: 10, Status: model.WithdrawStatusPending, Method: "alipay", AccountNo: "a", AccountName: "n"}
	if err := db.GetDB().Create(&order).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := `{"ids":[` + strconv.FormatUint(uint64(order.ID), 10) + `,999999],"approve":true,"note":"batch"}`
	w := cashflowStepUpAPIJSON(t, engine, "/api/v1/admin/withdrawal/batch-review", adminToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("batch review: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			Results []struct {
				ID    uint   `json:"id"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"results"`
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Succeeded != 1 || result.Data.Failed != 1 || len(result.Data.Results) != 2 || !result.Data.Results[0].OK || result.Data.Results[1].OK || result.Data.Results[1].Error == "" {
		t.Fatalf("partial result: %+v", result.Data)
	}
	// Paid batch similarly records row-level failure and continues after a missing ID.
	body = `{"ids":[` + strconv.FormatUint(uint64(order.ID), 10) + `,999998],"paid_ref":"bank-ref"}`
	w = cashflowStepUpAPIJSON(t, engine, "/api/v1/admin/withdrawal/batch-paid", adminToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("batch paid: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Succeeded != 0 || result.Data.Failed != 2 || result.Data.Results[0].Error == "" || result.Data.Results[1].Error == "" {
		t.Fatalf("paid result: %+v", result.Data)
	}
}

func cashflowStepUpAPIJSON(t *testing.T, engine *gin.Engine, path, adminToken, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/user/step-up", adminToken, `{"password":"password123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("step-up: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "auth", Value: adminToken})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.StepUpTokenHeader, payload.Data["token"].(string))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
