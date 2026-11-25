package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestAnthropicToolProbeRequiresResultRoundTrip(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("matches_tool_result=%t", valid), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom/messages" || r.Header.Get("X-Api-Key") != "probe-key" ||
					r.Header.Get("Anthropic-Version") != "2023-06-01" {
					t.Error("unexpected probe path or headers")
				}
				if calls.Add(1) == 1 {
					fmt.Fprint(w, `{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_probe","name":"switchyard_probe","input":{}}]}`)
					return
				}
				var request struct {
					Messages []struct {
						Role    string
						Content json.RawMessage
					}
				}
				var blocks []struct {
					Type    string
					ID      string `json:"tool_use_id"`
					Content string
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 3 ||
					request.Messages[1].Role != "assistant" || request.Messages[2].Role != "user" ||
					json.Unmarshal(request.Messages[2].Content, &blocks) != nil || len(blocks) != 1 ||
					blocks[0].Type != "tool_result" || blocks[0].ID != "toolu_probe" || blocks[0].Content == "" {
					t.Error("second round did not replay the tool exchange")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				answer := "unrelated reply"
				if valid {
					answer = blocks[0].Content
				}
				fmt.Fprintf(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":%q}]}`, answer)
			}))
			defer server.Close()
			channel := model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: server.URL, AnthropicMessagePath: "/custom/messages"}}
			grant := model.ChannelGrant{ID: 7, ChannelModel: &model.ChannelModel{Name: "probe-model"},
				ChannelKey: &model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "probe-key"}}}
			result, err := runAnthropicToolProbe(context.Background(), server.Client(), channel, grant, false, false)
			if (err == nil) != valid || result.Success != valid || !result.TwoRound || calls.Load() != 2 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls.Load())
			}
		})
	}
}
