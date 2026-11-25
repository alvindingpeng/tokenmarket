package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

type anthropicProbeUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

func (u *anthropicProbeUsage) add(other anthropicProbeUsage) {
	u.Input += other.Input
	u.Output += other.Output
	u.CacheRead += other.CacheRead
	u.CacheWrite += other.CacheWrite
}

type anthropicProbeMessage struct {
	StopReason string              `json:"stop_reason"`
	Content    []json.RawMessage   `json:"content"`
	Usage      anthropicProbeUsage `json:"usage"`
}

func probeEvaluation(task string, passed, total int, start time.Time, usage anthropicProbeUsage, channel *model.ChannelModel) model.ModelProbeEvaluation {
	result := model.ModelProbeEvaluation{Task: task, Passed: passed, Total: total, DurationMS: time.Since(start).Milliseconds()}
	if (usage.Input > 0 || usage.Output > 0) && channel.SupplyPrice != (model.LLMPrice{}) {
		cost := channel.SupplyPrice.Cost(usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite)
		result.EstimatedSupplyCost = &cost
		result.Currency = channel.PriceCurrency
		if result.Currency == "" {
			result.Currency = "USD"
		}
	}
	return result
}

func decodeAnthropicProbeStream(ctx context.Context, body []byte) (anthropicProbeMessage, error) {
	var result anthropicProbeMessage
	events := httpclient.NewDefaultSSEDecoder(ctx, io.NopCloser(bytes.NewReader(body)))
	defer events.Close()
	var block map[string]json.RawMessage
	var partial bytes.Buffer
	started := false
	bad := fmt.Errorf("incomplete or invalid Anthropic event stream")
	for events.Next() {
		if events.Current() == nil {
			continue
		}
		var event struct {
			Type    string
			Index   int
			Message anthropicProbeMessage
			Usage   anthropicProbeUsage
			Block   map[string]json.RawMessage `json:"content_block"`
			Delta   struct {
				Type, Text  string
				StopReason  string `json:"stop_reason"`
				PartialJSON string `json:"partial_json"`
			}
		}
		if json.Unmarshal(events.Current().Data, &event) != nil {
			return result, bad
		}
		switch event.Type {
		case "ping":
		case "message_start":
			if started {
				return result, bad
			}
			started = true
			result.Usage = event.Message.Usage
		case "content_block_start":
			if !started || block != nil || event.Block == nil || event.Index != len(result.Content) || result.StopReason != "" {
				return result, bad
			}
			block = event.Block
			partial.Reset()
			if string(block["type"]) == `"text"` {
				var initial string
				if json.Unmarshal(block["text"], &initial) != nil {
					return result, bad
				}
				partial.WriteString(initial)
			} else if string(block["type"]) != `"tool_use"` {
				return result, bad
			}
		case "content_block_delta":
			if block == nil || event.Index != len(result.Content) {
				return result, bad
			}
			switch {
			case event.Delta.Type == "text_delta" && string(block["type"]) == `"text"`:
				partial.WriteString(event.Delta.Text)
			case event.Delta.Type == "input_json_delta" && string(block["type"]) == `"tool_use"`:
				partial.WriteString(event.Delta.PartialJSON)
			default:
				return result, bad
			}
		case "content_block_stop":
			if block == nil || event.Index != len(result.Content) {
				return result, bad
			}
			if string(block["type"]) == `"text"` {
				block["text"], _ = json.Marshal(partial.String())
			} else if partial.Len() > 0 {
				block["input"] = partial.Bytes()
			}
			encoded, err := json.Marshal(block)
			if err != nil {
				return result, bad
			}
			result.Content = append(result.Content, encoded)
			block = nil
		case "message_delta":
			if !started || block != nil {
				return result, bad
			}
			if event.Delta.StopReason != "" {
				result.StopReason = event.Delta.StopReason
			}
			result.Usage.Output = event.Usage.Output
		case "message_stop":
			if block == nil && result.StopReason != "" {
				return result, nil
			}
			return result, bad
		default:
			return result, bad
		}
	}
	return result, bad
}
