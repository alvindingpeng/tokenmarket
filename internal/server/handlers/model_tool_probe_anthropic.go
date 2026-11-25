package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func runAnthropicToolProbe(ctx context.Context, client *http.Client, channel model.Channel, grant model.ChannelGrant, stream, evaluate bool) (result model.ModelToolProbeResult, err error) {
	result = model.ModelToolProbeResult{ChannelGrantID: grant.ID, ModelName: grant.ChannelModel.Name}
	base, err := url.Parse(strings.TrimSpace(channel.BaseURL))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return result, fmt.Errorf("invalid channel base URL")
	}
	path := strings.TrimSpace(channel.AnthropicMessagePath)
	if path == "" {
		path = "/v1/messages"
	}
	endpoint := base.ResolveReference(&url.URL{Path: path}).String()
	probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	probeClient := *client
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	const name = "switchyard_probe"
	user := map[string]any{"role": "user", "content": "Call switchyard_probe once with an empty object. Then return only the text from the tool result."}
	payload := map[string]any{
		"model": result.ModelName, "max_tokens": 128,
		"messages": []map[string]any{user},
		"tools": []map[string]any{{
			"name": name, "description": "Return the verification text.",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		}},
		"tool_choice": map[string]string{"type": "auto"},
	}
	if stream {
		payload["stream"] = true
	}
	var usage anthropicProbeUsage
	send := func() (anthropicProbeMessage, error) {
		var response anthropicProbeMessage
		body, err := postToolProbe(probeCtx, &probeClient, endpoint, channel, grant.ChannelKey.Key, payload, model.ProtocolAnthropicMessage)
		if err != nil {
			return response, fmt.Errorf("Anthropic Messages probe request failed")
		}
		if stream {
			response, err = decodeAnthropicProbeStream(probeCtx, body)
			if err != nil {
				return response, err
			}
		} else if json.Unmarshal(body, &response) != nil {
			return response, fmt.Errorf("invalid Anthropic Messages response")
		}
		usage.add(response.Usage)
		return response, nil
	}
	if evaluate {
		start := time.Now()
		defer func() {
			tool := probeEvaluation("tool", 0, 1, start, usage, grant.ChannelModel)
			if result.Success {
				tool.Passed = 1
			}
			result.Evaluation = append(result.Evaluation, tool)
			usage = anthropicProbeUsage{}
			start = time.Now()
			passed := 0
			for _, fixture := range anthropicCodingFixtures {
				payload = map[string]any{
					"model": result.ModelName, "max_tokens": 128,
					"messages": []map[string]string{{"role": "user", "content": fixture.prompt}},
				}
				if stream {
					payload["stream"] = true
				}
				reply, sendErr := send()
				if sendErr == nil && reply.StopReason == "end_turn" && probeText(reply.Content) == fixture.expected {
					passed++
				}
			}
			result.Evaluation = append(result.Evaluation, probeEvaluation("coding", passed, len(anthropicCodingFixtures), start, usage, grant.ChannelModel))
		}()
	}
	first, err := send()
	if err != nil {
		return result, err
	}
	var callID string
	for _, raw := range first.Content {
		var block struct {
			Type, ID, Name string
			Input          map[string]json.RawMessage
		}
		if json.Unmarshal(raw, &block) != nil {
			return result, fmt.Errorf("invalid Anthropic content block")
		}
		switch block.Type {
		case "text":
		case "tool_use":
			if callID != "" || strings.TrimSpace(block.ID) == "" || block.Name != name || block.Input == nil || len(block.Input) != 0 {
				return result, fmt.Errorf("first round returned an unexpected tool call")
			}
			callID = block.ID
		default:
			return result, fmt.Errorf("first round returned an unsupported content block")
		}
	}
	if first.StopReason != "tool_use" || callID == "" {
		return result, fmt.Errorf("first round did not return a valid tool call")
	}
	result.TwoRound = true
	nonce := rand.Text()
	payload["messages"] = []map[string]any{
		user,
		{"role": "assistant", "content": first.Content},
		{"role": "user", "content": []map[string]string{{"type": "tool_result", "tool_use_id": callID, "content": nonce}}},
	}
	payload["tool_choice"] = map[string]string{"type": "none"}
	second, err := send()
	if err != nil {
		return result, err
	}
	if second.StopReason != "end_turn" || probeText(second.Content) != nonce {
		return result, fmt.Errorf("second round did not return the tool result")
	}
	result.Success = true
	result.VerifiedAt = time.Now()
	return result, nil
}

var anthropicCodingFixtures = []struct{ prompt, expected string }{
	{"In Python, a function def count_items(items): return len(items)-1 fails count_items([])==0 and count_items([1,2])==2. Return only the corrected return statement as plain text.", "return len(items)"},
	{"In JavaScript, const count = xs => xs.filter(x => x > 0).length must also count zero. Return only the corrected comparison expression as plain text.", "x >= 0"},
}

func probeText(content []json.RawMessage) string {
	var answer strings.Builder
	for _, raw := range content {
		var block struct{ Type, Text string }
		if json.Unmarshal(raw, &block) != nil || block.Type != "text" {
			return ""
		}
		answer.WriteString(block.Text)
	}
	return strings.TrimSpace(answer.String())
}
