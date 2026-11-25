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

func runAnthropicToolProbe(ctx context.Context, client *http.Client, channel model.Channel, grant model.ChannelGrant, _ int) (model.ModelToolProbeResult, error) {
	result := model.ModelToolProbeResult{ChannelGrantID: grant.ID, ModelName: grant.ChannelModel.Name}
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
	type message struct {
		StopReason string            `json:"stop_reason"`
		Content    []json.RawMessage `json:"content"`
	}
	send := func() (message, error) {
		var response message
		body, err := postToolProbe(probeCtx, &probeClient, endpoint, channel, grant.ChannelKey.Key, payload, model.ProtocolAnthropicMessage)
		if err != nil {
			return response, fmt.Errorf("Anthropic Messages probe request failed")
		}
		if json.Unmarshal(body, &response) != nil {
			return response, fmt.Errorf("invalid Anthropic Messages response")
		}
		return response, nil
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
	var answer strings.Builder
	for _, raw := range second.Content {
		var block struct{ Type, Text string }
		if json.Unmarshal(raw, &block) != nil || block.Type != "text" {
			return result, fmt.Errorf("second round returned an unsupported content block")
		}
		answer.WriteString(block.Text)
	}
	if second.StopReason != "end_turn" || strings.TrimSpace(answer.String()) != nonce {
		return result, fmt.Errorf("second round did not return the tool result")
	}
	result.Success = true
	result.VerifiedAt = time.Now()
	return result, nil
}
