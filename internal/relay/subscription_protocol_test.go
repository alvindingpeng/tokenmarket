package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
)

// 生产上订阅渠道的模型上架后, 用 OpenAI 格式调用会打到 {base}/v1/chat/completions 并 404:
// 授权里混进了 OpenAI 协议位, buildOutbound 的 passthrough 就直接套用了 OpenAI 出站。
// 这里断言订阅渠道即使带着这样的协议位, 也只能走 v1internal 冒号协议。
func TestSubscriptionChannelIgnoresForeignProtocolBits(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "generateContent") {
			w.Write([]byte("{}"))
			return
		}
		// 订阅上游没有 OpenAI 端点: 打错路径就是 404, 正是生产上的症状。
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	// 混入 OpenAI / Anthropic 位: 这些协议在这个上游根本不存在, 只有 Gemini CLI 位真正可用。
	env := newRelayGroupDialect(t, upstream.URL,
		model.ProtocolGeminiCLI|model.ProtocolOpenAIChatCompletion|model.ProtocolAnthropicMessage,
		model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover), model.DialectSubscription,
		[]model.ChannelKeyConfig{{
			Name:            "antigravity",
			Key:             `{"access_token":"a.b.c","refresh_token":"rt"}`,
			Enabled:         true,
			Priority:        1,
			AccountKind:     model.AccountKindOAuth,
			AccountPlatform: model.AccountPlatformAntigravity,
		}})

	body := fmt.Sprintf("{\"model\":%q,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}", env.group)
	w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body)

	mu.Lock()
	got := append([]string(nil), paths...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("没有发出上游请求, 响应 code=%d body=%s", w.Code, w.Body.String())
	}
	for _, p := range got {
		if strings.Contains(p, "chat/completions") || strings.Contains(p, "/v1/messages") {
			t.Fatalf("订阅渠道被打到 %s (paths=%v): 协议位未收敛到 Gemini CLI", p, got)
		}
	}
}

// TestSubscriptionChannelIgnoresForeignProtocolBitsForResponses 覆盖 Responses 入口。
//
// 与上一个测试是同一个缺陷的另一条入口: 线上实测在 22:29 出现一条
// target_protocol=4(OpenAI Responses) 的 model_missing —— 说明 Responses 入站
// 也在某条路径上把 want 原样透传到了上游地址, 得到 Google 的 HTML 404 页面:
//
//	Request failed: Not Found, error: <!DOCTYPE html><title>Error 404 (Not Found)!!1</title>
//
// 上游 data 端点只有 v1internal:streamGenerateContent, 任何 /v1/responses 都是不存在的路径。
func TestSubscriptionChannelIgnoresForeignProtocolBitsForResponses(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "generateContent") {
			// 回一个最小可解析的 SSE 流, 让请求能走到成功终态。
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		// 订阅上游没有 OpenAI/Responses 端点: 打错路径就是 HTML 404。
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("<!DOCTYPE html><title>Error 404 (Not Found)!!1</title>"))
	}))
	defer upstream.Close()

	env := newRelayGroupDialect(t, upstream.URL,
		model.ProtocolGeminiCLI|model.ProtocolOpenAIResponse|model.ProtocolAnthropicMessage,
		model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover), model.DialectSubscription,
		[]model.ChannelKeyConfig{{
			Name:            "antigravity",
			Key:             `{"access_token":"a.b.c","refresh_token":"rt"}`,
			Enabled:         true,
			Priority:        1,
			AccountKind:     model.AccountKindOAuth,
			AccountPlatform: model.AccountPlatformAntigravity,
		}})

	body := fmt.Sprintf(`{"model":%q,"input":"hi","stream":true}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIResponse, env.userID, body)

	mu.Lock()
	got := append([]string(nil), paths...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("没有发出上游请求, 响应 code=%d body=%s", w.Code, w.Body.String())
	}
	for _, p := range got {
		if strings.Contains(p, "/v1/responses") || strings.Contains(p, "chat/completions") || strings.Contains(p, "/v1/messages") {
			t.Fatalf("订阅渠道的 Responses 入站被打到 %s (paths=%v, code=%d): 协议位未收敛到 Gemini CLI",
				p, got, w.Code)
		}
	}
}
