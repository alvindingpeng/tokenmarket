package relay

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

type autoTask struct {
	name          string
	input, output int64
}

func classifyAuto(body []byte, protocol model.Protocol, input, output int64) autoTask {
	task := autoTask{name: "unsupported", input: input, output: output}
	if protocol != model.ProtocolOpenAIChatCompletion {
		return task
	}
	var request struct {
		Messages []struct {
			Role    string
			Content *string
		}
		Tools          json.RawMessage
		Functions      json.RawMessage
		ToolChoice     json.RawMessage `json:"tool_choice"`
		FunctionCall   json.RawMessage `json:"function_call"`
		Modalities     []string
		ResponseFormat struct {
			Type   string
			Schema json.RawMessage `json:"json_schema"`
		} `json:"response_format"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Messages) == 0 ||
		len(request.Tools)+len(request.Functions)+len(request.ToolChoice)+len(request.FunctionCall) != 0 ||
		(len(request.Modalities) != 0 && (len(request.Modalities) != 1 || request.Modalities[0] != "text")) {
		return task
	}
	var last string
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer", "assistant", "user":
			if message.Content == nil {
				return task
			}
		default:
			return task
		}
		if message.Role == "user" {
			last = *message.Content
		}
	}
	if strings.TrimSpace(last) == "" {
		return task
	}
	var nonASCII int64
	for _, value := range body {
		if value >= 128 {
			nonASCII++
		}
	}
	task.input = max(input, (int64(len(body))-nonASCII)/4+nonASCII+32)
	switch request.ResponseFormat.Type {
	case "json_schema":
		if len(request.ResponseFormat.Schema) != 0 && request.ResponseFormat.Schema[0] == '{' {
			task.name = "extraction"
		}
	case "", "text":
		task.name = "general"
		last = strings.ToLower(strings.TrimSpace(last))
		for _, prefix := range []string{"summarize", "summarise", "summary of", "请总结", "总结", "请摘要", "摘要", "概括"} {
			if strings.HasPrefix(last, prefix) {
				task.name = "summary"
				break
			}
		}
	}
	return task
}

func autoCandidates(group model.Group, task autoTask) (model.Group, []model.RelayRouteCandidate) {
	config := group.RelayConfig
	qualified := config.AutoGeneral
	switch task.name {
	case "summary":
		if config.AutoSummary != "" {
			qualified = config.AutoSummary
		}
	case "extraction":
		qualified = config.AutoExtract
	case "unsupported":
		qualified = ""
	}
	grants := map[int]bool{}
	for _, value := range strings.Split(qualified, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && id > 0 {
			grants[id] = true
		}
	}
	items := group.Items
	group.Items = make([]model.GroupItem, 0, len(items))
	group.AutoRequestCosts = make(map[int]float64, len(items))
	var excluded []model.RelayRouteCandidate
	for _, item := range items {
		reason := autoExclusion(item, task, grants[item.ChannelGrantID])
		if reason != "" {
			excluded = append(excluded, model.RelayRouteCandidate{ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName, Reason: reason})
			continue
		}
		group.Items = append(group.Items, item)
		group.AutoRequestCosts[item.ID] = (float64(task.input)*item.UserPrice.Input + float64(task.output)*item.UserPrice.Output) / 1e6
	}
	return group, excluded
}

func autoExclusion(item model.GroupItem, task autoTask, qualified bool) string {
	if !qualified {
		return "auto_not_qualified"
	}
	if !item.Available || item.Protocols&model.ProtocolOpenAIChatCompletion == 0 || item.Kind != model.MediaKindText {
		return "auto_unavailable_or_incompatible"
	}
	grant, err := op.ChannelGrantGet(item.ChannelGrantID)
	if err != nil || grant.Protocols&model.ProtocolOpenAIChatCompletion == 0 {
		return "auto_unavailable_or_incompatible"
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil || (channel.Dialect != "" && channel.Dialect != model.DialectGeneric) || strings.TrimSpace(channel.ParamOverride) != "" {
		return "auto_unavailable_or_incompatible"
	}
	reference := op.ModelReferenceGet(grant.ChannelModel.Name)
	limit, maxOutput := grant.ChannelModel.ContextLength, reference.MaxOutput
	if limit == 0 {
		limit = reference.ContextLength
	}
	if capability, ok := op.CapabilityGet(grant.ChannelModel.Name); ok && capability.Source == model.CapabilitySourceManual && strings.EqualFold(capability.Name, grant.ChannelModel.Name) {
		if capability.Context > 0 {
			limit = capability.Context
		}
		if capability.MaxOutput > 0 {
			maxOutput = capability.MaxOutput
		}
	}
	if limit <= 0 {
		return "auto_context_unknown"
	}
	if task.output > int64(limit)-task.input || (maxOutput > 0 && task.output > int64(maxOutput)) {
		return "auto_token_limit"
	}
	return ""
}
