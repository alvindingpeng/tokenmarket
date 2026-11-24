package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

func TestRunModelToolProbeRequiresTwoSuccessfulRounds(t *testing.T) {
	handlerTestResetCaches(t)
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "tool-probe.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Run("success", func(t *testing.T) {
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if call == 1 {
				fmt.Fprint(w, `{"id":"probe","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"switchyard_probe","arguments":"{\"nonce\":\"probe-1\"}"}}]},"finish_reason":"tool_calls"}]}`)
				return
			}
			fmt.Fprint(w, `{"id":"probe","choices":[{"index":0,"message":{"role":"assistant","content":"probe complete"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()
		channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "probe", BaseURL: server.URL, OpenAIChatCompletionPath: "/v1/chat/completions", Dialect: model.DialectGeneric}}
		grant := model.ChannelGrant{
			ID: 7, Protocols: model.ProtocolOpenAIChatCompletion,
			ChannelModel: &model.ChannelModel{ChannelID: 1, Name: "probe-model"},
			ChannelKey:   &model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-probe"}},
		}
		result, err := runModelToolProbe(context.Background(), server.Client(), channel, grant, 80)
		if err != nil || !result.Success || !result.TwoRound || calls.Load() != 2 {
			t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
		}
		capability, ok := op.TaskCapabilityGet("probe-model", model.AutoRoutingTaskTool)
		if !ok || !capability.Verified || capability.Score != 80 {
			t.Fatalf("verified capability was not persisted: %+v %t", capability, ok)
		}
	})

	t.Run("first round without tool call", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"probe","choices":[{"index":0,"message":{"role":"assistant","content":"I answered directly"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()
		channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "probe", BaseURL: server.URL, Dialect: model.DialectGeneric}}
		grant := model.ChannelGrant{
			ID: 8, ChannelModel: &model.ChannelModel{ChannelID: 1, Name: "probe-model"},
			ChannelKey: &model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-probe"}},
		}
		result, err := runModelToolProbe(context.Background(), server.Client(), channel, grant, 80)
		if err == nil || result.Success || result.TwoRound {
			t.Fatalf("non-tool response was accepted: result=%+v err=%v", result, err)
		}
	})
}

func TestProbeExtractsAndRejectsMalformedToolCalls(t *testing.T) {
	if _, ok := extractProbeToolCall([]byte(`{"choices":[{"message":{"content":"no tool"}}]}`)); ok {
		t.Fatal("text-only response passed as a tool call")
	}
	body := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"call-1","function":{"name":"probe","arguments":"{}"}}]}}]}`)
	call, ok := extractProbeToolCall(body)
	if !ok || call["id"] != "call-1" {
		t.Fatalf("valid tool call not extracted: %v %t", call, ok)
	}
	if probeHasFinalAnswer(body) {
		t.Fatal("tool-call response was mistaken for a final text answer")
	}
	if !probeHasFinalAnswer([]byte(`{"choices":[{"message":{"content":"done"}}]}`)) {
		t.Fatal("final answer was not recognized")
	}
}

func TestToolProbeErrorNeverLeaksBodyOrKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"secret echo sk-leaked"}`)
	}))
	defer server.Close()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "probe", BaseURL: server.URL, Dialect: model.DialectGeneric}}
	grant := model.ChannelGrant{ID: 9, ChannelModel: &model.ChannelModel{Name: "m"}, ChannelKey: &model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-probe"}}}
	_, err := runModelToolProbe(context.Background(), server.Client(), channel, grant, 80)
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sk-probe") || strings.Contains(err.Error(), "sk-leaked") {
		t.Fatalf("unsafe probe error: %v", err)
	}
}

func TestToolProbeHelperParsingDeduplicatesGrantIDs(t *testing.T) {
	if got := parseGrantIDsForProbe(" 2, 2, nope, 0, 7 "); len(got) != 2 || got[0] != 2 || got[1] != 7 {
		t.Fatalf("parsed IDs=%v", got)
	}
	var stored model.ModelTaskCapability
	if json.Unmarshal([]byte(`{"task":"tool","verified":true}`), &stored) != nil || !stored.Verified {
		t.Fatal("tool capability JSON compatibility failed")
	}
}
