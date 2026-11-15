package op

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestGeminiCLIMethodURLColonFormat 验证方法 URL 用冒号而非斜杠。
//
// 真实端点要求 {base}/v1internal:{method}, 写成斜杠会 404。
func TestGeminiCLIMethodURLColonFormat(t *testing.T) {
	url := geminiCLIMethodURL("https://daily.example.com", "loadCodeAssist")
	if url != "https://daily.example.com/v1internal:loadCodeAssist" {
		t.Errorf("got %q, want colon format", url)
	}
}

// TestGeminiCLILROURLSlashFormat 验证 LRO 轮询 URL 用斜杠而非冒号。
func TestGeminiCLILROURLSlashFormat(t *testing.T) {
	url := geminiCLILROURL("https://daily.example.com", "operations/abc123")
	if url != "https://daily.example.com/v1internal/operations/abc123" {
		t.Errorf("got %q, want slash format", url)
	}
}

// TestGeminiCLIHeadersContainAntigravityUA 验证请求头包含 Antigravity 两个必填头。
//
// 缺任一头即 403, 且错误体不提示缺哪个, 单测钉住防回归。
func TestGeminiCLIHeadersContainAntigravityUA(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://example.com", nil)
	applyGeminiCLIHeaders(req, "test-token")
	if req.Header.Get("Authorization") != "Bearer test-token" {
		t.Errorf("Authorization missing")
	}
	// 断言**语义特征**而不是与常量自身比较: 直接比较同一个常量的写法是自证式的 ——
	// 常量被改成什么都会通过, 起不到守护作用(实测把 UA 改成 "curl/8.0" 仍然 PASS)。
	// 这里钉住的是"必须是 Antigravity 身份", 这是上游据此决定给哪条 tier 分支的前提。
	ua := req.Header.Get("User-Agent")
	if ua == "" {
		t.Fatal("缺少 User-Agent")
	}
	if !strings.Contains(ua, "antigravity") {
		t.Errorf("User-Agent 必须声明 Antigravity 身份, 实际 %q", ua)
	}
	apiClient := req.Header.Get("X-Goog-Api-Client")
	if apiClient == "" {
		t.Fatal("缺少 X-Goog-Api-Client")
	}
	// Client-Metadata 里的 ideType 决定 tier 分支; 写成 pluginType=GEMINI 会撞上
	// Google 已停用的个人免费层(403 UNSUPPORTED_CLIENT)。
	meta := req.Header.Get("Client-Metadata")
	if !strings.Contains(meta, "ANTIGRAVITY") {
		t.Errorf("Client-Metadata 必须声明 ANTIGRAVITY, 实际 %q", meta)
	}
}

// TestRetrieveUserQuotaResponseParsing 解析 retrieveUserQuota 真实响应样例。
//
// 样例来自实测; buckets 里包含模型 ID 与 remainingFraction, 后者<=0 视作不可用。
func TestRetrieveUserQuotaResponseParsing(t *testing.T) {
	raw := []byte(`{
	  "quota": {
	    "buckets": [
	      {"modelId": "gemini-3-flash", "remainingFraction": 0.8, "remainingAmount": 1000, "resetTime": "2026-11-16T00:00:00Z"},
	      {"modelId": "gemini-3-pro", "remainingFraction": 0.0, "remainingAmount": 0, "resetTime": "2026-11-16T00:00:00Z"},
	      {"modelId": "gemini-3-ultra", "remainingFraction": 0.5, "remainingAmount": 500, "resetTime": "2026-11-16T00:00:00Z"}
	    ]
	  }
	}`)
	names, err := ParseRetrieveUserQuota(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// gemini-3-pro remainingFraction=0 被过滤
	want := []string{"gemini-3-flash", "gemini-3-ultra"}
	if len(names) != len(want) {
		t.Fatalf("got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("index %d: got %q want %q", i, names[i], want[i])
		}
	}
}

// TestRetrieveUserQuotaEmptyBuckets 无 buckets 返回空清单(不报错)。
// 这是实现行为: AvailableModels() 会返回 nil, 调用方根据 nil/len==0 再决定是否报错。
func TestRetrieveUserQuotaEmptyBuckets(t *testing.T) {
	raw := []byte(`{"quota":{}}`)
	models, err := ParseRetrieveUserQuota(raw)
	if err != nil {
		t.Fatalf("empty quota should not error: %v", err)
	}
	if models != nil && len(models) > 0 {
		t.Errorf("expected empty or nil, got %v", models)
	}
}

// TestGenerateContentEnvelopeStructure 验证信封结构 {model,project,user_prompt_id,request,enabled_credit_types?}。
func TestGenerateContentEnvelopeStructure(t *testing.T) {
	env, err := BuildGenerateContentEnvelope("proj-123", "gemini-3-flash", "prompt-abc", "sess-xyz", []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(env.Request), &m); err != nil {
		t.Fatalf("inner request unmarshal: %v", err)
	}
	// 顶层字段
	if env.Model != "gemini-3-flash" || env.Project != "proj-123" || env.UserPromptID != "prompt-abc" {
		t.Errorf("top-level fields: model=%q project=%q prompt=%q", env.Model, env.Project, env.UserPromptID)
	}
	// 内层 request 是完整的标准 Gemini 请求体
	var inner map[string]any
	if err := json.Unmarshal([]byte(env.Request), &inner); err != nil {
		t.Fatalf("inner unmarshal: %v", err)
	}
	if inner["contents"] == nil {
		t.Error("inner request missing contents")
	}
	// session_id 注入到了内层 request
	if inner["session_id"] != "sess-xyz" {
		t.Errorf("session_id missing or wrong: %v", inner["session_id"])
	}
}

// TestUnwrapGenerateContentResponseDoubleLayer 验证双层响应解包: candidates 在 .response 里。
func TestUnwrapGenerateContentResponseDoubleLayer(t *testing.T) {
	// 双层包装: 顶层 traceId/response/consumedCredits, 真实 candidates 在 response 里
	raw := []byte(`{
	  "traceId": "abc",
	  "response": {
	    "candidates": [{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
	    "usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2}
	  },
	  "consumedCredits": [],
	  "remainingCredits": []
	}`)
	inner, err := UnwrapGenerateContentResponse(raw)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	var innerObj map[string]any
	if err := json.Unmarshal(inner, &innerObj); err != nil {
		t.Fatalf("inner unmarshal: %v", err)
	}
	cands := innerObj["candidates"]
	if cands == nil {
		t.Fatal("candidates missing after unwrap")
	}
	candArr, ok := cands.([]any)
	if !ok || len(candArr) == 0 {
		t.Fatal("candidates not array or empty")
	}
}

// TestUnwrapGenerateContentResponseNoWrapper 无 response 包装时原样返回。
func TestUnwrapGenerateContentResponseNoWrapper(t *testing.T) {
	// 兼容某些镜像端点直接返回标准 Gemini 响应
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	inner, err := UnwrapGenerateContentResponse(raw)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(inner) != string(raw) {
		t.Errorf("no wrapper should return as-is")
	}
}

// TestGeminiCLIBasesDefault 解析默认基址组合: load=prod, data=daily。
func TestGeminiCLIBasesDefault(t *testing.T) {
	var cfg model.ChannelConfig
	load, data := GeminiCLIBases(cfg)
	if load != GeminiCLIBaseProd || data != GeminiCLIBaseDaily {
		t.Errorf("default: load=%q data=%q", load, data)
	}
}

// TestGeminiCLIBasesExplicitDaily 渠道显式配 daily 时 load 仍走 prod。
func TestGeminiCLIBasesExplicitDaily(t *testing.T) {
	cfg := model.ChannelConfig{BaseURL: GeminiCLIBaseDaily}
	load, data := GeminiCLIBases(cfg)
	if load != GeminiCLIBaseProd || data != GeminiCLIBaseDaily {
		t.Errorf("explicit daily: load=%q data=%q", load, data)
	}
}

// TestGeminiCLIBasesExplicitProd 渠道显式配 prod 时两侧都走 prod。
func TestGeminiCLIBasesExplicitProd(t *testing.T) {
	cfg := model.ChannelConfig{BaseURL: GeminiCLIBaseProd}
	load, data := GeminiCLIBases(cfg)
	if load != GeminiCLIBaseProd || data != GeminiCLIBaseProd {
		t.Errorf("explicit prod: load=%q data=%q", load, data)
	}
}

// TestGeminiCLIBasesCustomMirror 自建镜像时两侧都用同一地址。
func TestGeminiCLIBasesCustomMirror(t *testing.T) {
	cfg := model.ChannelConfig{BaseURL: "https://mirror.example.com"}
	load, data := GeminiCLIBases(cfg)
	if load != "https://mirror.example.com" || data != "https://mirror.example.com" {
		t.Errorf("custom: load=%q data=%q", load, data)
	}
}
