package handlers

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

func TestAdminFxRateStatusRoute(t *testing.T) {
	userToken, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	op.FxRateCacheReset()
	t.Cleanup(op.FxRateCacheReset)
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "CNY", Day: day, Rate: 7.2, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx rate: %v", err)
	}

	path := "/api/v1/admin/fx/rates/status"
	if w := ticketAPI(t, engine, http.MethodGet, path, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path, userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("user: %d %s", w.Code, w.Body.String())
	}
	w := ticketAPI(t, engine, http.MethodGet, path, adminToken, "")
	data := ticketAPIData(t, w)
	if data["day"] != day || data["age_days"].(float64) != 0 || data["stale"].(bool) {
		t.Fatalf("status: %+v", data)
	}
	if data["source"] != op.FxRateSourceName() || data["max_age_days"].(float64) != float64(op.FxRateMaxAgeDays) {
		t.Fatalf("metadata: %+v", data)
	}
	items := data["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["to"] != "CNY" {
		t.Fatalf("items: %+v", items)
	}
}

func TestAdminFxRateRefreshRouteRequiresAdmin(t *testing.T) {
	userToken, _, _, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	path := fmt.Sprintf("%s", "/api/v1/admin/fx/rates/refresh")
	if w := ticketAPI(t, engine, http.MethodPost, path, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, path, userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("user: %d %s", w.Code, w.Body.String())
	}
}

// TestAdminFxRateHistoryRoute 覆盖鉴权(匿名 401/非管理员 403)、管理员查询、日期过滤、
// 币种过滤(大小写归一, 排除非 USD 基准行)、区间上限、limit 边界与非法参数。
func TestAdminFxRateHistoryRoute(t *testing.T) {
	userToken, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	rows := []model.FxRate{
		{From: model.LedgerCurrency, To: "CNY", Day: "20260102", Rate: 7.2, Source: "test"},
		{From: model.LedgerCurrency, To: "CNY", Day: "20260103", Rate: 7.3, Source: "test"},
		{From: model.LedgerCurrency, To: "JPY", Day: "20260102", Rate: 150, Source: "test"},
		{From: "EUR", To: "CNY", Day: "20260102", Rate: 8, Source: "test"},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed fx rates: %v", err)
	}
	path := "/api/v1/admin/fx/rates/history"
	if w := ticketAPI(t, engine, http.MethodGet, path, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path, userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", w.Code, w.Body.String())
	}
	w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103", adminToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	data := ticketAPIData(t, w)
	if data["from"] != "20260102" || data["to"] != "20260103" {
		t.Fatalf("range: %+v", data)
	}
	// 缺省 limit 回显 100。
	if data["limit"].(float64) != 100 {
		t.Fatalf("default limit: %+v", data)
	}
	items := data["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items: %+v", items)
	}
	first, _ := items[0].(map[string]any)
	if first["day"] != "20260102" || first["to"] != "CNY" {
		t.Fatalf("items[0]: %+v", first)
	}
	if _, ok := first["source"]; !ok {
		t.Fatalf("items[0] missing source: %+v", first)
	}
	if _, ok := first["created_at"]; !ok {
		t.Fatalf("items[0] missing created_at: %+v", first)
	}
	// 按币种过滤: 小写输入归一成大写, 只返回 USD 基准的 CNY 行; USD 作为账面币种本身合法。
	w = ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&currency=cny", adminToken, "")
	data = ticketAPIData(t, w)
	items = data["items"].([]any)
	if len(items) != 2 || data["currency"] != "CNY" {
		t.Fatalf("filtered items: %+v", data)
	}
	w = ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&currency=USD", adminToken, "")
	data = ticketAPIData(t, w)
	if items = data["items"].([]any); len(items) != 0 || data["currency"] != "USD" {
		t.Fatalf("usd filter: %+v", data)
	}
	// 区间上限 366 天: 367 天(含首尾)按 400 拒绝。
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20250101&to=20260103", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("range over limit: %d %s", w.Code, w.Body.String())
	}
	// limit 边界: 0 与 501 拒绝, 500 放行。
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&limit=0", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("limit zero: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&limit=501", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("limit over: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&limit=500", adminToken, ""); w.Code != http.StatusOK {
		t.Fatalf("limit max: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260102&to=20260103&limit=abc", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("limit not a number: %d %s", w.Code, w.Body.String())
	}
	// 起止倒挂、非法日期与非法币种都按 400 拒绝。
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20260103&to=20260102", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("inverted range: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path+"?from=20261301", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, path+"?currency=XYZ", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad currency: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminFxRevaluationMonthEndDelta(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	month := "2026-10"
	day := "20261031"
	if err := db.GetDB().Create(&[]model.FxRate{
		{From: model.LedgerCurrency, To: "CNY", Day: "20261015", Rate: 1.0 / 7.0, Source: "test"},
		{From: model.LedgerCurrency, To: "CNY", Day: day, Rate: 1.0 / 8.0, Source: "test"},
	}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	created := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	rows := []model.BillingRecord{
		{RequestID: 9001, UserID: 1, Currency: "CNY", FxRate: 7.0, FxDay: "20261015", UserCost: 14, OwnerRevenue: 7, CreatedAt: created},
		{RequestID: 9002, UserID: 1, Currency: "CNY", FxRate: 7.0, FxDay: "20261015", UserCost: 7, OwnerRevenue: 3.5, CreatedAt: created},
		{RequestID: 9003, UserID: 1, Currency: "CNY", FxRate: 0, FxDay: "unavailable", UserCost: 100, OwnerRevenue: 50, CreatedAt: created},
		{RequestID: 9004, UserID: 1, Currency: "USD", FxRate: 1, FxDay: "", UserCost: 5, OwnerRevenue: 2, CreatedAt: created},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed billing: %v", err)
	}
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/fx/revaluation?month="+month, adminToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("revaluation: %d %s", w.Code, w.Body.String())
	}
	data := ticketAPIData(t, w)
	if data["month"] != month {
		t.Fatalf("month: %+v", data)
	}
	base := data["fx_day_summary"].(map[string]any)
	if base["count"].(float64) != 2 || base["user_cost"].(float64) != 21 {
		t.Fatalf("base: %+v", base)
	}
	revalued := data["revalued"].(map[string]any)
	if got := revalued["user_cost"].(float64); got <= 0 {
		t.Fatalf("revalued user cost=%v, want positive", got)
	}
	if got := revalued["owner_revenue"].(float64); got <= 0 {
		t.Fatalf("revalued owner revenue=%v, want positive", got)
	}
	delta := data["delta"].(map[string]any)
	if _, ok := delta["user_cost"].(float64); !ok {
		t.Fatalf("delta user cost missing: %+v", delta)
	}
	excluded := data["excluded"].(map[string]any)
	if excluded["count"].(float64) != 2 {
		t.Fatalf("excluded: %+v", excluded)
	}
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/fx/revaluation?month=2026-13", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid month: %d %s", w.Code, w.Body.String())
	}
	_ = gorm.ErrRecordNotFound
}
