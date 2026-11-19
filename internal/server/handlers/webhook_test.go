package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func webhookHandlersTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "webhook-handlers.db"), false); err != nil {
		t.Fatal(err)
	}
	if err := secretstore.Init(secretstore.KEKConfig{Inline: strings.Repeat("b2", 32)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		secretstore.Reset()
		_ = db.Close()
	})
	gin.SetMode(gin.TestMode)
}

// webhookTestEngine 按真实路由形状挂 handler, 身份由可切换的注入器提供(与 notification_identity_test 同法)。
func webhookTestEngine(t *testing.T) (*gin.Engine, *int) {
	t.Helper()
	currentUser := 41
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", currentUser)
		c.Set("role", "user")
		c.Next()
	})
	r.POST("/api/v1/webhook", middleware.RequireJSON(), webhookCreate)
	r.GET("/api/v1/webhook/list", webhookList)
	r.PUT("/api/v1/webhook/:id", middleware.RequireJSON(), webhookUpdate)
	r.DELETE("/api/v1/webhook/:id", webhookDelete)
	r.POST("/api/v1/webhook/:id/test", webhookTest)
	r.GET("/api/v1/webhook/:id/logs", webhookLogs)
	r.POST("/api/v1/webhook/:id/deliveries/:delivery_id/replay", webhookReplayDelivery)
	return r, &currentUser
}

func webhookRequest(t *testing.T, r *gin.Engine, method, path, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

const webhookValidCreateBody = `{"url":"https://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890"}`

// 真实路由注册(含 middleware.Auth)未带登录态一律 401; 路由形状(根 POST + /list + /:id 族)注册不冲突。
func TestWebhookRoutesRegisteredAndRequireAuth(t *testing.T) {
	webhookHandlersTestSetup(t)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/webhook", webhookValidCreateBody},
		{http.MethodGet, "/api/v1/webhook/list", ""},
		{http.MethodPut, "/api/v1/webhook/1", "{}"},
		{http.MethodDelete, "/api/v1/webhook/1", ""},
		{http.MethodPost, "/api/v1/webhook/1/test", ""},
		{http.MethodGet, "/api/v1/webhook/1/logs", ""},
		{http.MethodPost, "/api/v1/webhook/1/deliveries/abc/replay", ""},
	} {
		w := httptest.NewRecorder()
		httpReq := httptest.NewRequest(req.method, req.path, strings.NewReader(req.body))
		httpReq.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, httpReq)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without auth = %d %s, want 401", req.method, req.path, w.Code, w.Body.String())
		}
	}
}

// 身份隔离 + 密钥零回显 + 错误码。
func TestWebhookHandlersIdentityAndSecretLeak(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, currentUser := webhookTestEngine(t)

	code, body := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", webhookValidCreateBody)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, body)
	}
	if strings.Contains(body, "secret-key-1234567890") || strings.Contains(body, "secret_encrypted") {
		t.Fatalf("secret leaked in create response: %s", body)
	}
	if !strings.Contains(body, "\"has_secret\":true") {
		t.Fatalf("has_secret missing: %s", body)
	}
	var created struct {
		Data struct {
			ID        uint64 `json:"id"`
			HasSecret bool   `json:"has_secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}

	// 列表按用户隔离, 且不回显密钥。
	code, body = webhookRequest(t, r, http.MethodGet, "/api/v1/webhook/list?page=1&page_size=10", "")
	if code != http.StatusOK || strings.Contains(body, "secret-key-1234567890") {
		t.Fatalf("list = %d %s", code, body)
	}

	// 换用户: 读/改/删/测试/日志一律 404 webhook_not_found。
	*currentUser = 42
	for _, req := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/webhook/" + uint64String(created.Data.ID), "{}"},
		{http.MethodDelete, "/api/v1/webhook/" + uint64String(created.Data.ID), ""},
		{http.MethodPost, "/api/v1/webhook/" + uint64String(created.Data.ID) + "/test", ""},
		{http.MethodGet, "/api/v1/webhook/" + uint64String(created.Data.ID) + "/logs", ""},
	} {
		code, body = webhookRequest(t, r, req.method, req.path, req.body)
		if code != http.StatusNotFound || !strings.Contains(body, "webhook_not_found") {
			t.Fatalf("%s %s as other user = %d %s, want 404 webhook_not_found", req.method, req.path, code, body)
		}
	}
	code, body = webhookRequest(t, r, http.MethodGet, "/api/v1/webhook/list", "")
	if code != http.StatusOK || !strings.Contains(body, "\"items\":[]") {
		t.Fatalf("other user list must be empty: %d %s", code, body)
	}

	// 本人: 更新留空 secret 保留, 日志不含 URL/密钥。
	*currentUser = 41
	code, body = webhookRequest(t, r, http.MethodPut, "/api/v1/webhook/"+uint64String(created.Data.ID), `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("update = %d %s", code, body)
	}
	if !strings.Contains(body, "\"has_secret\":true") {
		t.Fatalf("empty secret must keep old secret: %s", body)
	}
	code, body = webhookRequest(t, r, http.MethodPost, "/api/v1/webhook/"+uint64String(created.Data.ID)+"/test", "")
	if code != http.StatusOK || !strings.Contains(body, "delivery_id") {
		t.Fatalf("test = %d %s", code, body)
	}
	code, body = webhookRequest(t, r, http.MethodPost, "/api/v1/webhook/"+uint64String(created.Data.ID)+"/test", "")
	if code != http.StatusTooManyRequests || !strings.Contains(body, "webhook_test_rate_limited") {
		t.Fatalf("second test = %d %s, want 429 webhook_test_rate_limited", code, body)
	}
	code, body = webhookRequest(t, r, http.MethodGet, "/api/v1/webhook/"+uint64String(created.Data.ID)+"/logs", "")
	if code != http.StatusOK {
		t.Fatalf("logs = %d %s", code, body)
	}
	for _, forbidden := range []string{"hooks.example.com", "secret", "payload"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("logs leak %q: %s", forbidden, body)
		}
	}
}

// 校验错误码: URL/密钥非法与配额超限。
func TestWebhookHandlersValidationAndQuotaCodes(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, _ := webhookTestEngine(t)

	cases := []struct {
		name, body string
		wantCode   int
		wantErr    string
	}{
		{"http url", `{"url":"http://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890"}`, 400, "invalid_webhook_url"},
		{"private url", `{"url":"https://127.0.0.1/x","events":["system"],"secret":"secret-key-1234567890"}`, 400, "invalid_webhook_url"},
		{"userinfo url", `{"url":"https://user:pass@hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890"}`, 400, "invalid_webhook_url"},
		{"bad events", `{"url":"https://hooks.example.com/x","events":["nope"],"secret":"secret-key-1234567890"}`, 400, "invalid_webhook_events"},
		{"short secret", `{"url":"https://hooks.example.com/x","events":["system"],"secret":"short"}`, 400, "invalid_webhook_secret"},
		{"bad retry", `{"url":"https://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890","retry_count":9}`, 400, "invalid_request"},
	}
	for _, tc := range cases {
		code, body := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", tc.body)
		if code != tc.wantCode || !strings.Contains(body, tc.wantErr) {
			t.Fatalf("%s: %d %s, want %d %s", tc.name, code, body, tc.wantCode, tc.wantErr)
		}
	}

	// 配额: 第 11 条 409。
	okBody := `{"url":"https://hooks.example.com/q%d","events":["system"],"secret":"secret-key-1234567890"}`
	for i := 0; i < 10; i++ {
		code, body := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", strings.Replace(okBody, "%d", uint64String(uint64(i)), 1))
		if code != http.StatusOK {
			t.Fatalf("create %d = %d %s", i, code, body)
		}
	}
	code, body := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", strings.Replace(okBody, "%d", "overflow", 1))
	if code != http.StatusConflict || !strings.Contains(body, "webhook_quota_exceeded") {
		t.Fatalf("quota = %d %s, want 409 webhook_quota_exceeded", code, body)
	}
}

// handler 不回显底层错误原文: url.Error 内嵌完整 URL(可能带 user:pass/token),
// 无论已知还是未知错误, 响应只给固定文案与错误码。
func TestWebhookHandlersNeverEchoUnderlyingError(t *testing.T) {
	webhookHandlersTestSetup(t)

	raw := &url.Error{
		Op:  "Post",
		URL: "https://user:pass@hooks.example.com/x?token=super-secret-token",
		Err: errors.New("dial tcp 10.0.0.5:443: connect: connection refused"),
	}
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"unknown wrapped url error", fmt.Errorf("send webhook: %w", raw), http.StatusInternalServerError},
		{"secret open failure", fmt.Errorf("webhook secret unavailable: %w", raw), http.StatusInternalServerError},
		{"db error with SQL text", errors.New("database write failed: INSERT INTO webhook_deliveries ..."), http.StatusInternalServerError},
		{"known url error wrapping leak", fmt.Errorf("%w: %v", op.ErrWebhookURLInvalid, raw), http.StatusBadRequest},
		{"known not-found wrapping leak", fmt.Errorf("get webhook: %w: %v", op.ErrWebhookNotFound, raw), http.StatusNotFound},
		{"delivery not-found wrapping leak", fmt.Errorf("get delivery: %w: %v", op.ErrWebhookDeliveryNotFound, raw), http.StatusNotFound},
		{"not-replayable wrapping leak", fmt.Errorf("%w: %v", op.ErrWebhookDeliveryNotReplayable, raw), http.StatusConflict},
		{"invalid status filter", op.ErrWebhookStatusInvalid, http.StatusBadRequest},
		{"invalid event filter", op.ErrWebhookEventInvalid, http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/webhook", strings.NewReader("{}"))
		webhookRespondError(c, tc.err)
		body := w.Body.String()
		if w.Code != tc.wantCode {
			t.Fatalf("%s: code = %d body = %s, want %d", tc.name, w.Code, body, tc.wantCode)
		}
		for _, forbidden := range []string{"super-secret-token", "user:pass", "hooks.example.com", "10.0.0.5", "INSERT"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s: response echoes %q: %s", tc.name, forbidden, body)
			}
		}
		// 已知错误必须带机器可读 error_code; 未知内部错误按契约 error_code 可省略(只回通用文案)。
		if tc.wantCode != http.StatusInternalServerError && !strings.Contains(body, "error_code") {
			t.Fatalf("%s: missing machine-readable error_code: %s", tc.name, body)
		}
	}
}

func uint64String(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// webhookHandlerDeliveryRow 直接在库里造一条投递行: handler 测试只关心 HTTP 分流, 不走投递引擎。
func webhookHandlerDeliveryRow(t *testing.T, userID uint, hookID uint64, deliveryID, event, status string) {
	t.Helper()
	row := model.WebhookDelivery{
		DeliveryID:    deliveryID,
		WebhookID:     hookID,
		UserID:        userID,
		HookVersion:   1,
		Event:         event,
		Payload:       "{\"id\":\"" + deliveryID + "\"}",
		Status:        status,
		NextAttemptAt: time.Now(),
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("create delivery %s: %v", deliveryID, err)
	}
}

// 重放 + 日志过滤的 HTTP 契约: 非法过滤 400, 非终态重放 409, 未知投递 404, 越权 404。
func TestWebhookHandlersReplayAndLogFilters(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, currentUser := webhookTestEngine(t)

	code, body := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", webhookValidCreateBody)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, body)
	}
	var created struct {
		Data struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	hookID := created.Data.ID
	base := "/api/v1/webhook/" + uint64String(hookID)

	webhookHandlerDeliveryRow(t, 41, hookID, "h-failed-system", "system", model.WebhookDeliveryStatusFailed)
	webhookHandlerDeliveryRow(t, 41, hookID, "h-failed-recharge", "recharge", model.WebhookDeliveryStatusFailed)
	webhookHandlerDeliveryRow(t, 41, hookID, "h-succeeded-system", "system", model.WebhookDeliveryStatusSucceeded)
	webhookHandlerDeliveryRow(t, 41, hookID, "h-pending-system", "system", model.WebhookDeliveryStatusPending)

	type logPage struct {
		Data struct {
			Items []struct {
				DeliveryID string `json:"delivery_id"`
				Event      string `json:"event"`
				Status     string `json:"status"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	getLogs := func(query string) (int, string, logPage) {
		t.Helper()
		code, body := webhookRequest(t, r, http.MethodGet, base+"/logs"+query, "")
		var page logPage
		if code == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &page); err != nil {
				t.Fatalf("logs%s: %v (%s)", query, err, body)
			}
		}
		return code, body, page
	}

	valid := []struct {
		query      string
		wantStatus string
		wantEvent  string
		wantTotal  int64
	}{
		{"", "", "", 4},
		{"?status=failed", model.WebhookDeliveryStatusFailed, "", 2},
		{"?event=system", "", "system", 3},
		{"?status=failed&event=recharge", model.WebhookDeliveryStatusFailed, "recharge", 1},
		{"?event=test", "", model.WebhookEventTest, 0},
	}
	for _, tc := range valid {
		code, body, page := getLogs(tc.query)
		if code != http.StatusOK {
			t.Fatalf("logs%s = %d %s", tc.query, code, body)
		}
		if page.Data.Total != tc.wantTotal || int64(len(page.Data.Items)) != tc.wantTotal {
			t.Fatalf("logs%s total=%d items=%d, want %d", tc.query, page.Data.Total, len(page.Data.Items), tc.wantTotal)
		}
		for _, item := range page.Data.Items {
			if tc.wantStatus != "" && item.Status != tc.wantStatus {
				t.Fatalf("logs%s: status = %s", tc.query, item.Status)
			}
			if tc.wantEvent != "" && item.Event != tc.wantEvent {
				t.Fatalf("logs%s: event = %s", tc.query, item.Event)
			}
		}
	}

	// 闭集外的过滤值是 400 + 专属错误码, 不是"查不到"的空列表。
	code, body, _ = getLogs("?status=bogus")
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_webhook_status") {
		t.Fatalf("bogus status = %d %s, want 400 invalid_webhook_status", code, body)
	}
	code, body, _ = getLogs("?event=bogus")
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid_webhook_event") {
		t.Fatalf("bogus event = %d %s, want 400 invalid_webhook_event", code, body)
	}

	// 重放 failed 投递: 200 + 新投递(pending), 原行不动。
	type replayResp struct {
		Data struct {
			Delivery struct {
				DeliveryID   string `json:"delivery_id"`
				Event        string `json:"event"`
				Status       string `json:"status"`
				Attempts     int    `json:"attempts"`
				ResponseCode int    `json:"response_code"`
			} `json:"delivery"`
		} `json:"data"`
	}
	code, body = webhookRequest(t, r, http.MethodPost, base+"/deliveries/h-failed-system/replay", "")
	if code != http.StatusOK {
		t.Fatalf("replay = %d %s", code, body)
	}
	for _, forbidden := range []string{"hooks.example.com", "payload", "h-failed-system"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("replay response leaks %q: %s", forbidden, body)
		}
	}
	var first replayResp
	if err := json.Unmarshal([]byte(body), &first); err != nil {
		t.Fatal(err)
	}
	if first.Data.Delivery.DeliveryID == "" || first.Data.Delivery.DeliveryID == "h-failed-system" {
		t.Fatalf("replay must return a fresh delivery id: %s", body)
	}
	if first.Data.Delivery.Status != model.WebhookDeliveryStatusPending || first.Data.Delivery.Attempts != 0 ||
		first.Data.Delivery.ResponseCode != 0 || first.Data.Delivery.Event != "system" {
		t.Fatalf("replay delivery = %+v", first.Data.Delivery)
	}
	// 原行仍是 failed: 可以再次重放, 每次都是一条新的待投递。
	code, body = webhookRequest(t, r, http.MethodPost, base+"/deliveries/h-failed-system/replay", "")
	if code != http.StatusOK {
		t.Fatalf("second replay = %d %s", code, body)
	}
	var second replayResp
	if err := json.Unmarshal([]byte(body), &second); err != nil {
		t.Fatal(err)
	}
	if second.Data.Delivery.DeliveryID == first.Data.Delivery.DeliveryID {
		t.Fatalf("each replay must get its own delivery id: %s", body)
	}
	if code, body, page := getLogs(""); code != http.StatusOK || page.Data.Total != 6 {
		t.Fatalf("logs after replay = %d %s total=%d, want 6", code, body, page.Data.Total)
	}

	// 非终态拒绝重放。
	code, body = webhookRequest(t, r, http.MethodPost, base+"/deliveries/h-pending-system/replay", "")
	if code != http.StatusConflict || !strings.Contains(body, "webhook_delivery_not_replayable") {
		t.Fatalf("replay pending = %d %s, want 409 webhook_delivery_not_replayable", code, body)
	}
	// 投递不存在。
	code, body = webhookRequest(t, r, http.MethodPost, base+"/deliveries/nope/replay", "")
	if code != http.StatusNotFound || !strings.Contains(body, "webhook_delivery_not_found") {
		t.Fatalf("replay missing = %d %s, want 404 webhook_delivery_not_found", code, body)
	}
	// 越权: 别人的订阅一律 webhook_not_found。
	*currentUser = 42
	code, body = webhookRequest(t, r, http.MethodPost, base+"/deliveries/h-failed-system/replay", "")
	if code != http.StatusNotFound || !strings.Contains(body, "webhook_not_found") {
		t.Fatalf("replay as other user = %d %s, want 404 webhook_not_found", code, body)
	}
	code, body = webhookRequest(t, r, http.MethodGet, base+"/logs?status=failed", "")
	if code != http.StatusNotFound || !strings.Contains(body, "webhook_not_found") {
		t.Fatalf("filtered logs as other user = %d %s, want 404 webhook_not_found", code, body)
	}
}
