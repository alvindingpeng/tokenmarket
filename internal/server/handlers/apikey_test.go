package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

func apikeyHandlerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "apikey-handler.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
}

func apikeyTestContext(t *testing.T, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", 1)
	c.Set("role", string(model.RoleUser))
	return c, w
}

func decodeAPIKeyResponse(t *testing.T, body string) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("bad response %s: %v", body, err)
	}
	return envelope.Data
}

// TestCreateAPIKeyReturnsPlaintextExactlyOnce 明文只在创建响应里出现一次, 指纹永远不出 API。
func TestCreateAPIKeyReturnsPlaintextExactlyOnce(t *testing.T) {
	apikeyHandlerTestDB(t)

	c, w := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/create", `{"name":"once","supported_models":[]}`)
	createAPIKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "key_hash") {
		t.Fatalf("fingerprint leaked in create response: %s", body)
	}
	data := decodeAPIKeyResponse(t, body)
	plaintext, _ := data["api_key"].(string)
	if plaintext == "" {
		t.Fatalf("create response must carry the plaintext exactly once: %s", body)
	}
	if data["key_prefix"] == "" || data["key_last4"] == "" {
		t.Fatalf("mask fields missing from create response: %s", body)
	}

	// 列表: 只有掩码, 没有明文也没有指纹。
	c2, w2 := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/list", "")
	listAPIKey(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w2.Code, w2.Body.String())
	}
	listBody := w2.Body.String()
	if strings.Contains(listBody, plaintext) || strings.Contains(listBody, "\"api_key\"") {
		t.Fatalf("list leaked the key: %s", listBody)
	}
	if strings.Contains(listBody, "key_hash") {
		t.Fatalf("fingerprint leaked in list response: %s", listBody)
	}
	if !strings.Contains(listBody, "key_prefix") || !strings.Contains(listBody, "key_last4") {
		t.Fatalf("list must give the mask fields: %s", listBody)
	}
}

// TestCreateAPIKeyRejectsCustomKey 自定义密钥已停用: 请求带 api_key 一律 400, 密钥一律服务端高熵生成。
// 自定义的 password 类低熵密钥会把无盐 SHA-256 指纹变成字典爆破的靶子, 新建这条路因此彻底封死。
func TestCreateAPIKeyRejectsCustomKey(t *testing.T) {
	apikeyHandlerTestDB(t)

	c, w := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/create", `{"name":"custom","api_key":"password"}`)
	createAPIKey(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("custom key must be rejected with 400, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("rejection must not echo the custom key")
	}
}

// TestUpdateAPIKeyRejectsKeyReplacement 禁止更新替换原 Key: 带明文的更新直接拒。
func TestUpdateAPIKeyRejectsKeyReplacement(t *testing.T) {
	apikeyHandlerTestDB(t)

	c, w := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/create", `{"name":"fixed","supported_models":[]}`)
	createAPIKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decodeAPIKeyResponse(t, w.Body.String())
	id := int(created["id"].(float64))
	plaintext := created["api_key"].(string)

	c2, w2 := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/update",
		`{"id":`+strconv.Itoa(id)+`,"name":"fixed","api_key":"sk-replacement"}`)
	updateAPIKey(c2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("replacement must be rejected with 400, got %d %s", w2.Code, w2.Body.String())
	}
	if _, err := op.APIKeyGetByAPIKey(plaintext, c2.Request.Context()); err != nil {
		t.Fatalf("original key must keep working: %v", err)
	}
}

// TestUpdateAPIKeyKeepsUnsubmittedFields 更新只动提交的字段, 且响应不再回显明文。
func TestUpdateAPIKeyKeepsUnsubmittedFields(t *testing.T) {
	apikeyHandlerTestDB(t)

	c, w := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/create",
		`{"name":"patch","supported_models":["gpt"],"quota_requests":6,"max_cost":2.5}`)
	createAPIKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decodeAPIKeyResponse(t, w.Body.String())
	id := int(created["id"].(float64))
	plaintext := created["api_key"].(string)

	c2, w2 := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/update",
		`{"id":`+strconv.Itoa(id)+`,"name":"patched"}`)
	updateAPIKey(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w2.Code, w2.Body.String())
	}
	updated := w2.Body.String()
	if strings.Contains(updated, plaintext) || strings.Contains(updated, "\"api_key\"") {
		t.Fatalf("update response must not echo the key: %s", updated)
	}
	if strings.Contains(updated, "key_hash") {
		t.Fatalf("fingerprint leaked in update response: %s", updated)
	}
	data := decodeAPIKeyResponse(t, updated)
	if data["name"] != "patched" {
		t.Fatalf("name not updated: %s", updated)
	}
	if data["quota_requests"] != float64(6) || data["max_cost"] != 2.5 {
		t.Fatalf("unsubmitted fields must survive the update: %s", updated)
	}
	if _, err := op.APIKeyGetByAPIKey(plaintext, c2.Request.Context()); err != nil {
		t.Fatalf("key broken by rename: %v", err)
	}
}

// TestAPIKeyStatsInfoNeverExposesSecrets Key 面板的 info 同样只给掩码。
func TestAPIKeyStatsInfoNeverExposesSecrets(t *testing.T) {
	apikeyHandlerTestDB(t)

	c, w := apikeyTestContext(t, http.MethodPost, "/api/v1/apikey/create", `{"name":"stats","supported_models":[]}`)
	createAPIKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decodeAPIKeyResponse(t, w.Body.String())
	id := int(created["id"].(float64))

	c2, w2 := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/stats", "")
	c2.Set("api_key_id", id)
	getStatsAPIKeyById(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w2.Code, w2.Body.String())
	}
	body := w2.Body.String()
	if strings.Contains(body, "key_hash") || strings.Contains(body, "\"api_key\"") {
		t.Fatalf("stats leaked key material: %s", body)
	}
	if !strings.Contains(body, "key_prefix") {
		t.Fatalf("stats info must carry the mask: %s", body)
	}
}
