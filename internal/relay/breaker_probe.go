package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

const breakerTextProtocols = model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage

// ProbeBreakerTarget performs one bounded, non-streaming text completion. It deliberately
// bypasses Forward: no user reservation, usage, billing, or media endpoint is involved.
// keyID or modelID identifies the claimed entity; the other must be zero.
func ProbeBreakerTarget(ctx context.Context, channelID, keyID, modelID int) error {
	if channelID <= 0 || (keyID <= 0) == (modelID <= 0) {
		return fmt.Errorf("invalid breaker probe target")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).Preload("Egresses").First(&channel, channelID).Error; err != nil {
		return err
	}
	if !channel.Enabled {
		return fmt.Errorf("probe channel is disabled")
	}
	// Parameter overrides may inject tools, media, or replace the token cap. Fail closed rather
	// than execute an administrator's arbitrary production payload during background recovery.
	if strings.TrimSpace(channel.ParamOverride) != "" {
		return fmt.Errorf("probe cannot safely apply parameter overrides")
	}
	q := db.GetDB().WithContext(ctx).Preload("ChannelKey").Preload("ChannelModel")
	if keyID > 0 {
		q = q.Where("channel_key_id = ?", keyID)
	} else {
		q = q.Where("channel_model_id = ?", modelID)
	}
	var grants []model.ChannelGrant
	if err := q.Order("id ASC").Find(&grants).Error; err != nil {
		return err
	}
	for _, grant := range grants {
		key, cm := grant.ChannelKey, grant.ChannelModel
		if key == nil || cm == nil || key.ChannelID != channelID || cm.ChannelID != channelID || !key.Enabled || strings.TrimSpace(key.Key) == "" || cm.Kind != model.MediaKindText || strings.TrimSpace(cm.Name) == "" {
			continue
		}
		if capability, ok := op.CapabilityGet(cm.Name); ok && (capability.OutputsImage() || capability.OutputsVideo() || !capability.OutputsText()) {
			continue
		}
		if grant.Protocols&(model.ProtocolOpenAIImage|model.ProtocolOpenAIVideo) != 0 {
			continue
		}
		if grant.Protocols&breakerTextProtocols == 0 {
			continue
		}
		// Never use a second broken component to validate the claimed one, even if expired.
		if keyID > 0 && cm.DisabledUntil != nil || modelID > 0 && key.DisabledUntil != nil {
			continue
		}
		return probeBreakerGrant(ctx, channel, grant)
	}
	return fmt.Errorf("no safe text probe target for channel %d", channelID)
}

func probeBreakerGrant(ctx context.Context, channel model.Channel, grant model.ChannelGrant) error {
	outbound, protocol, _, err := buildOutbound(channel, grant, *grant.ChannelKey, model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion)
	if err != nil {
		return err
	}
	if protocol&breakerTextProtocols == 0 {
		return fmt.Errorf("unsafe probe protocol")
	}
	body, err := json.Marshal(map[string]interface{}{"model": grant.ChannelModel.Name, "messages": []map[string]string{{"role": "user", "content": "Reply OK."}}, "max_tokens": 1, "stream": false})
	if err != nil {
		return err
	}
	raw := &httpclient.Request{Method: http.MethodPost, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
	egress, _, err := pickEgress(channel, "")
	if err != nil {
		return err
	}
	// Conversion executes the existing outbound transformer, including HTTP signing, and
	// the normal egress/proxy/SSRF-protected client. No billing/route state is created.
	result, err := sendConverted(ctx, llm.APIFormatOpenAIChatCompletion, raw, channel, egress, outbound, false)
	if err != nil {
		return fmt.Errorf("breaker upstream probe failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if result == nil || result.events != nil {
		return fmt.Errorf("invalid probe response")
	}
	var response struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(result.body, &response); err != nil {
		return fmt.Errorf("invalid probe response JSON")
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return fmt.Errorf("probe response error")
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == nil || strings.TrimSpace(*response.Choices[0].Message.Content) == "" {
		return fmt.Errorf("probe returned no text completion")
	}
	return nil
}
