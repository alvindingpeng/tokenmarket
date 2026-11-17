package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm"
)

// TestRateLimitedRetryBacksOffAndRecovers 是 429 退避的端到端证明。
//
// 场景: 单账号订阅渠道(只有一个成员), 上游对前两次请求返回真实 429, 第三次正常。
// 这是线上最常见形态: 上游窗口短暂打满, 几秒后自愈。
//
// 旧实现(裸 continue)在约 2 秒内把 MaxTotalAttempts 全部砸向已满的窗口, 请求必然
// 以 503 "maximum total attempts exceeded" 告终 —— 与用户看到的症状一致。
// 新实现退避后重试, 应能在上游恢复后拿到 200。
func TestRateLimitedRetryBacksOffAndRecovers(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			// 真实上游 429 报文形状: 网关据 status 分类为 rate。
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	cfg := model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)
	// 退避基数压到 1 秒(默认值), 保证测试耗时可控且仍能跨过上游的两次限流。
	cfg.MemberRetryIntervalSeconds = 1
	cfg.MaxTotalAttempts = 10
	env := newRelayGroupDialect(t, upstream.URL,
		model.ProtocolGeminiCLI, model.MediaKindText, 1, cfg, model.DialectSubscription,
		[]model.ChannelKeyConfig{{
			Name:            "antigravity",
			Key:             `{"access_token":"a.b.c","refresh_token":"rt"}`,
			Enabled:         true,
			Priority:        1,
			AccountKind:     model.AccountKindOAuth,
			AccountPlatform: model.AccountPlatformAntigravity,
		}})

	body := fmt.Sprintf(`{"model":%q,"input":"hi","stream":true}`, env.group)
	start := time.Now()
	w := forwardRequest(llm.APIFormatOpenAIResponse, env.userID, body)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("上游在两次 429 后恢复, 网关应退避重试并成功; got code=%d body=%s calls=%d",
			w.Code, w.Body.String(), calls.Load())
	}
	if calls.Load() != 3 {
		t.Fatalf("应当恰好打 3 次上游(2 次 429 + 1 次成功), got %d", calls.Load())
	}
	// 退避必须真的等过: 第 1 次退避 1s(基数 1s), 总耗时不可能低于这个量级。
	if elapsed < 900*time.Millisecond {
		t.Fatalf("两次 429 之间必须退避等待(基数 1s), 实际总耗时仅 %v —— 说明又退回猛打上游", elapsed)
	}
}

// TestRateLimitedRetryGivesUpAfterBudget 是上一条的边界: 上游一直 429 时网关不能无限重试。
//
// 必须"快速失败并透传真实 429", 而不是把用户挂到请求超时(默认 600s)。
func TestRateLimitedRetryGivesUpAfterBudget(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer upstream.Close()

	cfg := model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)
	cfg.MemberRetryIntervalSeconds = 1
	cfg.MaxTotalAttempts = 10
	env := newRelayGroupDialect(t, upstream.URL,
		model.ProtocolGeminiCLI, model.MediaKindText, 1, cfg, model.DialectSubscription,
		[]model.ChannelKeyConfig{{
			Name:            "antigravity",
			Key:             `{"access_token":"a.b.c","refresh_token":"rt"}`,
			Enabled:         true,
			Priority:        1,
			AccountKind:     model.AccountKindOAuth,
			AccountPlatform: model.AccountPlatformAntigravity,
		}})

	body := fmt.Sprintf(`{"model":%q,"input":"hi","stream":true}`, env.group)
	start := time.Now()
	w := forwardRequest(llm.APIFormatOpenAIResponse, env.userID, body)
	elapsed := time.Since(start)

	// 上限 3 次退避: 1s + 2s + 4s = 7s, 给足余量但仍远小于请求超时。
	if elapsed > 30*time.Second {
		t.Fatalf("一直 429 时必须快速失败而不是挂到超时, 实际耗时 %v", elapsed)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("持续 429 应透传限流错误(429), got code=%d body=%s", w.Code, w.Body.String())
	}
	// 初次 + 3 次退避重试 = 4 次上游调用; 不该出现 10 次猛打。
	if got := calls.Load(); got > 5 {
		t.Fatalf("持续 429 不应猛打上游: 实际打了 %d 次(期望 ≤5: 首轮+3 次退避重试)", got)
	}
}

// TestRateRetryDoesNotConsumeTotalAttemptBudget 钉住"429 退避重试不计入 MaxTotalAttempts"。
//
// 为什么必须分开计数: 429 代表请求根本没被模型处理(与准入拒绝同性质)。若照计,
// MaxTotalAttempts 收紧到接近 1 时, 一次上游瞬时 429 就会把预算用在"没打到模型"的轮次上,
// 退避反而更快撞到 "relay maximum total attempts exceeded" —— 比不修还差。
// 线上单账号订阅渠道正是紧预算场景(总尝试 10、成员每次 1), 上限一收紧就会复发。
func TestRateRetryDoesNotConsumeTotalAttemptBudget(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	cfg := model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)
	cfg.MemberRetryIntervalSeconds = 1
	// 预算恰好只够一次真实尝试: 若退避撞进预算, 第二次(本应成功的)轮次就没机会发生。
	cfg.MaxTotalAttempts = 1
	env := newRelayGroupDialect(t, upstream.URL,
		model.ProtocolGeminiCLI, model.MediaKindText, 1, cfg, model.DialectSubscription,
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

	if w.Code != http.StatusOK {
		t.Fatalf("MaxTotalAttempts=1 时, 一次 429 后退避重试仍应发生; got code=%d body=%s calls=%d —— 若这里失败, 说明退避重试被错误地计入了总预算",
			w.Code, w.Body.String(), calls.Load())
	}
	if calls.Load() != 2 {
		t.Fatalf("应当打 2 次上游(1 次 429 + 1 次成功), got %d", calls.Load())
	}
}
