package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestAnthropicStreamingEvaluation(t *testing.T) {
	for _, failure := range []string{"", "truncated", "invalid JSON", "wrong result"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Stream   bool
					Tools    []json.RawMessage
					Messages []struct{ Content json.RawMessage }
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !payload.Stream || r.Header.Get("Accept") != "text/event-stream" {
					t.Error("not a streaming request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(data string) { fmt.Fprintf(w, "data: %s\n\n", data) }
				emit(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}`)
				stop := "end_turn"
				if len(payload.Messages) < 5 && len(payload.Tools) > 0 {
					stop = "tool_use"
					emit(fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_%d","name":"switchyard_probe","input":{}}}`, len(payload.Messages)))
					emit(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`)
					if failure != "invalid JSON" {
						emit(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"}"}}`)
					}
				} else {
					var answer string
					if len(payload.Messages) == 5 {
						var texts []string
						for _, index := range []int{2, 4} {
							var result []struct{ Content string }
							if json.Unmarshal(payload.Messages[index].Content, &result) != nil || len(result) != 1 {
								t.Error("missing tool result")
								return
							}
							if index == 2 && len(result[0].Content) < 6000 {
								t.Error("first tool result must exercise long content")
							}
							lastLine := result[0].Content[strings.LastIndex(result[0].Content, "\n")+1:]
							texts = append(texts, lastLine)
						}
						answer = strings.Join(texts, " ")
						if failure == "wrong result" {
							answer = "unrelated"
						}
					} else if strings.Contains(string(payload.Messages[0].Content), "is_valid_port") {
						answer = `{"find":"port < 65535","replace":"port <= 65535"}`
					} else if strings.Contains(string(payload.Messages[0].Content), "Python") {
						answer = "return len(items)"
					} else {
						answer = "x >= 0"
					}
					emit(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
					emit(fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, answer))
				}
				emit(`{"type":"content_block_stop","index":0}`)
				emit(fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":5}}`, stop))
				if failure != "truncated" || len(payload.Tools) == 0 {
					emit(`{"type":"message_stop"}`)
				}
			}))
			defer server.Close()
			channel := model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: server.URL}}
			grant := model.ChannelGrant{ChannelModel: &model.ChannelModel{Name: "fixture", SupplyPrice: model.LLMPrice{Input: 1, Output: 2}}, ChannelKey: &model.ChannelKey{}}
			result, err := runAnthropicToolProbe(context.Background(), server.Client(), channel, grant, true, true)
			if (err == nil) != (failure == "") || result.Success != (failure == "") {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if len(result.Evaluation) != 2 || result.Evaluation[1].Passed != 3 || result.Evaluation[1].EstimatedSupplyCost == nil {
				t.Fatalf("missing separate coding outcomes: %+v", result.Evaluation)
			}
			if failure == "" && (result.Evaluation[0].StreamsCompleted != 3 || result.Evaluation[0].StreamsExpected != 3) {
				t.Fatalf("missing complete streams: %+v", result.Evaluation)
			}
		})
	}
}
