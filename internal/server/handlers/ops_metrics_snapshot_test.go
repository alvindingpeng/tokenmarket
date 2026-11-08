package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
)

// TestMetricsSnapshotRequiresAdministrator 走真实路由注册表验证鉴权:
// 匿名 401、普通用户 403、管理员 200。快照只读, 但泄漏的是全站经营数字
// (用户余额合计、累计成本), 因此必须与其他 ops 管理端点同一档: 仅管理员。
func TestMetricsSnapshotRequiresAdministrator(t *testing.T) {
	userToken, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	for _, actor := range []struct {
		name  string
		token string
		code  int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"non-admin", userToken, http.StatusForbidden},
		{"admin", adminToken, http.StatusOK},
	} {
		w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ops/metrics/snapshot", actor.token, "")
		if w.Code != actor.code {
			t.Fatalf("%s: got %d want %d: %s", actor.name, w.Code, actor.code, w.Body.String())
		}
		if actor.code == http.StatusOK && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("snapshot must be no-store, got %q", w.Header().Get("Cache-Control"))
		}
	}
}

// TestMetricsSnapshotShape 钉住响应形状与口径: 字段名必须与 op.CollectMetrics 的 JSON 一致,
// 数值必须是库里的真实聚合, 而不是占位零 —— 面板上的数字会被当成经营结果读。
func TestMetricsSnapshotShape(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	at := time.Date(2026, 11, 8, 3, 0, 0, 0, time.Local)
	rows := []model.RelayRequest{
		{ID: 9001, Status: "success", StartedAt: at, PromptTokens: 100, CompletionTokens: 40, Cost: 1.5},
		{ID: 9002, Status: "success", StartedAt: at, PromptTokens: 200, CompletionTokens: 60, Cost: 2.5},
		{ID: 9003, Status: "failed", StartedAt: at, PromptTokens: 7, CompletionTokens: 0, Cost: 0},
		{ID: 9004, Status: "canceled", StartedAt: at, PromptTokens: 3, CompletionTokens: 1, Cost: 0.25},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed relay requests: %v", err)
	}

	// 一条启用渠道 + 一条手工停用的渠道: channels_enabled 必须只数启用的那条。
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "metrics-snapshot-on"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	disabled := model.Channel{ChannelConfig: model.ChannelConfig{Name: "metrics-snapshot-off"}}
	if err := db.GetDB().Create(&disabled).Error; err != nil {
		t.Fatalf("create disabled channel: %v", err)
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	alerts := []model.ChannelAlert{
		{Kind: "down", Reason: "in window", CreatedAt: time.Now()},
		{Kind: "down", Reason: "in window", CreatedAt: time.Now()},
	}
	if err := db.GetDB().Create(&alerts).Error; err != nil {
		t.Fatalf("seed alerts: %v", err)
	}
	// 窗口外的告警不计数: 口径是"近 24 小时"。
	old := model.ChannelAlert{Kind: "down", Reason: "too old", CreatedAt: time.Now().Add(-48 * time.Hour)}
	if err := db.GetDB().Create(&old).Error; err != nil {
		t.Fatalf("seed old alert: %v", err)
	}

	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ops/metrics/snapshot", adminToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin snapshot: %d %s", w.Code, w.Body.String())
	}
	data := ticketAPIData(t, w)

	// 字段形状: 缺一个字段就是前端面板读不到数字。
	for _, key := range []string{
		"generated_at", "requests_by_status", "input_tokens", "output_tokens", "cost",
		"users", "balance", "channels", "channels_enabled", "alerts_24h",
	} {
		if _, ok := data[key]; !ok {
			t.Fatalf("snapshot missing field %q: %s", key, w.Body.String())
		}
	}
	if generated, _ := data["generated_at"].(string); generated == "" {
		t.Fatalf("generated_at empty: %s", w.Body.String())
	} else if _, err := time.Parse(time.RFC3339, generated); err != nil {
		t.Fatalf("generated_at not RFC3339: %q", generated)
	}

	statusCounts, ok := data["requests_by_status"].(map[string]any)
	if !ok {
		t.Fatalf("requests_by_status is not an object: %s", w.Body.String())
	}
	wantStatus := map[string]float64{"success": 2, "failed": 1, "canceled": 1}
	for status, want := range wantStatus {
		got, ok := statusCounts[status]
		if !ok || got.(float64) != want {
			t.Fatalf("requests_by_status[%q] = %v, want %v (all: %+v)", status, got, want, statusCounts)
		}
	}

	if got := data["input_tokens"].(float64); got != 310 {
		t.Fatalf("input_tokens = %v, want 310", got)
	}
	if got := data["output_tokens"].(float64); got != 101 {
		t.Fatalf("output_tokens = %v, want 101", got)
	}
	if got := data["cost"].(float64); got != 4.25 {
		t.Fatalf("cost = %v, want 4.25", got)
	}
	if got := data["channels"].(float64); got != 2 {
		t.Fatalf("channels = %v, want 2", got)
	}
	if got := data["channels_enabled"].(float64); got != 1 {
		t.Fatalf("channels_enabled = %v, want 1", got)
	}
	if got := data["alerts_24h"].(float64); got != 2 {
		t.Fatalf("alerts_24h = %v, want 2 (48h-old alert must not count)", got)
	}

	// users 与 balance 来自 users 表: 断言与库内聚合一致, 不写死具体人数
	// (环境里的账号数由测试辅助函数决定, 写死会与其改动脱钩)。
	var userAgg struct {
		Users   int64
		Balance float64
	}
	if err := db.GetDB().Model(&model.User{}).
		Select("count(*) as users, coalesce(sum(balance),0) as balance").
		Scan(&userAgg).Error; err != nil {
		t.Fatalf("aggregate users: %v", err)
	}
	if got := data["users"].(float64); got != float64(userAgg.Users) {
		t.Fatalf("users = %v, want %d", got, userAgg.Users)
	}
	if got := data["balance"].(float64); got != userAgg.Balance {
		t.Fatalf("balance = %v, want %v", got, userAgg.Balance)
	}
}

// TestMetricsSnapshotDocumentedInOpenAPI 端点进文档: 面板要照着 schema 读字段,
// 漏文档等于让调用方去猜字段名(且猜错不会报错, 只会显示空值)。
func TestMetricsSnapshotDocumentedInOpenAPI(t *testing.T) {
	paths := openAPIAdminPathsOps()
	route, ok := paths["/api/v1/ops/metrics/snapshot"].(gin.H)
	if !ok {
		t.Fatal("missing OpenAPI path /api/v1/ops/metrics/snapshot")
	}
	operation, ok := route["get"].(gin.H)
	if !ok || operation["summary"] == nil {
		t.Fatal("missing operation get /api/v1/ops/metrics/snapshot")
	}
	responses, ok := operation["responses"].(gin.H)
	if !ok {
		t.Fatalf("missing responses: %+v", operation)
	}
	if responses["403"] == nil || operation["security"] == nil {
		t.Fatalf("snapshot must document role denial and session auth: %+v", operation)
	}
	schema, ok := openAPIAdminSchemas()["MetricsSnapshot"].(gin.H)
	if !ok {
		t.Fatal("missing MetricsSnapshot schema")
	}
	properties, ok := schema["properties"].(gin.H)
	if !ok {
		t.Fatalf("MetricsSnapshot has no properties: %+v", schema)
	}
	for _, key := range []string{
		"generated_at", "requests_by_status", "input_tokens", "output_tokens", "cost",
		"users", "balance", "channels", "channels_enabled", "alerts_24h",
	} {
		if properties[key] == nil {
			t.Fatalf("MetricsSnapshot schema missing %q", key)
		}
	}
}
