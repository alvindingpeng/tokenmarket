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
	completed := 0
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
		if stream {
			completed++
		}
		return response, nil
	}
	if evaluate {
		user["content"] = "Call switchyard_probe twice in separate turns, using an empty object each time. After the first result, call the tool again. After the second, output only the last line of the first result, a single space, and the second result."
		start := time.Now()
		defer func() {
			tool := probeEvaluation("tool", 0, 1, start, usage, grant.ChannelModel)
			if stream {
				tool.StreamsCompleted, tool.StreamsExpected = completed, 3
			}
			if result.Success {
				tool.Passed = 1
			}
			result.Evaluation = append(result.Evaluation, tool)
			usage = anthropicProbeUsage{}
			completed = 0
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
				if sendErr == nil && reply.StopReason == "end_turn" && fixture.passes(probeText(reply.Content)) {
					passed++
				}
			}
			coding := probeEvaluation("coding", passed, len(anthropicCodingFixtures), start, usage, grant.ChannelModel)
			if stream {
				coding.StreamsCompleted, coding.StreamsExpected = completed, len(anthropicCodingFixtures)
			}
			result.Evaluation = append(result.Evaluation, coding)
		}()
	}
	first, err := send()
	if err != nil {
		return result, err
	}
	callID, err := anthropicProbeCallID(first, name)
	if err != nil {
		return result, err
	}
	result.TwoRound = true
	nonce := rand.Text()
	firstResult := nonce
	if evaluate {
		firstResult = strings.Repeat("This line is fixture data.\n", 256) + nonce
	}
	messages := []map[string]any{user}
	appendResult := func(response anthropicProbeMessage, id, content string) {
		messages = append(messages,
			map[string]any{"role": "assistant", "content": response.Content},
			map[string]any{"role": "user", "content": []map[string]string{{"type": "tool_result", "tool_use_id": id, "content": content}}})
		payload["messages"] = messages
	}
	appendResult(first, callID, firstResult)
	if !evaluate {
		payload["tool_choice"] = map[string]string{"type": "none"}
	}
	second, err := send()
	if err != nil {
		return result, err
	}
	if evaluate {
		secondID, callErr := anthropicProbeCallID(second, name)
		if callErr != nil || secondID == callID {
			return result, fmt.Errorf("second round did not return a new tool call")
		}
		secondNonce := rand.Text()
		appendResult(second, secondID, secondNonce)
		nonce += " " + secondNonce
		payload["tool_choice"] = map[string]string{"type": "none"}
		second, err = send()
		if err != nil {
			return result, err
		}
	}
	if second.StopReason != "end_turn" || probeText(second.Content) != nonce {
		return result, fmt.Errorf("second round did not return the tool result")
	}
	result.Success = true
	result.VerifiedAt = time.Now()
	return result, nil
}

func anthropicProbeCallID(response anthropicProbeMessage, name string) (string, error) {
	var callID string
	for _, raw := range response.Content {
		var block struct {
			Type, ID, Name string
			Input          map[string]json.RawMessage
		}
		if json.Unmarshal(raw, &block) != nil {
			return "", fmt.Errorf("invalid Anthropic content block")
		}
		switch block.Type {
		case "text":
		case "tool_use":
			if callID != "" || strings.TrimSpace(block.ID) == "" || block.Name != name || block.Input == nil || len(block.Input) != 0 {
				return "", fmt.Errorf("first round returned an unexpected tool call")
			}
			callID = block.ID
		default:
			return "", fmt.Errorf("first round returned an unsupported content block")
		}
	}
	if response.StopReason != "tool_use" || callID == "" {
		return "", fmt.Errorf("first round did not return a valid tool call")
	}
	return callID, nil
}

const probeBuggyFile = "def is_valid_port(port):\n    return 1 <= port < 65535\n"

type codingProbeFixture struct {
	prompt   string
	expected string
}

func (fixture codingProbeFixture) passes(answer string) bool {
	if fixture.expected != "" {
		return answer == fixture.expected
	}
	var patch struct{ Find, Replace string }
	if json.Unmarshal([]byte(answer), &patch) != nil || patch.Find == "" || strings.Count(probeBuggyFile, patch.Find) != 1 {
		return false
	}
	return strings.Replace(probeBuggyFile, patch.Find, patch.Replace, 1) == "def is_valid_port(port):\n    return 1 <= port <= 65535\n"
}

var anthropicCodingFixtures = []codingProbeFixture{
	{"In Python, a function def count_items(items): return len(items)-1 fails count_items([])==0 and count_items([1,2])==2. Return only the corrected return statement as plain text.", "return len(items)"},
	{"In JavaScript, const count = xs => xs.filter(x => x > 0).length must also count zero. Return only the corrected comparison expression as plain text.", "x >= 0"},
	{"Fix this Python file so it accepts ports 1 through 65535 inclusive. Return only a JSON object with find and replace strings describing one exact substitution. File:\n" + probeBuggyFile, ""},
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
