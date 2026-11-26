package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
)

func TestStandbyRouting(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mode            model.GroupMode
		budget, members int
		primaryStatus   int
		reserveStatus   int
		wantStatus      int
		wantPrimaries   int
		wantStandby     bool
		stream          bool
		tool            bool
		active          bool
		tooLong         bool
	}{
		{name: "healthy primary", mode: model.GroupModeFailover, budget: 3, members: 3, primaryStatus: 200, reserveStatus: 200, wantStatus: 200, wantPrimaries: 1},
		{name: "failure threshold", mode: model.GroupModeScore, budget: 7, members: 4, primaryStatus: 500, reserveStatus: 200, wantStatus: 200, wantPrimaries: 2, wantStandby: true},
		{name: "exhausted candidates", mode: model.GroupModeFailover, budget: 5, members: 2, primaryStatus: 503, reserveStatus: 200, wantStatus: 200, wantPrimaries: 1, wantStandby: true},
		{name: "budget reserves one extra attempt", mode: model.GroupModeScore, budget: 1, members: 4, primaryStatus: 500, reserveStatus: 200, wantStatus: 200, wantPrimaries: 1, wantStandby: true},
		{name: "standby failure is terminal", mode: model.GroupModeFailover, budget: 10, members: 4, primaryStatus: 503, reserveStatus: 500, wantStatus: 500, wantPrimaries: 2, wantStandby: true},
		{name: "policy refusal", mode: model.GroupModeScore, budget: 1, members: 2, primaryStatus: 400, reserveStatus: 200, wantStatus: 400, wantPrimaries: 1},
		{name: "committed stream", mode: model.GroupModeFailover, budget: 1, members: 2, primaryStatus: 200, reserveStatus: 200, wantStatus: 200, wantPrimaries: 1, stream: true},
		{name: "active task qualified reserve", mode: model.GroupModeScore, budget: 1, members: 2, primaryStatus: 500, reserveStatus: 200, wantStatus: 200, wantPrimaries: 1, wantStandby: true, active: true},
		{name: "context incompatible reserve", mode: model.GroupModeScore, budget: 1, members: 2, primaryStatus: 500, reserveStatus: 200, wantStatus: 500, wantPrimaries: 1, tooLong: true},
		{name: "unverified tool standby", mode: model.GroupModeScore, budget: 1, members: 2, primaryStatus: 500, reserveStatus: 200, wantStatus: 500, wantPrimaries: 1, tool: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct{ Model string }
				_ = json.NewDecoder(r.Body).Decode(&payload)
				calls = append(calls, payload.Model)
				status := tc.primaryStatus
				if strings.HasSuffix(payload.Model, "-0") {
					status = tc.reserveStatus
				}
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"cut\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
					w.(http.Flusher).Flush()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				switch status {
				case 200:
					fmt.Fprint(w, `{"id":"ok","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
				case 400:
					fmt.Fprint(w, `{"error":{"message":"content_policy_violation"}}`)
				default:
					fmt.Fprint(w, `{"error":{"message":"unavailable","type":"server_error"}}`)
				}
			}))
			defer server.Close()

			config := model.DefaultGroupRelayConfigForMode(tc.mode)
			config.MaxTotalAttempts = tc.budget
			env := newRelayGroup(t, server.URL, model.ProtocolOpenAIChatCompletion, model.MediaKindText, tc.members, config)
			group, err := op.GroupGetByName(env.group, env.userID)
			if err != nil {
				t.Fatal(err)
			}
			config.StandbyGrantID = group.Items[0].ChannelGrantID
			config.StandbyAfterFailures = 2
			if tc.active {
				config.AutoRouting = "active"
				config.AutoRampPercent = 100
				config.AutoGeneral = fmt.Sprintf("%d,%d", group.Items[0].ChannelGrantID, group.Items[1].ChannelGrantID)
				config.AutoContextOverrides = map[string]int{strconv.Itoa(group.Items[0].ChannelGrantID): 100_000, strconv.Itoa(group.Items[1].ChannelGrantID): 100_000}
			}
			if tc.tooLong {
				config.AutoContextOverrides = map[string]int{strconv.Itoa(group.Items[0].ChannelGrantID): 32}
			}
			_, err = op.GroupUpdate(group.ID, &model.GroupUpdateRequest{Mode: &tc.mode, RelayConfig: &config}, model.Scope{ID: env.userID, Role: model.RoleAdmin}, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			extra := ""
			if tc.stream {
				extra = `,"stream":true`
			}
			if tc.tool {
				extra = `,"tools":[{"type":"function","function":{"name":"get_data","parameters":{"type":"object"}}}]`
			}
			w := forwardRequest(llm.APIFormatOpenAIChatCompletion, env.userID, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]%s}`, env.group, extra))
			if w.Code != tc.wantStatus || len(calls) < tc.wantPrimaries {
				t.Fatalf("status=%d calls=%v body=%s", w.Code, calls, w.Body.String())
			}
			if slices.Contains(calls[:tc.wantPrimaries], group.Items[0].ModelName) {
				t.Fatalf("standby was used as a primary: %v", calls)
			}
			want := tc.wantPrimaries
			if tc.wantStandby {
				want++
			}
			if len(calls) != want || tc.wantStandby && calls[want-1] != group.Items[0].ModelName {
				t.Fatalf("incorrect standby dispatch: %v", calls)
			}
			if tc.wantStandby {
				var last model.RelayRequestAttempt
				if err := db.GetDB().Order("round DESC").First(&last).Error; err != nil || !strings.Contains(last.Decision, `"reason":"standby_selected"`) {
					t.Fatalf("standby decision missing: %v %s", err, last.Decision)
				}
				if tc.active {
					var decision model.RelayRouteDecision
					var details autoTaskDecisionDetails
					if json.Unmarshal([]byte(last.Decision), &decision) != nil ||
						json.Unmarshal([]byte(decision.Details), &details) != nil || details.RouteAction != "standby" {
						t.Fatalf("standby counted as an active decision: %s", last.Decision)
					}
				}
			}
		})
	}
}
