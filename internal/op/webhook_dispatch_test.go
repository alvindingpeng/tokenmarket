package op

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

const webhookTestSecret = "signing-secret-0123456789abcd"

// webhookDispatchTestSetup 同 webhookTestSetup, 并显式放行私网拨号:
// httptest 服务监听环回, 只在测试里替换 webhookDialAllowPrivate(默认恒 false, 全拒);
// 生产代码没有任何入口能改写该判定。
func webhookDispatchTestSetup(t *testing.T) {
	t.Helper()
	webhookTestSetup(t)
	prev := webhookDialAllowPrivate
	webhookDialAllowPrivate = func() bool { return true }
	t.Cleanup(func() { webhookDialAllowPrivate = prev })
}

// webhookSeedHook 直接种一行订阅(绕过 URL 保存校验, 便于指向 httptest 环回服务);
// 保存校验本身由 TestWebhookValidateURL 覆盖。
func webhookSeedHook(t *testing.T, userID uint, rawURL, secret string, events []string, retry int) model.Webhook {
	t.Helper()
	encoded, err := model.WebhookEncodeEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	hook := model.Webhook{
		UserID:     userID,
		URL:        rawURL,
		Events:     encoded,
		Enabled:    true,
		RetryCount: retry,
		Version:    1,
		LastStatus: model.WebhookLastStatusNone,
	}
	if err := db.GetDB().Select("UserID", "URL", "Events", "Enabled", "RetryCount", "Version", "LastStatus").
		Create(&hook).Error; err != nil {
		t.Fatal(err)
	}
	sealed, err := webhookSealSecret(hook.ID, userID, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&model.Webhook{}).Where("id = ?", hook.ID).
		Update("secret_encrypted", sealed).Error; err != nil {
		t.Fatal(err)
	}
	hook.SecretEncrypted = sealed
	return hook
}

// webhookSeedDelivery 直接种一条待投递记录。
func webhookSeedDelivery(t *testing.T, hook model.Webhook, event string) model.WebhookDelivery {
	t.Helper()
	var deliveryID string
	if err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var err error
		deliveryID, err = webhookEnqueueTx(tx, hook, event, 0, model.WebhookPayloadData{
			Title: "title", Content: "content", Link: "/link", RelatedID: 7,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var d model.WebhookDelivery
	if err := db.GetDB().Where("delivery_id = ?", deliveryID).First(&d).Error; err != nil {
		t.Fatal(err)
	}
	return d
}

// webhookLoadDelivery 读一条投递(新变量, 避开 GORM dest 复用坑)。
func webhookLoadDelivery(t *testing.T, id uint64) model.WebhookDelivery {
	t.Helper()
	var d model.WebhookDelivery
	if err := db.GetDB().Where("id = ?", id).First(&d).Error; err != nil {
		t.Fatal(err)
	}
	return d
}

// webhookDueNow 把投递的下次尝试时间拉回当前(模拟退避到期)。
func webhookDueNow(t *testing.T, id uint64) {
	t.Helper()
	if err := db.GetDB().Model(&model.WebhookDelivery{}).Where("id = ?", id).
		Update("next_attempt_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
}

type webhookCaptured struct {
	header http.Header
	body   []byte
}

// webhookCaptureServer 记录全部到达的请求(头 + 原始 body)。
func webhookCaptureServer(t *testing.T, respond func(i int, w http.ResponseWriter)) (*httptest.Server, *[]webhookCaptured, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var captured []webhookCaptured
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = append(captured, webhookCaptured{header: r.Header.Clone(), body: body})
		i := len(captured)
		mu.Unlock()
		if respond != nil {
			respond(i, w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, &captured, &mu
}

// 签名与载荷: 标准头三件套 + 可复算 HMAC + 载荷只含用户自己的通知字段。
func TestWebhookDispatchSignsAndDeliversPayload(t *testing.T) {
	webhookDispatchTestSetup(t)
	server, captured, mu := webhookCaptureServer(t, nil)

	hook := webhookSeedHook(t, 42, server.URL+"/hook?token=abc", webhookTestSecret, []string{"system"}, 2)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))

	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("requests = %d, want 1", len(*captured))
	}
	req := (*captured)[0]
	if got := req.header.Get("X-Switchyard-Delivery-ID"); got != d.DeliveryID {
		t.Fatalf("delivery id header = %q, want %q", got, d.DeliveryID)
	}
	ts, err := strconv.ParseInt(req.header.Get("X-Switchyard-Timestamp"), 10, 64)
	if err != nil {
		t.Fatalf("timestamp header: %v", err)
	}
	// 签名可复算: HMAC-SHA256(secret, timestamp + "." + rawBody)。
	mac := hmac.New(sha256.New, []byte(webhookTestSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(req.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := req.header.Get("X-Switchyard-Signature"); got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if ct := req.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}

	var payload model.WebhookPayload
	if err := json.Unmarshal(req.body, &payload); err != nil {
		t.Fatalf("payload decode: %v (%s)", err, req.body)
	}
	if payload.ID != d.DeliveryID || payload.Event != string(model.NotificationTypeSystem) ||
		payload.UserID != 42 || payload.Timestamp != ts {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Data.Title != "title" || payload.Data.Content != "content" ||
		payload.Data.Link != "/link" || payload.Data.RelatedID != 7 {
		t.Fatalf("payload data = %+v", payload.Data)
	}
	// 载荷只有约定字段: 不带请求原文/API Key/密钥。
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(req.body, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "event", "timestamp", "user_id", "data"} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("payload missing %q: %s", key, req.body)
		}
	}
	if len(keys) != 5 {
		t.Fatalf("payload has extra keys: %s", req.body)
	}
	for _, forbidden := range []string{"api_key", "apikey", "secret", "token"} {
		if strings.Contains(strings.ToLower(string(req.body)), forbidden) {
			t.Fatalf("payload leaks %q: %s", forbidden, req.body)
		}
	}

	final := webhookLoadDelivery(t, d.ID)
	if final.Status != model.WebhookDeliveryStatusSucceeded || final.Attempts != 1 || final.LastStatusCode != 200 {
		t.Fatalf("delivery = %+v", final)
	}
	var hookRow model.Webhook
	if err := db.GetDB().First(&hookRow, hook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if hookRow.LastStatus != model.WebhookDeliveryStatusSucceeded {
		t.Fatalf("hook last_status = %s", hookRow.LastStatus)
	}
}

// 重试策略: 5xx/429/网络错误可重试(指数退避、不超 retry_count); 其它 4xx 一次终结;
// delivery_id 与 body 重试间不变(至少一次 + 接收端可去重)。
func TestWebhookRetryPolicy(t *testing.T) {
	t.Run("5xx retries then succeeds with stable delivery id", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
			if i < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 2)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))

		for round := 0; round < 3; round++ {
			if err := WebhookDispatchRun(context.Background(), 10); err != nil {
				t.Fatal(err)
			}
			if round < 2 {
				mid := webhookLoadDelivery(t, d.ID)
				if mid.Status != model.WebhookDeliveryStatusPending {
					t.Fatalf("round %d status = %s, want pending", round, mid.Status)
				}
				if !mid.NextAttemptAt.After(time.Now()) {
					t.Fatalf("round %d next_attempt_at not scheduled in future", round)
				}
				webhookDueNow(t, d.ID)
			}
		}
		final := webhookLoadDelivery(t, d.ID)
		if final.Status != model.WebhookDeliveryStatusSucceeded || final.Attempts != 3 {
			t.Fatalf("delivery = %+v", final)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 3 {
			t.Fatalf("requests = %d, want 3", len(*captured))
		}
		for i, req := range *captured {
			if req.header.Get("X-Switchyard-Delivery-ID") != d.DeliveryID {
				t.Fatalf("request %d delivery id drifted", i)
			}
			if string(req.body) != string((*captured)[0].body) {
				t.Fatalf("request %d body drifted: %s", i, req.body)
			}
		}
	})

	t.Run("429 is retryable", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, _, _ := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
			if i == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 1)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		if got := webhookLoadDelivery(t, d.ID); got.Status != model.WebhookDeliveryStatusPending || got.Attempts != 1 {
			t.Fatalf("after 429: %+v", got)
		}
		webhookDueNow(t, d.ID)
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		if got := webhookLoadDelivery(t, d.ID); got.Status != model.WebhookDeliveryStatusSucceeded || got.Attempts != 2 {
			t.Fatalf("after recovery: %+v", got)
		}
	})

	t.Run("4xx terminates immediately", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("nope"))
		})
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 3)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		got := webhookLoadDelivery(t, d.ID)
		if got.Status != model.WebhookDeliveryStatusFailed || got.Attempts != 1 || got.LastStatusCode != 400 {
			t.Fatalf("delivery = %+v", got)
		}
		// 不再重试。
		webhookDueNow(t, d.ID)
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 1 {
			t.Fatalf("requests = %d, want 1", len(*captured))
		}
	})

	t.Run("retry budget bounded by retry_count", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 1)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		for round := 0; round < 4; round++ {
			if err := WebhookDispatchRun(context.Background(), 10); err != nil {
				t.Fatal(err)
			}
			if webhookLoadDelivery(t, d.ID).Status == model.WebhookDeliveryStatusPending {
				webhookDueNow(t, d.ID)
			}
		}
		got := webhookLoadDelivery(t, d.ID)
		// retry_count=1 => 总尝试 2 次后 failed。
		if got.Status != model.WebhookDeliveryStatusFailed || got.Attempts != 2 {
			t.Fatalf("delivery = %+v", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 2 {
			t.Fatalf("requests = %d, want 2", len(*captured))
		}
	})

	t.Run("backoff bounded", func(t *testing.T) {
		if webhookBackoff(1) != WebhookRetryBaseDelay {
			t.Fatalf("first backoff = %v", webhookBackoff(1))
		}
		if webhookBackoff(2) != 2*WebhookRetryBaseDelay {
			t.Fatalf("second backoff = %v", webhookBackoff(2))
		}
		for _, attempts := range []int{1, 2, 3, 10, 100} {
			if got := webhookBackoff(attempts); got > WebhookRetryMaxDelay {
				t.Fatalf("backoff(%d) = %v exceeds cap", attempts, got)
			}
		}
	})
}

// 不跟随重定向: 3xx 一次终结, 重定向目标收不到任何请求。
func TestWebhookNoRedirectFollowed(t *testing.T) {
	webhookDispatchTestSetup(t)
	var targetMu sync.Mutex
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetMu.Lock()
		targetHits++
		targetMu.Unlock()
	}))
	defer target.Close()

	server, captured, mu := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
		w.Header().Set("Location", target.URL+"/internal")
		w.WriteHeader(http.StatusFound)
	})
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 3)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusFailed || got.Attempts != 1 {
		t.Fatalf("delivery = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("source requests = %d, want 1", len(*captured))
	}
	targetMu.Lock()
	defer targetMu.Unlock()
	if targetHits != 0 {
		t.Fatalf("redirect target hit %d times, want 0", targetHits)
	}
}

// 忽略环境代理: HTTP(S)_PROXY 指向的代理一个连接都拿不到, 请求直连目标。
func TestWebhookEnvProxyIgnored(t *testing.T) {
	webhookDispatchTestSetup(t)
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyLn.Close()
	var proxyMu sync.Mutex
	proxyConns := 0
	go func() {
		for {
			conn, err := proxyLn.Accept()
			if err != nil {
				return
			}
			proxyMu.Lock()
			proxyConns++
			proxyMu.Unlock()
			conn.Close()
		}
	}()
	t.Setenv("HTTP_PROXY", "http://"+proxyLn.Addr().String())
	t.Setenv("HTTPS_PROXY", "http://"+proxyLn.Addr().String())
	t.Setenv("http_proxy", "http://"+proxyLn.Addr().String())
	t.Setenv("https_proxy", "http://"+proxyLn.Addr().String())

	server, captured, mu := webhookCaptureServer(t, nil)
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 0)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if got := webhookLoadDelivery(t, d.ID); got.Status != model.WebhookDeliveryStatusSucceeded {
		t.Fatalf("delivery = %+v", got)
	}
	mu.Lock()
	hits := len(*captured)
	mu.Unlock()
	if hits != 1 {
		t.Fatalf("target hits = %d, want 1", hits)
	}
	proxyMu.Lock()
	defer proxyMu.Unlock()
	if proxyConns != 0 {
		t.Fatalf("proxy saw %d connections, want 0", proxyConns)
	}
}

// 拨号层防内网: 默认(非测试放行)下环回目标被 DialGuard 拦截, 请求根本到不了服务。
func TestWebhookDialBlocksPrivateTargets(t *testing.T) {
	webhookTestSetup(t) // 不放行私网: webhookDialAllowPrivate 保持生产默认(恒 false)
	server, captured, mu := webhookCaptureServer(t, nil)
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 0)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusFailed {
		t.Fatalf("delivery = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 0 {
		t.Fatalf("private target received %d requests, want 0", len(*captured))
	}
}

// 超时按网络错误处理(可重试), 单次网络尝试有 5 秒上限(测试用短超时加速)。
func TestWebhookTimeoutRetryable(t *testing.T) {
	webhookDispatchTestSetup(t)
	prevTimeout := webhookHTTPTimeout
	webhookHTTPTimeout = 150 * time.Millisecond
	t.Cleanup(func() { webhookHTTPTimeout = prevTimeout })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 1)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusPending || got.Attempts != 1 || got.LastStatusCode != 0 {
		t.Fatalf("delivery = %+v", got)
	}
}



// 生产客户端硬化: 无代理(环境代理无效)、不跟随重定向、拨号判定是显式 false ——
// DialGuard 对 AllowPrivate == nil 的语义是放行, 判定函数绝不能缺位。
func TestWebhookHTTPClientHardening(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	client := webhookHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("proxy must be hard-disabled (Proxy == nil); env proxies must not apply")
	}
	req, err := http.NewRequest(http.MethodGet, "https://hooks.example.com/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, nil); err != http.ErrUseLastResponse {
		t.Fatalf("CheckRedirect = %v, want ErrUseLastResponse (no redirect following)", err)
	}
	// 判定必须是显式 false 的函数: 传 nil 会被 DialGuard 解释成"放行私网"。
	if webhookDialAllowPrivate == nil || webhookDialAllowPrivate() {
		t.Fatal("production dial allow-private must be an explicit func returning false")
	}
	// 不复用管理员「允许私网」设置: 该判定与设置层零耦合(无任何读取路径), 管理员例外只属于上游渠道。
}

// 崩溃恢复: lease 过期的 processing 会被重新抢占补投(至少一次); lease 未到期的不抢。
func TestWebhookStaleLeaseReclaimed(t *testing.T) {
	webhookDispatchTestSetup(t)
	server, captured, mu := webhookCaptureServer(t, nil)
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 2)

	// A: lease 已过期的 processing(模拟 worker 崩溃)。
	a := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	past := time.Now().Add(-2 * WebhookLeaseDuration)
	if err := db.GetDB().Model(&model.WebhookDelivery{}).Where("id = ?", a.ID).
		Updates(map[string]interface{}{"status": model.WebhookDeliveryStatusProcessing, "attempts": 1, "lease_expires_at": past}).Error; err != nil {
		t.Fatal(err)
	}
	// B: lease 仍在租期内的 processing, 不应被抢。
	b := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	future := time.Now().Add(WebhookLeaseDuration)
	if err := db.GetDB().Model(&model.WebhookDelivery{}).Where("id = ?", b.ID).
		Updates(map[string]interface{}{"status": model.WebhookDeliveryStatusProcessing, "attempts": 1, "lease_expires_at": future}).Error; err != nil {
		t.Fatal(err)
	}

	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	gotA := webhookLoadDelivery(t, a.ID)
	if gotA.Status != model.WebhookDeliveryStatusSucceeded || gotA.Attempts != 2 {
		t.Fatalf("stale lease delivery = %+v", gotA)
	}
	gotB := webhookLoadDelivery(t, b.ID)
	if gotB.Status != model.WebhookDeliveryStatusProcessing || gotB.Attempts != 1 {
		t.Fatalf("live lease delivery = %+v", gotB)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("requests = %d, want 1", len(*captured))
	}
}

// 并发抢占: 同一条投递只可能有一个 worker 拿到(条件更新 + attempts 乐观锁)。
func TestWebhookConcurrentClaimSingleOwner(t *testing.T) {
	webhookDispatchTestSetup(t)
	hook := webhookSeedHook(t, 1, "https://hooks.example.com/x", webhookTestSecret, []string{"system"}, 2)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))

	const workers = 8
	owned := make(chan bool, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, got, err := webhookClaimDelivery(context.Background(), d.ID)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			owned <- got
		}()
	}
	close(start)
	wg.Wait()
	close(owned)
	count := 0
	for got := range owned {
		if got {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("claims won = %d, want 1", count)
	}
}

// 并发投递: 多 worker 同时处理同一批, 每条投递只发出一次网络请求(不重复发)。
func TestWebhookConcurrentDispatchSingleSend(t *testing.T) {
	webhookDispatchTestSetup(t)
	server, captured, mu := webhookCaptureServer(t, nil)
	hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 0)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))

	const workers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			webhookDispatchOne(context.Background(), d.ID)
		}()
	}
	close(start)
	wg.Wait()

	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusSucceeded || got.Attempts != 1 {
		t.Fatalf("delivery = %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("requests = %d, want 1", len(*captured))
	}
}

// 投递前版本检查(第二道闸): 订阅改绑/禁用后, 旧投递一律取消, 不发往任何地址。
func TestWebhookDispatchVersionAndEnabledGating(t *testing.T) {
	t.Run("stale version cancels delivery", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, nil)
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 2)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		// 模拟"取消没来得及生效"的竞争窗口: 直接改版本, 不动投递。
		if err := db.GetDB().Model(&model.Webhook{}).Where("id = ?", hook.ID).
			Update("version", hook.Version+1).Error; err != nil {
			t.Fatal(err)
		}
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		if got := webhookLoadDelivery(t, d.ID); got.Status != model.WebhookDeliveryStatusCancelled {
			t.Fatalf("delivery = %+v", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 0 {
			t.Fatalf("requests = %d, want 0", len(*captured))
		}
	})

	t.Run("disabled hook cancels normal events but allows explicit test", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, nil)
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 2)
		normal := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		test := webhookSeedDelivery(t, hook, model.WebhookEventTest)
		if err := db.GetDB().Model(&model.Webhook{}).Where("id = ?", hook.ID).
			Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		if got := webhookLoadDelivery(t, normal.ID); got.Status != model.WebhookDeliveryStatusCancelled {
			t.Fatalf("normal delivery = %+v", got)
		}
		if got := webhookLoadDelivery(t, test.ID); got.Status != model.WebhookDeliveryStatusSucceeded {
			t.Fatalf("test delivery = %+v", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 1 {
			t.Fatalf("requests = %d, want 1", len(*captured))
		}
	})

	t.Run("deleted hook cancels residual deliveries", func(t *testing.T) {
		webhookDispatchTestSetup(t)
		server, captured, mu := webhookCaptureServer(t, nil)
		hook := webhookSeedHook(t, 1, server.URL, webhookTestSecret, []string{"system"}, 2)
		d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
		if err := db.GetDB().Where("id = ?", hook.ID).Delete(&model.Webhook{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := WebhookDispatchRun(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		if got := webhookLoadDelivery(t, d.ID); got.Status != model.WebhookDeliveryStatusCancelled {
			t.Fatalf("delivery = %+v", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*captured) != 0 {
			t.Fatalf("requests = %d, want 0", len(*captured))
		}
	})
}

// 容量控制: 终态日志超保留期删除、每订阅只留最近 N 条; 非终态积压超上限取消最旧。
func TestWebhookCleanupRetentionAndCaps(t *testing.T) {
	webhookDispatchTestSetup(t)
	hook := webhookSeedHook(t, 1, "https://hooks.example.com/x", webhookTestSecret, []string{"system"}, 2)

	seed := func(status string, n int) []uint64 {
		t.Helper()
		rows := make([]model.WebhookDelivery, 0, n)
		for i := 0; i < n; i++ {
			rows = append(rows, model.WebhookDelivery{
				DeliveryID: model.WebhookEventTest + "-" + status + "-" + strconv.Itoa(i) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
				WebhookID:  hook.ID,
				UserID:     hook.UserID,
				HookVersion: hook.Version,
				Event:      model.WebhookEventTest,
				Payload:    "{}",
				Status:     status,
			})
		}
		if err := db.GetDB().CreateInBatches(&rows, 200).Error; err != nil {
			t.Fatal(err)
		}
		ids := make([]uint64, 0, n)
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids
	}

	// 1. 超保留期的终态记录删除。
	old := seed(model.WebhookDeliveryStatusSucceeded, 3)
	oldest := time.Now().Add(-2 * WebhookLogRetention)
	if err := db.GetDB().Exec("UPDATE webhook_deliveries SET updated_at = ? WHERE id IN ?", oldest, old).Error; err != nil {
		t.Fatal(err)
	}
	// 2. 终态容量: 塞 WebhookLogMaxPerHook + 30 条, 清理后只留最新 WebhookLogMaxPerHook 条。
	fresh := seed(model.WebhookDeliveryStatusFailed, WebhookLogMaxPerHook+30)

	if err := WebhookCleanupRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.GetDB().Model(&model.WebhookDelivery{}).
		Where("webhook_id = ? AND status IN ?", hook.ID, webhookTerminalStatuses).Count(&count)
	if count != WebhookLogMaxPerHook {
		t.Fatalf("terminal rows = %d, want %d", count, WebhookLogMaxPerHook)
	}
	var gone int64
	db.GetDB().Model(&model.WebhookDelivery{}).Where("id IN ?", old).Count(&gone)
	if gone != 0 {
		t.Fatalf("expired rows survived = %d", gone)
	}
	// 留下的是最新的: fresh 尾部(WebhookLogMaxPerHook 条)应在。
	var survived int64
	db.GetDB().Model(&model.WebhookDelivery{}).Where("id IN ?", fresh[30:]).Count(&survived)
	if survived != WebhookLogMaxPerHook {
		t.Fatalf("newest rows survived = %d, want %d", survived, WebhookLogMaxPerHook)
	}

	// 3. 非终态积压超上限: 取消最旧。
	pending := seed(model.WebhookDeliveryStatusPending, WebhookPendingMaxPerHook+10)
	if err := WebhookCleanupRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pendingLeft int64
	db.GetDB().Model(&model.WebhookDelivery{}).
		Where("webhook_id = ? AND status = ?", hook.ID, model.WebhookDeliveryStatusPending).Count(&pendingLeft)
	if pendingLeft != WebhookPendingMaxPerHook {
		t.Fatalf("pending left = %d, want %d", pendingLeft, WebhookPendingMaxPerHook)
	}
	var cancelledOldest model.WebhookDelivery
	if err := db.GetDB().Where("id = ?", pending[0]).First(&cancelledOldest).Error; err != nil {
		t.Fatal(err)
	}
	if cancelledOldest.Status != model.WebhookDeliveryStatusCancelled {
		t.Fatalf("oldest pending = %s, want cancelled", cancelledOldest.Status)
	}
	var newestStillPending model.WebhookDelivery
	if err := db.GetDB().Where("id = ?", pending[len(pending)-1]).First(&newestStillPending).Error; err != nil {
		t.Fatal(err)
	}
	if newestStillPending.Status != model.WebhookDeliveryStatusPending {
		t.Fatalf("newest pending = %s, want pending", newestStillPending.Status)
	}
}

// 错误摘要脱敏: 不落 URL(token)、不落响应正文(防泄露与 SSRF 回显)。
func TestWebhookErrorLogSanitized(t *testing.T) {
	webhookDispatchTestSetup(t)
	const marker = "internal-ssrf-response-marker"
	server, _, _ := webhookCaptureServer(t, func(i int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, marker+" at http://169.254.169.254/latest/meta-data")
	})
	rawURL := server.URL + "/hook?token=super-secret-token"
	hook := webhookSeedHook(t, 1, rawURL, webhookTestSecret, []string{"system"}, 0)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusFailed {
		t.Fatalf("delivery = %+v", got)
	}
	for _, forbidden := range []string{marker, "super-secret-token", rawURL, "169.254.169.254", "://"} {
		if strings.Contains(got.LastError, forbidden) {
			t.Fatalf("last_error leaks %q: %q", forbidden, got.LastError)
		}
	}
	if !strings.Contains(got.LastError, "endpoint responded 500") {
		t.Fatalf("last_error = %q", got.LastError)
	}
}

// 网络错误的脱敏: dial 错误内嵌完整 URL, 落库前必须抹掉(scrub)。
func TestWebhookNetworkErrorSanitized(t *testing.T) {
	webhookDispatchTestSetup(t)
	rawURL := "http://127.0.0.1:1/hook?token=leaked-token-value"
	hook := webhookSeedHook(t, 1, rawURL, webhookTestSecret, []string{"system"}, 0)
	d := webhookSeedDelivery(t, hook, string(model.NotificationTypeSystem))
	if err := WebhookDispatchRun(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	got := webhookLoadDelivery(t, d.ID)
	if got.Status != model.WebhookDeliveryStatusFailed {
		t.Fatalf("delivery = %+v", got)
	}
	for _, forbidden := range []string{"leaked-token-value", rawURL, "://"} {
		if strings.Contains(got.LastError, forbidden) {
			t.Fatalf("last_error leaks %q: %q", forbidden, got.LastError)
		}
	}
}
