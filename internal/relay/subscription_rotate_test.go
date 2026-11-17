package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
)

// TestSubscriptionChannelRotatesAccounts 是 S4 死代码修复的验收测试。
//
// 它不看 SelectChannelKey 的返回值(那个函数本来就对), 而是看转发真正发出去的凭据:
// 同一订阅渠道挂两个账号, 连续转发两次, 上游必须收到两把不同的 Bearer。
//
// 为什么必须端到端: S4 修的是 handler 里的分支判断。若只测 SelectChannelKey,
// 把 handler 改回死代码状态测试依然全绿 —— 那证明不了修复生效。
func TestSubscriptionChannelRotatesAccounts(t *testing.T) {
	var mu sync.Mutex
	received := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()

	// 两个订阅账号, 优先级逐个递增; 授权只会绑第一个(见 grantKeyName)。
	keySpecs := []model.ChannelKeyConfig{
		{Name: "account-1", Key: "token-one", Enabled: true, Priority: 1},
		{Name: "account-2", Key: "token-two", Enabled: true, Priority: 2},
	}
	env := newRelayGroupDialect(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover), model.DialectSubscription, keySpecs)

	var dbKeys []model.ChannelKey
	if derr := db.GetDB().Find(&dbKeys).Error; derr != nil {
		t.Logf("read keys err: %v", derr)
	}
	for _, k := range dbKeys {
		t.Logf("key id=%d name=%s enabled=%v priority=%d kind=%s", k.ID, k.Name, k.Enabled, k.Priority, k.AccountKind)
	}
	if kk, kerr := op.SelectChannelKey(1, "roundrobin"); kerr != nil {
		t.Logf("SelectChannelKey err: %v", kerr)
	} else {
		t.Logf("SelectChannelKey ok id=%d", kk.ID)
	}

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	for i := 0; i < 2; i++ {
		w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次转发失败: code=%d body=%s", i, w.Code, w.Body.String())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("上游应收到 2 次请求, 实际 %d 次: %v", len(received), received)
	}
	// 核心断言: 两次用的必须是不同账号。死代码状态下这里会是同一个。
	if received[0] == received[1] {
		t.Fatalf("订阅渠道多账号未轮换, 两次都用 %q —— SelectChannelKey 仍是死代码", received[0])
	}
	for i, auth := range received {
		if !strings.Contains(auth, "token-one") && !strings.Contains(auth, "token-two") {
			t.Errorf("第 %d 次用的凭据不属于该渠道: %q", i, auth)
		}
	}
}

// TestGenericChannelKeepsGrantBoundKey 锁定向后兼容: 非订阅渠道仍然只用授权绑定的那把。
//
// 这是 S4 的边界条件。修法 A 的关键承诺是"现有 25 个渠道行为零变化",
// 若这条也轮换起来, 说明改动漏进了普通渠道, 那些渠道的既有多账号隔离会被打乱。
func TestGenericChannelKeepsGrantBoundKey(t *testing.T) {
	var mu sync.Mutex
	received := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()

	// 同样是两个账号, 但方言是 generic: 必须始终用授权绑定的 account-1。
	keySpecs := []model.ChannelKeyConfig{
		{Name: "account-1", Key: "token-one", Enabled: true, Priority: 1},
		{Name: "account-2", Key: "token-two", Enabled: true, Priority: 2},
	}
	env := newRelayGroupDialect(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover), model.DialectGeneric, keySpecs)

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	for i := 0; i < 2; i++ {
		w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次转发失败: code=%d body=%s", i, w.Code, w.Body.String())
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("上游应收到 2 次请求, 实际 %d 次", len(received))
	}
	if received[0] != received[1] {
		t.Fatalf("普通渠道不应轮换凭据, 但两次分别是 %q 与 %q", received[0], received[1])
	}
	if !strings.Contains(received[0], "token-one") {
		t.Fatalf("普通渠道应使用授权绑定的 account-1, 实际 %q", received[0])
	}
}
