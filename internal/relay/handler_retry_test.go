package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// retry 测试聚焦三条重试安全语义(见 handler.go):
//  1. 生图/生视频的创建请求一旦真正发到上游, 任何失败都不再换目标重发(含 429 与响应超时);
//  2. 请求级取消与上游成功同时到达时, 正文与视频任务归属不丢;
//  3. 上游准入拒绝不消耗跨成员尝试预算, 走等待路径限速而不是空转烧完预算。

// relayRetryTestDB 每个用例独立起一个 sqlite 库, 顺带钉死结算上浮比例, 让费用断言只看媒体价格本身。
// 路由状态与限流桶按主键记账, 而各用例的 sqlite 主键都从头计数; 每个用例装一份干净的
// 路由状态与限流器, 免得上个用例留下的成员冷却与限流触顶串到下个用例(它们的键会撞上)。
func relayRetryTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "relay_retry.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.SettingUpsert(model.SettingKeyMarkupRatio, "0"); err != nil {
		t.Fatalf("seed markup setting: %v", err)
	}
	// httptest 上游都在 127.0.0.1: 打开私网放行, SSRF 防护由 op 层的拒绝用例单独覆盖。
	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("seed allow-private setting: %v", err)
	}
	routeMu.Lock()
	clear(routes)
	routeMu.Unlock()
	original := limiter
	limiter = ratelimit.NewResolved(
		func(scope ratelimit.Scope) ratelimit.Limits {
			rpm, tpm, concurrent := op.RateLimitsFor(scope.Kind, scope.ID, scope.Model)
			return ratelimit.Limits{RPM: rpm, TPM: tpm, Concurrent: concurrent}
		},
		func(scope ratelimit.Scope) []ratelimit.Scope {
			var scopes []ratelimit.Scope
			for _, name := range op.RateScopeModels(scope.Kind, scope.ID, scope.Model) {
				scopes = append(scopes, ratelimit.Scope{Kind: scope.Kind, ID: scope.ID, Model: name})
			}
			return scopes
		},
	)
	t.Cleanup(func() { limiter = original })
}

type relayGroupEnv struct {
	userID   uint
	group    string // 分组名, 即客户端请求的 model 字段。
	grantIDs []int
}

// newRelayGroup 建齐「用户-渠道-凭据-模型-授权-分组」整条链路并预热缓存。
// members 决定分组里放几个成员: 每个成员是独立渠道上的一条授权(独立渠道/凭据/模型),
// 一个成员被 MarkBlocked 或冷却时不会牵连其余成员, 换目标行为才能观察清楚。
func newRelayGroup(t *testing.T, upstreamURL string, protocols model.Protocol, kind model.MediaKind, members int, relayConfig model.GroupRelayConfig) relayGroupEnv {
	t.Helper()
	relayRetryTestDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	username := "u" + suffix
	user, err := op.UserCreate(username, username+"@test.local", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := op.UserSetBalance(user.ID, 1000, user.ID, "test", "seed"); err != nil {
		t.Fatalf("set balance: %v", err)
	}
	scope := model.Scope{ID: user.ID, Role: model.RoleAdmin}

	for i := 0; i < members; i++ {
		modelName := fmt.Sprintf("up-model-%s-%d", suffix, i)
		created, err := op.ChannelCreate(&model.ChannelDetail{
			ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("ch-%s-%d", suffix, i), Enabled: true, BaseURL: upstreamURL},
			Keys:          []model.ChannelKeyConfig{{Name: "k1", Key: fmt.Sprintf("sk-%d", i), Enabled: true}},
			Models:        []string{modelName},
			Grants:        []model.ChannelGrantConfig{{ModelName: modelName, KeyName: "k1", Protocols: protocols}},
		}, scope, ctx)
		if err != nil {
			t.Fatalf("create channel %d: %v", i, err)
		}
		if _, err := op.ChannelPublish(created.ID, true, scope, ctx); err != nil {
			t.Fatalf("publish channel %d: %v", i, err)
		}
		listing := op.ChannelModelListing{Name: modelName, Listed: true, SupplyPrice: model.LLMPrice{Input: 1, Output: 1}, Kind: kind}
		switch kind {
		case model.MediaKindVideo:
			listing.MediaSupply = model.MediaPrice{PerVideo: 1, PerSecond: 0.5}
		case model.MediaKindImage:
			listing.MediaSupply = model.MediaPrice{PerImage: 0.1}
		}
		if err := op.ChannelModelListingUpdate(created.ID, []op.ChannelModelListing{listing}, scope, ctx); err != nil {
			t.Fatalf("list model %d: %v", i, err)
		}
	}

	var grantRows []model.ChannelGrant
	if err := db.GetDB().Order("id").Find(&grantRows).Error; err != nil {
		t.Fatalf("read grants: %v", err)
	}
	if len(grantRows) != members {
		t.Fatalf("want %d grants, got %d", members, len(grantRows))
	}
	items := make([]model.GroupItemInput, 0, len(grantRows))
	grantIDs := make([]int, 0, len(grantRows))
	for _, g := range grantRows {
		items = append(items, model.GroupItemInput{ChannelGrantID: g.ID})
		grantIDs = append(grantIDs, g.ID)
	}
	groupName := "grp-" + suffix
	if _, err := op.GroupCreate(&model.GroupCreateRequest{Name: groupName, Mode: model.GroupModeFailover, RelayConfig: relayConfig, Items: items}, scope, ctx); err != nil {
		t.Fatalf("create group: %v", err)
	}
	return relayGroupEnv{userID: user.ID, group: groupName, grantIDs: grantIDs}
}

// forwardRequest 以真实的 Forward 转发一条请求并返回响应。
// 不依赖 *testing.T, 允许在后台 goroutine 中调用。
func forwardRequest(format llm.APIFormat, userID uint, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	forwardRequestWriter(format, userID, body, w)
	return w
}

// forwardRequestWriter 与 forwardRequest 相同, 但写到调用方给定的 ResponseWriter,
// 用于模拟「一个字节都写不出去」的客户端故障。
func forwardRequestWriter(format llm.APIFormat, userID uint, body string, w http.ResponseWriter) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/relay-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set("api_key_user_id", int(userID))
	c.Set("api_key_id", 0)
	Forward(format)(c)
}

// newUpstreamStub 起一个固定应答的上游并统计收到的 POST 数。
func newUpstreamStub(status int, body string) (*httptest.Server, *atomic.Int64) {
	var posts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	return srv, &posts
}

// limitedAttempts 统计本库(每个用例独占)里记为准入拒绝的尝试行。
func limitedAttempts(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := db.GetDB().Model(&model.RelayRequestAttempt{}).Where("status = ?", "limited").Count(&n).Error; err != nil {
		return 0
	}
	return n
}

// TestMediaCreateSingleShotAfterDispatch 钉住媒体单发: 生视频的创建请求发出后遇上 5xx,
// 不换成员重发 —— 上游可能已经受理了创建, 重发就是重复任务。
func TestMediaCreateSingleShotAfterDispatch(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusInternalServerError, `{"error":{"message":"boom","type":"server_error"}}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIVideo, model.MediaKindVideo, 2,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":"4","size":"1280x720"}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIVideo, env.userID, body)
	if got := posts.Load(); got != 1 {
		t.Fatalf("video creation redispatched after dispatch: %d upstream POSTs, want 1", got)
	}
	if w.Code < 400 {
		t.Fatalf("want the upstream failure surfaced to the client, got status %d", w.Code)
	}
}

// TestMediaCreateSingleShotOnRateLimit 钉住 429 同样不重发: 限流分支原先绕过了媒体保护,
// 会换成员再 POST 一次。
func TestMediaCreateSingleShotOnRateLimit(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded","type":"rate_limit_exceeded"}}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIVideo, model.MediaKindVideo, 2,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":"4","size":"1280x720"}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIVideo, env.userID, body)
	if got := posts.Load(); got != 1 {
		t.Fatalf("video creation redispatched on 429: %d upstream POSTs, want 1", got)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want the 429 surfaced to the client, got status %d", w.Code)
	}
}

// TestTextFailoverStillSwitchesMembers 是媒体单发的对照组: 文本请求没有创建副作用,
// 5xx 之后仍然换成员重试 —— 证明媒体保护没有把普通故障转移一起砍掉。
func TestTextFailoverStillSwitchesMembers(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusInternalServerError, `{"error":{"message":"boom","type":"server_error"}}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body)
	if got := posts.Load(); got != 2 {
		t.Fatalf("text request should try both members before giving up: %d upstream POSTs, want 2 (status %d body %s)", got, w.Code, w.Body.String())
	}
	if w.Code < 400 {
		t.Fatalf("want an error status after both members fail, got %d", w.Code)
	}
}

// TestAdmissionRejectionDoesNotBurnTotalAttempts 钉住准入拒绝不消耗尝试预算:
// 上游限流窗口未满、按估算 token 预留必然被拒(选路侧 Blocked 判不出)的场景下,
// 请求要等到容量回来后成功, 而不是几毫秒内烧光 MaxTotalAttempts 以 503 告终。
func TestAdmissionRejectionDoesNotBurnTotalAttempts(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusOK,
		`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	defer srv.Close()
	relayConfig := model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)
	relayConfig.MaxWaitingRequests = 100 // 等待队列开启: 准入拒绝按等待路径限速。
	relayConfig.MaxWaitSeconds = 30
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, relayConfig)

	admitAll := &atomic.Bool{} // 翻转前 TPM 限额远小于请求估算, 每次预留都会被拒。
	original := limiter
	limiter = ratelimit.New(admissionDenyPolicy(admitAll))
	defer func() { limiter = original }()

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body) }()

	// 等到准入拒绝次数越过 MaxTotalAttempts(默认 10)后放开容量。
	deadline := time.Now().Add(30 * time.Second)
	for limitedAttempts(t) < 12 {
		if time.Now().After(deadline) {
			t.Fatalf("admission rejections stopped at %d, want >= 12 before capacity returns", limitedAttempts(t))
		}
		time.Sleep(25 * time.Millisecond)
	}
	admitAll.Store(true)

	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("request did not finish after upstream capacity returned")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("admission rejections burned the attempt budget: status %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("want exactly 1 upstream POST once admitted, got %d", got)
	}
}

// TestAdmissionRejectionWaitingDisabledRejectsWith429 钉住等待关闭时的拒绝语义:
// 不空转、不烧预算, 按 429 立即回绝(等待队列默认关闭)。
func TestAdmissionRejectionWaitingDisabledRejectsWith429(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusOK,
		`{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	admitAll := &atomic.Bool{} // 永不放容量。
	original := limiter
	limiter = ratelimit.New(admissionDenyPolicy(admitAll))
	defer func() { limiter = original }()

	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 when waiting is disabled, got status %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 0 {
		t.Fatalf("admission was rejected yet %d upstream POSTs went out", got)
	}
}

// admissionDenyPolicy 用户维度不设限; 上游维度在放开前给一个远小于请求估算的 TPM 限额:
// 当前用量进不了触顶状态(Blocked 为假), 但按估算 token 的预留必然被拒。
func admissionDenyPolicy(admitAll *atomic.Bool) func(ratelimit.Scope) ratelimit.Limits {
	return func(s ratelimit.Scope) ratelimit.Limits {
		if s.Kind == model.RateScopeUser || s.Kind == model.RateScopeAPIKey {
			return ratelimit.Limits{}
		}
		if admitAll.Load() {
			return ratelimit.Limits{}
		}
		return ratelimit.Limits{TPM: 100}
	}
}

// TestFinalizeCanceledSuccessKeepsBodyAndBillsMedia 钉住「取消与成功同时到达」的收尾:
// 尝试行只按取消定稿一次, 上游正文随终态保留, 视频任务归属与媒体计费都不丢。
func TestFinalizeCanceledSuccessKeepsBodyAndBillsMedia(t *testing.T) {
	relayRetryTestDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	username := "u" + suffix
	user, err := op.UserCreate(username, username+"@test.local", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := op.UserSetBalance(user.ID, 1000, user.ID, "test", "seed"); err != nil {
		t.Fatalf("set balance: %v", err)
	}

	request := newRequestState(ctx, "grp-cancel", "", 7, model.ProtocolOpenAIVideo,
		`{"model":"grp-cancel","prompt":"p"}`, 0, user.ID, "127.0.0.1", "", 600)
	// 一轮真实的上游尝试: 计费归属(渠道发布者)随 startRound 登记, 结算要给它记收入。
	request.startRound(func() {}, "ch · k1", "up-model", model.ProtocolOpenAIVideo, 3, user.ID, "share-1", model.LLMPrice{Input: 1, Output: 1}, 5)
	request.setMediaTarget(model.MediaKindVideo, model.MediaPrice{PerVideo: 1, PerSecond: 0.5}, 42)
	request.setRequestedMedia(model.MediaUnits{Seconds: 4, Resolution: "720p"})
	reservationID, err := op.BillingReserve(user.ID, request.ID, 5)
	if err != nil {
		t.Fatalf("reserve balance: %v", err)
	}
	request.SetReservation(reservationID)

	// 上游成功返回了任务对象, 但请求级超时与它同时到达。
	result := &upstreamResponse{body: []byte(`{"id":"video_task_1","status":"queued","seconds":"4","size":"1280x720"}`)}
	finalizeCanceledSuccess(request, result, context.DeadlineExceeded)

	if request.Status != StatusFailed {
		t.Fatalf("want failed(超时)终态, got %q", request.Status)
	}
	if want := string(result.body); request.responseBody != want {
		t.Fatalf("cancel-after-success dropped the upstream body: got %q want %q", request.responseBody, want)
	}
	if _, err := op.VideoJobGet("video_task_1", user.ID); err != nil {
		t.Fatalf("video job ownership was not recorded: %v", err)
	}
	var record model.BillingRecord
	if err := db.GetDB().Where("request_id = ?", request.ID).First(&record).Error; err != nil {
		t.Fatalf("no billing record settled for the delivered creation: %v", err)
	}
	if record.MediaUnits.Videos != 1 || record.MediaUnits.Seconds != 4 {
		t.Fatalf("media units not billed from the preserved body: %+v", record.MediaUnits)
	}
	if record.UserCost <= 0 {
		t.Fatalf("billed cost should cover the video creation, got %.6f", record.UserCost)
	}
}

// TestZeroCapTokensStillReserves 钉住 capTokens 的零值处理: 余额预扣上限设置为 0(或缺失)时
// 必须回落到默认 4096 —— 按 0 计算会让请求在不冻结任何费用的情况下消耗真实上游成本。
// 限流预留(estimateTokens)与余额预扣(reserveBalance)两条路径都要有非零兜底。
func TestZeroCapTokensStillReserves(t *testing.T) {
	relayRetryTestDB(t)
	ctx := context.Background()
	if err := op.SettingUpsert(model.SettingKeyBalanceReserveCap, "0"); err != nil {
		t.Fatalf("set zero cap: %v", err)
	}
	if _, estOut := estimateTokens([]byte(`{"model":"g","messages":[{"role":"user","content":"hi"}]}`)); estOut != 4096 {
		t.Fatalf("estimateTokens must fall back to 4096 when capTokens is 0, got %d", estOut)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	username := "u" + suffix
	user, err := op.UserCreate(username, username+"@test.local", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := op.UserSetBalance(user.ID, 1000, user.ID, "test", "seed"); err != nil {
		t.Fatalf("set balance: %v", err)
	}
	request := newRequestState(ctx, "grp-cap", "", 7, model.ProtocolOpenAIChatCompletion,
		`{"model":"grp-cap"}`, 0, user.ID, "127.0.0.1", "", 600)
	group := model.Group{
		ID:    7,
		Name:  "grp-cap",
		Items: []model.GroupItem{{UserPrice: model.LLMPrice{Input: 1, Output: 1}}},
	}
	if err := reserveBalance(request, group); err != nil {
		t.Fatalf("reserveBalance with zero capTokens: %v", err)
	}
	defer request.markFailed(errors.New("test done"), "", nil) // 停掉计费租约心跳。
	var reservation model.BalanceReservation
	if err := db.GetDB().Where("request_id = ?", request.ID).First(&reservation).Error; err != nil {
		t.Fatalf("no balance reservation recorded: %v", err)
	}
	if reservation.Amount <= 0 {
		t.Fatalf("zero capTokens produced a zero balance reserve: %.6f", reservation.Amount)
	}
}

// ---- 流式收尾语义 ----

// latestRelayRequest 返回进程内最新一个请求状态的副本。
func latestRelayRequest() (RequestState, bool) {
	mu.Lock()
	defer mu.Unlock()
	var latest RequestState
	found := false
	for _, r := range requests {
		if !found || r.StartedAt.After(latest.StartedAt) {
			latest = *r
			found = true
		}
	}
	return latest, found
}

// attemptRowFor 取指定请求的首轮尝试行。
func attemptRowFor(t *testing.T, requestID uint64) model.RelayRequestAttempt {
	t.Helper()
	var row model.RelayRequestAttempt
	if err := db.GetDB().Where("request_id = ?", requestID).Order("round").First(&row).Error; err != nil {
		t.Fatalf("no attempt row for request %d: %v", requestID, err)
	}
	return row
}

// newStreamingUpstream 起一个分两段下发的 SSE 上游: 首个内容事件发出即停, 等 release 关闭后
// 再补结束事件与用量帧 —— 测试借此在「首字节已交付、流尚未结束」的窗口里观察请求状态。
func newStreamingUpstream(release <-chan struct{}) (*httptest.Server, *atomic.Int64) {
	var posts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"he\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-release
		fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"llo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	return srv, &posts
}

// TestStreamRoundFinalizesOnce 钉住流式轮次的收尾语义: 首字节已交付但流尚未结束时, 尝试行
// 必须仍是「在途」—— 终态只在流末统一收尾一次, 不在首事件后提前记 success。
// 同时顺带钉住提交标记的时机: markCommitted 只在 Write 返回 n>0 之后落下。
func TestStreamRoundFinalizesOnce(t *testing.T) {
	release := make(chan struct{})
	srv, _ := newStreamingUpstream(release)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, body) }()

	// 等到首字节已提交(提交标记只在写成功后落下), 在流未结束的窗口里检查尝试行。
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, ok := latestRelayRequest()
		if ok && state.Model == env.group && state.Status == StatusCommitted {
			if state.FirstTokenDuration <= 0 {
				close(release)
				t.Fatal("committed without recording first-token timing")
			}
			row := attemptRowFor(t, state.ID)
			if row.Status != "running" {
				close(release)
				t.Fatalf("streaming round finalized early while still streaming: attempt status %q, want running", row.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("stream never reached committed state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)

	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("stream did not finish")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("stream failed: status %d body %s", w.Code, w.Body.String())
	}
	state, ok := latestRelayRequest()
	if !ok || state.Model != env.group {
		t.Fatal("request state missing after stream")
	}
	if state.Status != StatusSuccess {
		t.Fatalf("want success final state, got %q", state.Status)
	}
	row := attemptRowFor(t, state.ID)
	if row.Status != "success" {
		t.Fatalf("want a single success attempt row after stream end, got %q", row.Status)
	}
}

// TestStreamDeliveredThenCanceledKeepsSuccess 钉住「完整交付与客户端断开竞态」: 结束事件已转发、
// 流已按协议终止后, 请求上下文的取消不得覆盖成功终态 —— 响应已完整计费交付, 记成 canceled
// 会把成功请求算成失败指标。
func TestStreamDeliveredThenCanceledKeepsSuccess(t *testing.T) {
	release := make(chan struct{})
	srv, _ := newStreamingUpstream(release)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/relay-test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		c.Request = req.WithContext(reqCtx)
		c.Set("api_key_user_id", int(env.userID))
		c.Set("api_key_id", 0)
		Forward(llm.APIFormatOpenAIChatCompletion)(c)
		done <- w
	}()

	// 首事件已提交后放开上游剩余事件; 结束事件送达前后取消请求上下文,
	// 模拟「客户端在完整交付的瞬间断开」的竞态窗口。
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, ok := latestRelayRequest()
		if ok && state.Model == env.group && state.Status == StatusCommitted {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("stream never reached committed state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	// 等结束事件转发完成(流循环按 last 退出), 取消落在收尾聚合/结算窗口内,
	// 与 handler.go 的「完整交付后 ctx.Err() 不得覆盖成功」检查正面相撞。
	time.Sleep(300 * time.Millisecond)
	cancelReq()

	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("stream did not finish")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("stream failed: status %d body %s", w.Code, w.Body.String())
	}
	state, ok := latestRelayRequest()
	if !ok {
		t.Fatal("request state missing after stream")
	}
	if state.Status != StatusSuccess {
		t.Fatalf("delivered stream must stay success, got %q", state.Status)
	}
}

// brokenWriter 模拟「一个字节都没写出去」的客户端故障。
type brokenWriter struct{ header http.Header }

func (w *brokenWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *brokenWriter) Write([]byte) (int, error) { return 0, errors.New("client write failed") }

func (w *brokenWriter) WriteHeader(int) {}

// TestStreamCommitRequiresWrittenBytes 钉住提交标记的门槛: 首字节写失败时不得进入 committed,
// 否则「什么都没写出去」的响应会被误记成已提交, 也挡住了本来安全的错误处理路径。
func TestStreamCommitRequiresWrittenBytes(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, _ := newStreamingUpstream(release)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	body := fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, env.group)
	forwardRequestWriter(llm.APIFormatOpenAIChatCompletion, env.userID, body, &brokenWriter{})

	state, ok := latestRelayRequest()
	if !ok || state.Model != env.group {
		t.Fatal("request state missing")
	}
	if state.Status != StatusFailed {
		t.Fatalf("want failed state after unwritten response, got %q", state.Status)
	}
	if state.FirstTokenDuration != 0 {
		t.Fatalf("commit marker set without a single written byte: first-token=%v", state.FirstTokenDuration)
	}
}
