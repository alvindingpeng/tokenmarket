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

	"github.com/alvindingpeng/switchyard/internal/db"
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
