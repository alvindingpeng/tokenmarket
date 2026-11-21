package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"net/http"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// userStatsRollupDayStart 返回本地挂钟某天的 00:00, 与 op.dayStart 同口径。
func userStatsRollupDayStart(at time.Time) time.Time {
	at = at.In(time.Local)
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.Local)
}

// userStatsRollupRawData 取出成功响应里的 data 字段原文, 便于按字段名断言 JSON 契约。
func userStatsRollupRawData(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	var payload struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("parse response %s: %v", string(body), err)
	}
	if payload.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", payload.Code, string(body))
	}
	return payload.Data
}

// TestUserStatsOverviewMergesRollups 用户统计接口与账单页必须同口径:
// 明细被留存清理后, 概览里的金额/词元/请求数要由日汇总补齐, 并如实返回覆盖边界。
func TestUserStatsOverviewMergesRollups(t *testing.T) {
	token, _, _, _, user, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	recentDay := userStatsRollupDayStart(time.Now()).AddDate(0, 0, -3)
	oldDay := userStatsRollupDayStart(time.Now()).AddDate(0, 0, -400)

	if err := db.GetDB().Create(&model.BillingRecord{
		UserID: user.ID, ModelName: "recent-model", Status: model.BillingStatusSettled,
		UserCost: 3, InputToken: 20, CreatedAt: recentDay.Add(9 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 超期那段: 明细已被清理, 只剩一行用户维度的日汇总。
	if err := db.GetDB().Create(&model.BillingRollup{
		Dimension: model.BillingRollupUser, DimensionID: user.ID,
		Day: oldDay.Format("20060102"), InputToken: 10, RequestCount: 1, UserCost: 2,
		SealedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	suffix := "?start_date=" + oldDay.Format("2006-01-02") + "&end_date=" + recentDay.Format("2006-01-02")
	w := ticketAPI(t, engine, "GET", "/api/v1/user-stats/overview"+suffix, token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String())
	}
	var result model.UserStatsOverview
	if err := json.Unmarshal(userStatsRollupRawData(t, w.Body.Bytes()), &result); err != nil {
		t.Fatal(err)
	}
	if result.TotalRequests != 2 || result.TotalCost != 5 || result.TotalTokens != 30 {
		t.Fatalf("汇总未并入概览: %+v", result)
	}
	// JSON 字段名是前端契约: 三个覆盖边界必须按 snake_case 出现。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(userStatsRollupRawData(t, w.Body.Bytes()), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"billing_coverage_start", "billing_rollup_covered_until", "model_breakdown_coverage_start"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("响应缺少字段 %s: %s", key, w.Body.String())
		}
	}
	if result.BillingCoverageStart != oldDay.Format("2006-01-02") {
		t.Fatalf("billing_coverage_start = %q, want %s", result.BillingCoverageStart, oldDay.Format("2006-01-02"))
	}
	if result.BillingRollupCoveredUntil != oldDay.Format("2006-01-02") {
		t.Fatalf("billing_rollup_covered_until = %q, want %s", result.BillingRollupCoveredUntil, oldDay.Format("2006-01-02"))
	}
	if result.ModelBreakdownCoverageStart != recentDay.Format("2006-01-02") {
		t.Fatalf("model_breakdown_coverage_start = %q, want %s", result.ModelBreakdownCoverageStart, recentDay.Format("2006-01-02"))
	}

	// 趋势曲线同区间也要含汇总那一天, 否则图与概览对不上。
	w = ticketAPI(t, engine, "GET", "/api/v1/user-stats/trend"+suffix, token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("trend: %d %s", w.Code, w.Body.String())
	}
	var trend []model.UserStatsTrend
	if err := json.Unmarshal(userStatsRollupRawData(t, w.Body.Bytes()), &trend); err != nil {
		t.Fatal(err)
	}
	if len(trend) != 2 {
		t.Fatalf("趋势点数 = %d, want 2: %s", len(trend), w.Body.String())
	}
	if trend[0].Date != oldDay.Format("2006-01-02") || trend[0].Cost != 2 || trend[0].Requests != 1 {
		t.Fatalf("汇总那天未进曲线: %+v", trend[0])
	}
	if trend[1].Date != recentDay.Format("2006-01-02") || trend[1].Cost != 3 || trend[1].Requests != 1 {
		t.Fatalf("明细那天: %+v", trend[1])
	}

	// 按模型拆分只有明细可得: 用响应头说明覆盖起点(数组响应体不能加字段)。
	w = ticketAPI(t, engine, "GET", "/api/v1/user-stats/models"+suffix, token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Model-Coverage-Start"); got != recentDay.Format("2006-01-02") {
		t.Fatalf("模型覆盖起点响应头 = %q, want %s", got, recentDay.Format("2006-01-02"))
	}
}
