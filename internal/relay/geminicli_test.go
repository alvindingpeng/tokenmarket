package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
)

// Gemini CLI 渠道的对外契约有三条, 每条错了都表现为"能用但结果不对"或"永远拿不到响应",
// 且都不会给出指向原因的报错, 所以逐条钉住。
func TestGeminiCLIOutboundEnvelopeAndURL(t *testing.T) {
	outbound, err := newGeminiCLIOutbound("https://cloudcode-pa.googleapis.com", auth.NewStaticKeyProvider("access-token-abc"), "my-project")
	if err != nil {
		t.Fatalf("构造出站: %v", err)
	}

	stream := false
	req := &llm.Request{
		Model:    "gemini-2.5-flash",
		Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: strPtr("Say OK")}}},
		Stream:   &stream,
	}
	httpReq, err := outbound.TransformRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}

	// 1) 方法地址必须是冒号形式(v1internal:generateContent), 不是斜杠。
	// 写成斜杠会得到一个 404, 而错误正文里不提示正确形状。
	if !strings.Contains(httpReq.URL, "v1internal:generateContent") {
		t.Errorf("URL 不是 RPC 冒号形式: %s", httpReq.URL)
	}
	if strings.Contains(httpReq.URL, "v1internal/generateContent") {
		t.Errorf("URL 误用了斜杠形式: %s", httpReq.URL)
	}
	if !strings.HasPrefix(httpReq.URL, "https://cloudcode-pa.googleapis.com") {
		t.Errorf("URL 未使用渠道基址: %s", httpReq.URL)
	}

	// 2) 请求体是信封: {"project","model","request"}。
	// 少了 request 这层包装, 上游会把整个请求当成非法参数。
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(httpReq.Body, &envelope); err != nil {
		t.Fatalf("请求体不是 JSON: %v (%s)", err, httpReq.Body)
	}
	for _, field := range []string{"project", "model", "request"} {
		if _, ok := envelope[field]; !ok {
			t.Errorf("信封缺字段 %q: %s", field, httpReq.Body)
		}
	}
	var project string
	_ = json.Unmarshal(envelope["project"], &project)
	if project != "my-project" {
		t.Errorf("信封 project = %q, want my-project", project)
	}
	// request 内部必须是标准 Gemini 请求(含 contents)。
	if !strings.Contains(string(envelope["request"]), "contents") {
		t.Errorf("request 里没有 contents, 内层转换没生效: %s", envelope["request"])
	}

	// 3) 认证必须是 Bearer access_token, 且不带 x-goog-api-key。
	// 带 x-goog-api-key 会触发 license 错误(#3501)。
	if httpReq.Auth == nil || httpReq.Auth.Type != httpclient.AuthTypeBearer {
		t.Errorf("认证类型不是 Bearer: %+v", httpReq.Auth)
	}
	if httpReq.Auth.APIKey != "access-token-abc" {
		t.Errorf("Bearer 令牌 = %q", httpReq.Auth.APIKey)
	}
	if httpReq.Headers.Get("X-Goog-Api-Key") != "" {
		t.Error("不应设置 X-Goog-Api-Key(会触发 license 错误)")
	}
	// Antigravity 身份头缺一个就会被拒。
	if httpReq.Headers.Get("Client-Metadata") == "" {
		t.Error("缺 Client-Metadata 头")
	}
	if httpReq.Headers.Get("X-Goog-Api-Client") == "" {
		t.Error("缺 X-Goog-Api-Client 头")
	}
}

// 流式必须带 alt=sse 且改用 streamGenerateContent, 否则上游返回一整个 JSON 而非事件流。
func TestGeminiCLIOutboundStreamURL(t *testing.T) {
	outbound, err := newGeminiCLIOutbound("https://cloudcode-pa.googleapis.com", auth.NewStaticKeyProvider("tok"), "p")
	if err != nil {
		t.Fatalf("构造出站: %v", err)
	}
	stream := true
	req := &llm.Request{
		Model:    "gemini-2.5-flash",
		Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: strPtr("hi")}}},
		Stream:   &stream,
	}
	httpReq, err := outbound.TransformRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	if !strings.Contains(httpReq.URL, "v1internal:streamGenerateContent") {
		t.Errorf("流式 URL 未使用 streamGenerateContent: %s", httpReq.URL)
	}
	if !strings.Contains(httpReq.URL, "alt=sse") {
		t.Errorf("流式 URL 缺 alt=sse: %s", httpReq.URL)
	}
}

// 响应是双层包装: {traceId, response:{candidates}}. 直接读顶层会永远拿到空 candidates 且不报错。
func TestGeminiCLIOutboundUnwrapsDoubleLayerResponse(t *testing.T) {
	outbound, err := newGeminiCLIOutbound("https://cloudcode-pa.googleapis.com", auth.NewStaticKeyProvider("tok"), "p")
	if err != nil {
		t.Fatalf("构造出站: %v", err)
	}

	// 真实响应形状(取自 Antigravity 实测与 CLI 源码取证)。
	body := []byte(`{
		"traceId": "trace-1",
		"response": {
			"candidates": [{"index":0,"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],
			"usageMetadata": {"promptTokenCount": 3, "candidatesTokenCount": 1},
			"modelVersion": "gemini-2.5-flash"
		}
	}`)
	resp, err := outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       body,
	})
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("解包后没有 choices: 说明双层 response 未被正确展开")
	}
	if got := resp.Choices[0].Message.Content.Content; got == nil || *got != "OK" {
		t.Errorf("正文 = %v, want OK", got)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens == 0 {
		t.Errorf("用量未解析: %+v", resp.Usage)
	}

	// 反向验证: 顶层直接放 candidates(即未包装)时, 解析必须失败而不是静默给空结果。
	// 这条防的是"有人在测试里用简化样例", 把真实的双层形状掩盖掉。
	if _, err := outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       []byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`),
	}); err == nil {
		t.Error("顶层直接给 candidates 时应当报错(缺 response 层), 否则说明解包未真正强制")
	}
}

// 上游错误必须带出状态码与正文, 否则"403 因为缺 scope"会被显示成一句无信息的失败。
func TestGeminiCLIOutboundErrorKeepsUpstreamDetail(t *testing.T) {
	outbound, err := newGeminiCLIOutbound("https://cloudcode-pa.googleapis.com", auth.NewStaticKeyProvider("tok"), "p")
	if err != nil {
		t.Fatalf("构造出站: %v", err)
	}
	body := []byte(`{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`)
	_, err = outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusForbidden,
		Body:       body,
	})
	if err == nil {
		t.Fatal("403 应当报错")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "permission") {
		t.Errorf("错误信息丢了上游细节: %v", err)
	}
}

func strPtr(s string) *string { return &s }
