package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
		config.AutoTool = fmt.Sprintf("%d", group.Items[0].ChannelGrantID)
		setScoreTestGroup(t, env, config)
		response := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"search"}],"tools":[{"type":"function","function":{"name":"search"}}],"max_tokens":100}`, env.group))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("Responses-only tool candidate should be rejected before dispatch: status=%d body=%s", response.Code, response.Body.String())
		}
	})
}
