package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
)

func TestAutoRoutingFiltersContextBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	models := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls.Add(1)
		models <- body.Model
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, server.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2, config)
	group := setScoreTestGroup(t, env, config)
	for index, item := range group.Items {
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: item.ModelName, Context: []int{100, 10000}[index]}, context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoSummary = fmt.Sprintf("%d,%d", group.Items[0].ChannelGrantID, group.Items[1].ChannelGrantID)
	setScoreTestGroup(t, env, config)
	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"请总结这篇文章"}],"max_tokens":100}`, env.group))
	if response.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
	if chosen := <-models; chosen != group.Items[1].ModelName {
		t.Fatalf("selected %q, want %q", chosen, group.Items[1].ModelName)
	}
	response = forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"extract"}],"response_format":{"type":"json_schema","json_schema":{"name":"sample","schema":{"type":"object"}}}}`, env.group))
	if response.Code != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("unqualified schema request dispatched: status=%d calls=%d", response.Code, calls.Load())
	}
	config.AutoRouting = "shadow"
	setScoreTestGroup(t, env, config)
	response = forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"plain text"}],"max_tokens":1}`, env.group))
	if response.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("shadow affected dispatch: status=%d calls=%d", response.Code, calls.Load())
	}
}

func TestAutoRoutingChoosesResponsesCodingAndNativeToolMembers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	t.Run("coding accepts Responses-only member", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIResponse, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoCoding = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		setScoreTestGroup(t, env, config)
		response := forwardRequest(llm.APIFormatOpenAIResponse, env.userID,
			fmt.Sprintf(`{"model":%q,"input":"Fix this compile error","max_output_tokens":100}`, env.group))
		if response.Code != http.StatusOK {
			t.Fatalf("Responses coding candidate rejected: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("tool rejects Responses-only member", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIResponse, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoToolVerified = config.AutoTool
		setScoreTestGroup(t, env, config)
		nonce := fmt.Sprintf("nonce-resp-%d", time.Now().UnixNano())
		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group, nonce))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("Responses-only tool candidate should be rejected before dispatch: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("tool candidate is not usable until explicitly verified", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		setScoreTestGroup(t, env, config)
		nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group, nonce))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("unverified tool candidate should be rejected: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("explicit group context override qualifies a real candidate", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoGeneral = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoContextOverrides = map[string]int{fmt.Sprintf("%d", group.Items[0].ChannelGrantID): 10000}
		setScoreTestGroup(t, env, config)
		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"plain text"}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusOK {
			t.Fatalf("configured context override did not qualify candidate: status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestAutoRoutingQualityFloorPrefersVerifiedTaskCapability(t *testing.T) {
	var calls atomic.Int64
	models := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls.Add(1)
		models <- body.Model
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2, config)
	group := setScoreTestGroup(t, env, config)
	for _, item := range group.Items {
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: item.ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// 第一项更便宜且排序更靠前，但只适合通用任务；第二项经过编程验证。
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: group.Items[0].ModelName, Task: model.AutoRoutingTaskGeneral, Score: 80, Verified: true,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: group.Items[1].ModelName, Task: model.AutoRoutingTaskCoding, Score: 92, Verified: true,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoCoding = fmt.Sprintf("%d,%d", group.Items[0].ChannelGrantID, group.Items[1].ChannelGrantID)
	config.AutoMinTaskScore = 80
	config.AutoQualityWeight = 60
	config.AutoPriceWeight = 40
	config.AutoLatencyWeight = 0
	config.AutoSuccessWeight = 0
	setScoreTestGroup(t, env, config)

	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Fix this compile error in the code"}],"max_tokens":100}`, env.group))
	if response.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
	if chosen := <-models; chosen != group.Items[1].ModelName {
		t.Fatalf("selected %q without coding capability, want %q", chosen, group.Items[1].ModelName)
	}
}

func TestAutoRoutingQualityFloorRejectsUnverifiedCandidates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
	group := setScoreTestGroup(t, env, config)
	if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: group.Items[0].ModelName, Task: model.AutoRoutingTaskCoding, Score: 95, Verified: false,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoCoding = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
	config.AutoMinTaskScore = 80
	setScoreTestGroup(t, env, config)

	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Fix this compile error in the code"}],"max_tokens":100}`, env.group))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unverified candidate dispatched: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAutoRoutingNoCandidateFallbackUsesOriginalScoreGroup(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
	group := setScoreTestGroup(t, env, config)
	if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: group.Items[0].ModelName, Task: model.AutoRoutingTaskCoding, Score: 50, Verified: true,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoCoding = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
	config.AutoMinTaskScore = 80
	config.AutoFallbackNoCandidate = true
	setScoreTestGroup(t, env, config)

	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Fix this compile error in the code"}],"max_tokens":100}`, env.group))
	if response.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("fallback did not use original score group: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestAutoRoutingToolNeverFallsBackOrExploresUnverifiedMember(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
	group := setScoreTestGroup(t, env, config)
	if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
		t.Fatal(err)
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
	config.AutoFallbackNoCandidate = true
	config.AutoExplorePercent = 20
	setScoreTestGroup(t, env, config)

	nonce := fmt.Sprintf("nonce-tool-gate-%d", time.Now().UnixNano())
	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group, nonce))
	if response.Code != http.StatusServiceUnavailable || calls.Load() != 0 {
		t.Fatalf("unverified tool bypassed safety gates: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestAutoRoutingStickyCandidateStillMustPassQualityFloor(t *testing.T) {
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoCoding = "1,2"
	config.AutoMinTaskScore = 80
	config.AutoStickySeconds = 300
	group := model.Group{
		ID: 99110, Mode: model.GroupModeScore, RelayConfig: config,
		Items: []model.GroupItem{
			{ID: 1, ChannelGrantID: 1, ModelName: "sticky", Available: true, Kind: model.MediaKindText, Protocols: model.ProtocolOpenAIChatCompletion},
			{ID: 2, ChannelGrantID: 2, ModelName: "qualified", Available: true, Kind: model.MediaKindText, Protocols: model.ProtocolOpenAIChatCompletion},
		},
	}
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: "sticky", Task: model.AutoRoutingTaskCoding, Score: 50, Verified: true,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	autoStickyRecordSuccess(group, 7, 3, AutoTaskCoding, model.ProtocolOpenAIChatCompletion, 1)
	analysis := analyzeAutoCandidates(group, autoTask{name: AutoTaskCoding, input: 10, output: 10})
	if _, found := autoItemByID(analysis.Group.Items, 1); found {
		t.Fatal("sticky member bypassed the quality floor")
	}
}

func TestAutoRoutingStickyRequestRetriesInsideEligibleMemberSet(t *testing.T) {
	var calls atomic.Int64
	models := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		models <- body.Model
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"message":"temporary upstream failure","type":"server_error"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 2, config)
	group := setScoreTestGroup(t, env, config)
	for _, item := range group.Items {
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: item.ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := op.TaskCapabilitySave(model.ModelTaskCapability{
			ModelName: item.ModelName, Task: model.AutoRoutingTaskGeneral, Score: 90, Verified: true,
		}, context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoGeneral = fmt.Sprintf("%d,%d", group.Items[0].ChannelGrantID, group.Items[1].ChannelGrantID)
	config.AutoMinTaskScore = 80
	config.AutoStickySeconds = 300
	group = setScoreTestGroup(t, env, config)
	autoStickyRecordSuccess(group, 0, env.userID, AutoTaskGeneral, model.ProtocolOpenAIChatCompletion, group.Items[1].ID)

	response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"plain text"}],"max_tokens":100}`, env.group))
	if response.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("sticky request did not recover inside eligible cohort: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
	first, second := <-models, <-models
	if first != group.Items[1].ModelName {
		t.Fatalf("sticky member was not tried first: first=%q want=%q", first, group.Items[1].ModelName)
	}
	if second == first || second != group.Items[0].ModelName {
		t.Fatalf("retry reopened exploration/unrestricted candidates: first=%q second=%q", first, second)
	}
}

func TestAutoRoutingCircuitAndRampHoldUseOriginalScoreGroup(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	t.Run("open circuit falls back", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoGeneral = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoFallbackFailures = 2
		group = setScoreTestGroup(t, env, config)
		autoCircuitRecordSuccess(group.ID, AutoTaskGeneral)
		autoCircuitRecordFailure(group, AutoTaskGeneral)
		autoCircuitRecordFailure(group, AutoTaskGeneral)

		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"plain text"}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusOK {
			t.Fatalf("open circuit rejected instead of falling back: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("ramp hold falls back", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoGeneral = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoRampPercent = 100
		for percent := 1; percent < 100; percent++ {
			if !autoRampHit(group.ID, 0, int(env.userID), percent) {
				config.AutoRampPercent = percent
				break
			}
		}
		if config.AutoRampPercent == 100 {
			t.Fatal("test could not find a stable ramp-miss bucket")
		}
		setScoreTestGroup(t, env, config)

		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"plain text"}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusOK {
			t.Fatalf("ramp hold rejected instead of falling back: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("tool circuit never falls back", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoRampPercent = 100
		config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoToolVerified = config.AutoTool
		config.AutoFallbackFailures = 2
		group = setScoreTestGroup(t, env, config)
		autoCircuitRecordSuccess(group.ID, AutoTaskTool)
		autoCircuitRecordFailure(group, AutoTaskTool)
		autoCircuitRecordFailure(group, AutoTaskTool)

		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"use a tool"}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("tool circuit reopened an unverified group: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("tool ramp hold never falls back", func(t *testing.T) {
		config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
		env := newRelayGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, 1, config)
		group := setScoreTestGroup(t, env, config)
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: group.Items[0].ModelName, Context: 10000}, context.Background()); err != nil {
			t.Fatal(err)
		}
		config.AutoRouting = "active"
		config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		config.AutoToolVerified = config.AutoTool
		config.AutoRampPercent = 100
		for percent := 1; percent < 100; percent++ {
			if !autoRampHit(group.ID, 0, int(env.userID), percent) {
				config.AutoRampPercent = percent
				break
			}
		}
		if config.AutoRampPercent == 100 {
			t.Fatal("test could not find a stable ramp-miss bucket")
		}
		setScoreTestGroup(t, env, config)

		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"use a tool"}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("tool ramp hold reopened an unverified group: status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestAutoRoutingAnthropicToolRequiresAnthropicVerification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":5,"output_tokens":2}}`)
	}))
	defer upstream.Close()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	env := newRelayGroup(t, upstream.URL, model.ProtocolAnthropicMessage, model.MediaKindText, 2, config)
	group := setScoreTestGroup(t, env, config)
	for _, item := range group.Items {
		if err := op.CapabilitySaveManual(model.ModelCapability{Name: item.ModelName, Context: 32000}, context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	grant0 := fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
	grant1 := fmt.Sprintf("%d", group.Items[1].ChannelGrantID)
	config.AutoRouting = "active"
	config.AutoRampPercent = 100
	config.AutoTool = grant0 + "," + grant1
	// Member 0 is only verified for OpenAI Chat & Responses; Member 1 is verified for Anthropic Messages.
	config.AutoToolVerified = grant0
	config.AutoResponsesToolVerified = grant0
	config.AutoAnthropicToolVerified = grant1
	group = setScoreTestGroup(t, env, config)

	task := autoTask{name: AutoTaskTool, protocol: model.ProtocolAnthropicMessage, input: 100, output: 100}
	analysis := analyzeAutoCandidates(group, task)
	if len(analysis.Group.Items) != 1 || analysis.Group.Items[0].ChannelGrantID != group.Items[1].ChannelGrantID {
		t.Fatalf("eligible anthropic tool items=%+v, want only grant %d", analysis.Group.Items, group.Items[1].ChannelGrantID)
	}
	if len(analysis.Excluded) != 1 || analysis.Excluded[0].Reason != "auto_tool_unverified" {
		t.Fatalf("excluded=%+v, want member 0 excluded with auto_tool_unverified", analysis.Excluded)
	}
}
