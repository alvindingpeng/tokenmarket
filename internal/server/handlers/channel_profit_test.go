package handlers

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelProfitRoute 覆盖鉴权(匿名 401/非管理员 403)、渠道隔离、空结果、404 与 400 的完整链路。
func TestChannelProfitRoute(t *testing.T) {
	userToken, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "profit-api"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	other := model.Channel{ChannelConfig: model.ChannelConfig{Name: "profit-api-other"}}
	if err := db.GetDB().Create(&other).Error; err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	day1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.Local)
	rows := []model.BillingRecord{
		{ChannelID: channel.ID, Status: model.BillingStatusSettled, UserCost: 10, OwnerRevenue: 7.5, PlatformRevenue: 2.5, CreatedAt: day1},
		{ChannelID: channel.ID, Status: model.BillingStatusSettled, UserCost: 5, OwnerRevenue: 4, PlatformRevenue: 1, CreatedAt: day1},
		{ChannelID: channel.ID, Status: model.BillingStatusSettled, UserCost: 2, OwnerRevenue: 1, PlatformRevenue: 1, CreatedAt: day2},
		{ChannelID: channel.ID, Status: model.BillingStatusRefunded, UserCost: 99, OwnerRevenue: 90, PlatformRevenue: 9, CreatedAt: day1},
		{ChannelID: other.ID, Status: model.BillingStatusSettled, UserCost: 77, OwnerRevenue: 70, PlatformRevenue: 7, CreatedAt: day1},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed billing records: %v", err)
	}

	base := fmt.Sprintf("/api/v1/channel/profit/%d", channel.ID)
	window := "?start_date=2026-10-01&end_date=2026-10-02"

	if w := ticketAPI(t, engine, http.MethodGet, base+window, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
	// 普通用户即使是渠道归属者也不该看到平台加价空间。
	if w := ticketAPI(t, engine, http.MethodGet, base+window, userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", w.Code, w.Body.String())
	}

	w := ticketAPI(t, engine, http.MethodGet, base+window, adminToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	data := ticketAPIData(t, w)
	if data["channel_id"].(float64) != float64(channel.ID) || data["channel_name"] != "profit-api" {
		t.Fatalf("identity: %+v", data)
	}
	if data["start_date"] != "2026-10-01" || data["end_date"] != "2026-10-02" {
		t.Fatalf("range: %+v", data)
	}
	totals, ok := data["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals missing: %+v", data)
	}
	if totals["requests"].(float64) != 3 ||
		totals["user_cost"].(float64) != 17 ||
		totals["owner_revenue"].(float64) != 12.5 ||
		totals["platform_revenue"].(float64) != 4.5 {
		t.Fatalf("totals: %+v", totals)
	}
	// 毛利率 = 平台毛利 / 收入 × 100 = 4.5 / 17 × 100。
	if margin := totals["margin_rate"].(float64); margin < 26.46 || margin > 26.48 {
		t.Fatalf("margin_rate = %v, want ~26.47", margin)
	}
	daily, ok := data["daily"].([]any)
	if !ok || len(daily) != 2 {
		t.Fatalf("daily: %+v", data["daily"])
	}
	first := daily[0].(map[string]any)
	if first["date"] != "2026-10-01" || first["requests"].(float64) != 2 || first["user_cost"].(float64) != 15 {
		t.Fatalf("daily[0]: %+v", first)
	}

	// 空区间: 200 + 零汇总 + daily 为 [] (不是 null)。
	empty := ticketAPI(t, engine, http.MethodGet, base+"?start_date=2025-01-01&end_date=2025-01-02", adminToken, "")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", empty.Code, empty.Body.String())
	}
	emptyData := ticketAPIData(t, empty)
	emptyTotals := emptyData["totals"].(map[string]any)
	if emptyTotals["requests"].(float64) != 0 || emptyTotals["user_cost"].(float64) != 0 || emptyTotals["margin_rate"].(float64) != 0 {
		t.Fatalf("empty totals: %+v", emptyTotals)
	}
	if list, ok := emptyData["daily"].([]any); !ok || len(list) != 0 {
		t.Fatalf("empty daily should be []: %+v", emptyData["daily"])
	}

	// 渠道不存在 / 主键非法 / 日期非法 / 起止倒挂都必须是 4xx, 且不回显底层错误。
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/channel/profit/999999", adminToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown channel: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/channel/profit/abc", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, base+"?start_date=2026-02-30", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, base+"?start_date=2026-10-31&end_date=2026-10-01", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("inverted range: %d %s", w.Code, w.Body.String())
	}
	// 区间上限 366 天: 367 天(含首尾)按参数错误拒绝, 防止一次误操作扫十年账单。
	if w := ticketAPI(t, engine, http.MethodGet, base+"?start_date=2025-01-01&end_date=2026-01-02", adminToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("range over limit: %d %s", w.Code, w.Body.String())
	}
	// 缺省日期: 最近 30 天, 今天的行必须命中。
	if w := ticketAPI(t, engine, http.MethodGet, base, adminToken, ""); w.Code != http.StatusOK {
		t.Fatalf("default window: %d %s", w.Code, w.Body.String())
	}
}
