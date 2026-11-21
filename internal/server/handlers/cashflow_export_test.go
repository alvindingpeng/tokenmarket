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

// TestWalletSummaryCarriesPoolCurrency 钱包摘要必须自报金额币种(与提现口径同一份账面 USD)。
//
// 修前: /api/v1/wallet/summary 只返回 balance/frozen/total_* 等裸数字, 没有 pool_currency ——
// 前端五个摘要卡片因此无从标注单位, 用户把 USD 余额读成人民币(差约 7 倍)。
// 断言走真实 HTTP 响应的 JSON(handler 层), 不是只检查 gin.H 字面量: 键名一旦改错,
// 前端读不到, 只有序列化后的响应才算契约。
func TestWalletSummaryCarriesPoolCurrency(t *testing.T) {
	userToken, _, _, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/wallet/summary", userToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("wallet summary: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode summary: %v %s", err, w.Body.String())
	}
	currency, ok := payload.Data["pool_currency"]
	if !ok {
		t.Fatalf("钱包摘要缺 pool_currency; 前端无法标注余额单位, 用户会把 USD 读成 CNY; body=%s", w.Body.String())
	}
	if currency != model.LedgerCurrency {
		t.Fatalf("pool_currency = %v, want %q (账面币种)", currency, model.LedgerCurrency)
	}
	// 金额语义零改动: 新用户余额为 0, 但键必须在且是数字(不是 null / 不存在的键)。
	if _, ok := payload.Data["balance"]; !ok {
		t.Fatalf("balance 键丢失: %s", w.Body.String())
	}
}

// TestWithdrawalExportCarriesPoolCurrency 提现导出必须把「收益池币种」单独成一列。
//
// 修前: 表头是 amount,fee,actual,currency,actual_cents,... —— 前三列是收益池 USD,
// 而 currency 列是**到账币种**(CNY/JPY/...)。财务把 amount 与 currency 拼起来就会把
// USD 当 CNY 入账。充值导出(amount_cents+currency+pay_cents+pay_currency)本来是对的, 提现这份漏了。
//
// 契约(加列不删列、不改列序, 免得打坏既有财务脚本): 新列 pool_currency 追加在末尾。
func TestWithdrawalExportCarriesPoolCurrency(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	op.FxRateCacheReset()
	t.Cleanup(op.FxRateCacheReset)
	order := model.Withdrawal{UserID: 1, Amount: 60, Fee: 0.6, Actual: 59.4, Rate: 7.2,
		ActualCents: 42768, Currency: "JPY", Method: "alipay",
		AccountName: "张三", AccountNo: "6222021234567890", Status: model.WithdrawStatusPending}
	if err := db.GetDB().Create(&order).Error; err != nil {
		t.Fatalf("seed withdrawal: %v", err)
	}
	w := cashflowStepUpAPI(t, engine, http.MethodGet, "/api/v1/admin/withdrawal/export", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("withdrawal export: %d %s", w.Code, w.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("export needs header + row, got %q", w.Body.String())
	}
	header := strings.Split(lines[0], ",")
	values := strings.Split(lines[1], ",")
	if len(header) != len(values) {
		t.Fatalf("header/row column count mismatch: %d vs %d (%q)", len(header), len(values), w.Body.String())
	}
	index := -1
	for i, name := range header {
		if name == "pool_currency" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("提现导出缺 pool_currency 列; 财务无法判断 amount 的单位; header=%v", header)
	}
	if values[index] != model.LedgerCurrency {
		t.Fatalf("pool_currency = %q, want %q", values[index], model.LedgerCurrency)
	}
	// 数值语义零改动: amount 列仍是订单金额, currency 列仍写充到账币种(旧列名不动)。
	if values[2] != "60.000000" {
		t.Fatalf("amount 列 = %q, want 60.000000 (数值语义不许改)", values[2])
	}
	var currencyCol string
	for i, name := range header {
		if name == "currency" {
			currencyCol = values[i]
		}
	}
	if currencyCol != "JPY" {
		t.Fatalf("currency 列 = %q, want JPY (旧列保留原语义: 到账币种)", currencyCol)
	}
}
