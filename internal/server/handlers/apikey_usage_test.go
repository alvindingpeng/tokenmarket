package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

// usageTestKey 建一把归属指定用户的 Key(明文由 op 生成, 落库只留指纹)。
func usageTestKey(t *testing.T, userID uint, name string) model.APIKey {
	t.Helper()
	key := model.APIKey{Name: name, UserID: userID}
	if err := op.APIKeyCreate(&key, context.Background()); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return key
}

// TestAPIKeyUsageHandlerRejectsBadParams 缺失/非法 api_key_id 与非法日期一律 400,
// 且不因参数错误泄露任何 Key 信息。
func TestAPIKeyUsageHandlerRejectsBadParams(t *testing.T) {
	apikeyHandlerTestDB(t)
	cases := []string{
		"/api/v1/apikey/usage",
		"/api/v1/apikey/usage?api_key_id=",
		"/api/v1/apikey/usage?api_key_id=abc",
		"/api/v1/apikey/usage?api_key_id=0",
		"/api/v1/apikey/usage?api_key_id=-3",
		"/api/v1/apikey/usage?api_key_id=1&from=2026-13-01",
		"/api/v1/apikey/usage?api_key_id=1&to=not-a-date",
	}
	for _, target := range cases {
		c, w := apikeyTestContext(t, http.MethodGet, target, "")
		getAPIKeyUsage(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400 (%s)", target, w.Code, w.Body.String())
		}
	}
}

// TestAPIKeyUsageHandlerNotFoundForOthersKey 他人 Key 一律 404 语义:
// 不能因为"存在但无权"给 403 —— 那会泄露 Key 的存在性, 而主键是连续整数。
func TestAPIKeyUsageHandlerNotFoundForOthersKey(t *testing.T) {
	apikeyHandlerTestDB(t)
	other := usageTestKey(t, 2, "other-user-key")

	c, w := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/usage?api_key_id="+strconv.Itoa(other.ID), "")
	getAPIKeyUsage(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("other owner key: code = %d, want 404 (%s)", w.Code, w.Body.String())
	}
	// 不存在的 Key 同样是 404: 两种情况响应不可区分。
	c2, w2 := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/usage?api_key_id=999999", "")
	getAPIKeyUsage(c2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing key: code = %d, want 404", w2.Code)
	}
	if w.Body.String() != w2.Body.String() {
		t.Fatalf("existing-but-foreign and missing must look identical: %q vs %q", w.Body.String(), w2.Body.String())
	}
}

// TestAPIKeyUsageHandlerOwnKeyReturnsReport 自己的 Key 返回 200, total 与造的数据一致,
// 且响应不含明文与指纹。
func TestAPIKeyUsageHandlerOwnKeyReturnsReport(t *testing.T) {
	apikeyHandlerTestDB(t)
	key := usageTestKey(t, 1, "own-key")
	now := time.Now()
	record := model.BillingRecord{
		UserID: 1, OwnerID: 2, ChannelID: 1, APIKeyID: key.ID, ModelName: "gpt-test",
		InputToken: 100, OutputToken: 50, UserCost: 3, OwnerRevenue: 1.5, PlatformRevenue: 1.5,
		Status: model.BillingStatusSettled, CreatedAt: now.Add(-2 * time.Hour),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed record: %v", err)
	}

	c, w := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/usage?api_key_id="+strconv.Itoa(key.ID), "")
	getAPIKeyUsage(c)
	if w.Code != http.StatusOK {
		t.Fatalf("own key: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "key_hash") || strings.Contains(body, "\"api_key\"") || strings.Contains(body, key.KeyHash) {
		t.Fatalf("usage response leaked key material: %s", body)
	}
	data := decodeAPIKeyResponse(t, body)
	total, ok := data["total"].(map[string]any)
	if !ok {
		t.Fatalf("missing total in %s", body)
	}
	if total["request_count"] != float64(1) || total["input_token"] != float64(100) || total["output_token"] != float64(50) {
		t.Fatalf("total mismatch: %v", total)
	}
	if total["user_cost"] != float64(3) {
		t.Fatalf("user_cost = %v, want 3", total["user_cost"])
	}
	if data["api_key_id"] != float64(key.ID) {
		t.Fatalf("api_key_id = %v, want %d", data["api_key_id"], key.ID)
	}
	days, ok := data["days"].([]any)
	if !ok {
		t.Fatalf("days must be an array: %s", body)
	}
	if len(days) != 1 {
		t.Fatalf("days = %d, want 1", len(days))
	}
	day, _ := days[0].(map[string]any)
	if day["request_count"] != float64(1) || day["date"] == "" {
		t.Fatalf("day entry mismatch: %v", day)
	}
}

// TestAPIKeyUsageHandlerAdminSeesAnyKey 管理员看别人的 Key 不做归属拦截(但依旧只读, 且无密钥材料)。
func TestAPIKeyUsageHandlerAdminSeesAnyKey(t *testing.T) {
	apikeyHandlerTestDB(t)
	key := usageTestKey(t, 2, "admin-viewed")
	c, w := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/usage?api_key_id="+strconv.Itoa(key.ID), "")
	c.Set("role", string(model.RoleAdmin))
	getAPIKeyUsage(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin view: %d %s", w.Code, w.Body.String())
	}
}

// TestAPIKeyUsageHandlerEmptyDaysIsArray 没有数据的 Key 也必须给非 null 的 days 数组:
// 前端按数组渲染, null 会让图表组件直接崩。
func TestAPIKeyUsageHandlerEmptyDaysIsArray(t *testing.T) {
	apikeyHandlerTestDB(t)
	key := usageTestKey(t, 1, "empty-key")
	c, w := apikeyTestContext(t, http.MethodGet, "/api/v1/apikey/usage?api_key_id="+strconv.Itoa(key.ID), "")
	getAPIKeyUsage(c)
	if w.Code != http.StatusOK {
		t.Fatalf("empty key: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "\"days\":[") {
		t.Fatalf("days must serialize as an array: %s", w.Body.String())
	}
}

// TestAPIKeyUsageDocumentedInOpenAPI 新端点必须进文档: 前端与外部调用方靠它知道
// 这是会话鉴权接口、api_key_id 必填、to 含当天, 以及"上线前归档的日子补不回来"这一上限。
func TestAPIKeyUsageDocumentedInOpenAPI(t *testing.T) {
	paths := openAPIAdminPaths()
	route, ok := paths["/api/v1/apikey/usage"].(gin.H)
	if !ok {
		t.Fatal("missing OpenAPI path /api/v1/apikey/usage")
	}
	operation, ok := route["get"].(gin.H)
	if !ok || operation["summary"] == nil {
		t.Fatal("missing operation get /api/v1/apikey/usage")
	}
	description, _ := operation["description"].(string)
	for _, want := range []string{"coverage_start", "404", "留存"} {
		if !strings.Contains(description, want) {
			t.Fatalf("usage description must document %q, got %q", want, description)
		}
	}
	params, ok := operation["parameters"].([]gin.H)
	if !ok || len(params) != 3 {
		t.Fatalf("usage must document 3 query parameters, got %v", operation["parameters"])
	}
	required, _ := params[0]["name"].(string)
	if required != "api_key_id" {
		t.Fatalf("first parameter = %q, want api_key_id", required)
	}
	if params[0]["required"] != true {
		t.Fatal("api_key_id must be documented as required")
	}
	schemas := openAPIAdminSchemas()
	for _, name := range []string{"APIKeyUsageReport", "APIKeyUsageDay", "APIKeyUsageTotals"} {
		if schemas[name] == nil {
			t.Fatalf("schema %s missing from the admin openapi docs", name)
		}
	}
}
