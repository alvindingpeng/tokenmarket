package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
)

func setScoreTestGroup(t *testing.T, env relayGroupEnv, config model.GroupRelayConfig) model.Group {
	t.Helper()
	g, err := op.GroupGetByName(env.group, env.userID)
	if err != nil {
		t.Fatal(err)
	}
	mode := model.GroupModeScore
	updated, err := op.GroupUpdate(g.ID, &model.GroupUpdateRequest{Mode: &mode, RelayConfig: &config}, model.Scope{ID: env.userID, Role: model.RoleAdmin}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return *updated
}

func TestScoreRetryCandidateOrderAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		budget, members, want int
	}{{"five", 5, 5, 5}, {"two", 2, 5, 2}, {"one", 1, 5, 1}, {"default", 0, 7, 7}, {"large", 100, 7, 7}, {"exhausted", 5, 2, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			var attempted []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				attempted = append(attempted, body.Model)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(500)
				fmt.Fprintf(w, `{"error":{"message":"failure-%d","type":"server_error"}}`, len(attempted))
			}))
			defer srv.Close()
			config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
			config.MaxTotalAttempts = tc.budget
			config.MemberMaxAttempts = 100
			config.MemberRetryIntervalSeconds = 300
			config.MaxWaitingRequests = 100
			env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, tc.members, config)
			g := setScoreTestGroup(t, env, config)
			// Explicit prices make the original weighted strategy's expected ordering observable.
			for i, item := range g.Items {
				if err := op.ChannelModelListingUpdate(item.ChannelID, []op.ChannelModelListing{{Name: item.ModelName, Listed: true, SupplyPrice: model.LLMPrice{Input: float64(i + 1), Output: float64(i + 1)}}}, model.Scope{ID: env.userID, Role: model.RoleAdmin}, context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group))
			if len(attempted) != tc.want {
				t.Fatalf("attempts %v want %d; status %d body %s", attempted, tc.want, w.Code, w.Body.String())
			}
			for i, name := range attempted {
				if name != g.Items[i].ModelName {
					t.Fatalf("order %v: index %d want %s", attempted, i, g.Items[i].ModelName)
				}
			}
			if w.Code != 500 || !strings.Contains(w.Body.String(), fmt.Sprintf("failure-%d", tc.want)) {
				t.Fatalf("last upstream error lost: %d %s", w.Code, w.Body.String())
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("score waited for retry interval/cooldown: %s", time.Since(start))
			}
		})
	}
}

func TestScoreRetryLocalExclusionSurvivesRecoveryAndConfigChanges(t *testing.T) {
	relayRetryTestDB(t)
	group := model.Group{ID: 9081, Mode: model.GroupModeScore, RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeScore), Items: []model.GroupItem{
		{ID: 1, ChannelID: 10, ChannelModelID: 100, ChannelKeyID: 1, Available: true},
		{ID: 2, ChannelID: 10, ChannelModelID: 100, ChannelKeyID: 2, Available: true},
		{ID: 3, ChannelID: 20, ChannelModelID: 200, Available: true},
	}}
	var state scoreRetryState
	state.exclude(group.Items[0], false)
	routeMu.Lock()
	routes[group.ID] = &RouteState{GroupID: group.ID, Cooldowns: map[int]int64{1: time.Now().Add(-time.Second).UnixMilli()}, ProbeItemID: 1}
	routeMu.Unlock()
	filtered := state.filtered(group)
	if hasOtherEligible(filtered, 3) {
		t.Fatal("different key of failed pair remained eligible")
	}
	if got := pickGroupItem(filtered); got.ID != 3 {
		t.Fatalf("revisited failed pair after cooldown expiry/probe: %+v", got)
	}
	if !group.Items[0].Available || !group.Items[1].Available {
		t.Fatal("mutated cached group")
	}
	if _, ok := RouteStateOf(group).Cooldowns[1]; !ok {
		t.Fatal("request filtering erased shared cooldown")
	}
	// Configuration replacement may change member IDs, but not endpoint identity.
	group.Items[0].ID = 9
	group.Items = append(group.Items, model.GroupItem{ID: 4, ChannelID: 10, ChannelModelID: 101, Available: true})
	filtered = state.filtered(group)
	if filtered.Items[0].Available || !filtered.Items[3].Available {
		t.Fatal("pair identity/config update filtering incorrect")
	}
	state.exclude(group.Items[0], true)
	if state.filtered(group).Items[3].Available {
		t.Fatal("quota did not skip other model on same channel")
	}
	group.Mode = model.GroupModeFailover
	if !state.filtered(group).Items[0].Available {
		t.Fatal("score exclusion leaked into other modes")
	}
	for _, budget := range []int{0, -1, 5, 10, 100} {
		if scoreAttemptLimit(budget) != budget {
			t.Fatalf("budget %d was unexpectedly changed", budget)
		}
	}
}

func TestScoreRetryClientPolicyAndMediaProtection(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		media      bool
	}{
		{"client", `{"error":{"message":"bad input","type":"invalid_request_error"}}`, 400, false},
		{"policy", `{"error":{"message":"content_policy_violation"}}`, 400, false},
		{"media", `{"error":{"message":"boom","type":"server_error"}}`, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, posts := newUpstreamStub(tc.status, tc.body)
			defer srv.Close()
			protocol, kind, format := model.ProtocolOpenAIChatCompletion, model.MediaKindText, llm.APIFormatOpenAIChatCompletion
			body := `{"model":%q,"messages":[{"role":"user","content":"hi"}]}`
			if tc.media {
				protocol, kind, format = model.ProtocolOpenAIVideo, model.MediaKindVideo, llm.APIFormatOpenAIVideo
				body = `{"model":%q,"prompt":"cat","seconds":"4","size":"1280x720"}`
			}
			config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
			env := newRelayGroup(t, srv.URL, protocol, kind, 3, config)
			setScoreTestGroup(t, env, config)
			w := forwardRequest(format, env.userID, fmt.Sprintf(body, env.group))
			if posts.Load() != 1 || w.Code != tc.status {
				t.Fatalf("unsafe retry: posts %d status %d body %s", posts.Load(), w.Code, w.Body.String())
			}
		})
	}
}

func TestScoreRetryDuplicateKeysAndChannelQuota(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(fmt.Sprintf("quota=%v", quota), func(t *testing.T) {
			var attempted []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				attempted = append(attempted, body.Model)
				w.Header().Set("Content-Type", "application/json")
				if quota {
					w.WriteHeader(402)
					fmt.Fprint(w, `{"error":{"message":"insufficient_quota"}}`)
				} else {
					w.WriteHeader(500)
					fmt.Fprint(w, `{"error":{"message":"boom"}}`)
				}
			}))
			defer srv.Close()
			config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
			env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2, config)
			g := setScoreTestGroup(t, env, config)
			first, last := g.Items[0], g.Items[1]
			scope := model.Scope{ID: env.userID, Role: model.RoleAdmin}
			ctx := context.Background()
			detail, err := op.ChannelDetailGet(first.ChannelID, scope)
			if err != nil {
				t.Fatal(err)
			}
			secondModel := first.ModelName + "-other"
			detail.Keys = append(detail.Keys, model.ChannelKeyConfig{Name: "k2", Key: "sk-2", Enabled: true})
			detail.Models = append(detail.Models, secondModel)
			detail.Grants = append(detail.Grants, model.ChannelGrantConfig{ModelName: first.ModelName, KeyName: "k2", Protocols: model.ProtocolOpenAIChatCompletion}, model.ChannelGrantConfig{ModelName: secondModel, KeyName: "k2", Protocols: model.ProtocolOpenAIChatCompletion})
			if _, err := op.ChannelUpdate(&detail, scope, ctx); err != nil {
				t.Fatal(err)
			}
			if err := op.ChannelModelListingUpdate(first.ChannelID, []op.ChannelModelListing{{Name: secondModel, Listed: true, SupplyPrice: model.LLMPrice{Input: 1, Output: 1}}}, scope, ctx); err != nil {
				t.Fatal(err)
			}
			var grants []model.ChannelGrant
			if err := db.GetDB().Order("id").Find(&grants).Error; err != nil {
				t.Fatal(err)
			}
			items := []model.GroupItemInput{{ChannelGrantID: first.ChannelGrantID}}
			for _, grant := range grants {
				if grant.ID != first.ChannelGrantID && grant.ID != last.ChannelGrantID {
					items = append(items, model.GroupItemInput{ChannelGrantID: grant.ID})
				}
			}
			items = append(items, model.GroupItemInput{ChannelGrantID: last.ChannelGrantID})
			if _, err := op.GroupUpdate(g.ID, &model.GroupUpdateRequest{Items: &items}, scope, ctx); err != nil {
				t.Fatal(err)
			}
			w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group))
			want := []string{first.ModelName, secondModel, last.ModelName}
			if quota {
				want = []string{first.ModelName, last.ModelName}
			}
			if strings.Join(attempted, ",") != strings.Join(want, ",") {
				t.Fatalf("attempted %v want %v status %d body %s", attempted, want, w.Code, w.Body.String())
			}
			if quota && !op.ChannelQuotaPaused(first.ChannelID) {
				t.Fatal("quota error not wired to channel breaker")
			}
		})
	}
}

func TestScoreStreamWrittenNeverRetries(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"cut\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 3, config)
	setScoreTestGroup(t, env, config)
	forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, env.group))
	if posts != 1 {
		t.Fatalf("committed stream redispatched %d times", posts)
	}
}

func TestScoreAdmissionRejectionDoesNotExcludeOrBurnBudget(t *testing.T) {
	srv, posts := newUpstreamStub(200, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	defer srv.Close()
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	config.MaxTotalAttempts = 1
	config.MaxWaitingRequests = 100
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
	setScoreTestGroup(t, env, config)
	admit := &atomic.Bool{}
	limiter = ratelimit.New(admissionDenyPolicy(admit))
	timer := time.AfterFunc(400*time.Millisecond, func() { admit.Store(true) })
	defer timer.Stop()
	w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group))
	if w.Code != 200 || posts.Load() != 1 || limitedAttempts(t) < 2 {
		t.Fatalf("admission changed score candidate/budget: status %d posts %d limited %d body %s", w.Code, posts.Load(), limitedAttempts(t), w.Body.String())
	}
}

func TestScoreRetryUsesLiveAttemptBudget(t *testing.T) {
	for _, tc := range []struct{ initial, updated, want int }{{5, 1, 1}, {1, 2, 2}} {
		t.Run(fmt.Sprintf("%d-to-%d", tc.initial, tc.updated), func(t *testing.T) {
			var env relayGroupEnv
			var updateErr error
			posts := 0
			config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
			config.MaxTotalAttempts = tc.initial
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				if posts == 1 {
					g, err := op.GroupGetByName(env.group, env.userID)
					if err != nil {
						updateErr = err
					} else {
						updated := config
						updated.MaxTotalAttempts = tc.updated
						_, updateErr = op.GroupUpdate(g.ID, &model.GroupUpdateRequest{RelayConfig: &updated}, model.Scope{ID: env.userID, Role: model.RoleAdmin}, context.Background())
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":{"message":"live-budget-failure"}}`)
			}))
			defer srv.Close()
			env = newRelayGroup(t, srv.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 3, config)
			setScoreTestGroup(t, env, config)
			w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, env.group))
			if updateErr != nil {
				t.Fatal(updateErr)
			}
			if posts != tc.want || !strings.Contains(w.Body.String(), "live-budget-failure") {
				t.Fatalf("live budget not honored: attempts %d want %d body %s", posts, tc.want, w.Body.String())
			}
		})
	}
}
