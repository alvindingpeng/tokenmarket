package relay

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

type autoTask struct {
	name          string
	input, output int64
}

const (
	AutoTaskUnsupported = "unsupported"
	AutoTaskGeneral     = "general"
	AutoTaskSummary     = "summary"
	AutoTaskCoding      = "coding"
	AutoTaskTool        = "tool"
	AutoTaskExtraction  = "extraction"
)

var (
	// autoCodeLine matches language constructs that rarely occur in prose. The
	// classifier also accepts explicit programming words below, but avoids
	// treating every JSON-looking prompt as a coding task.
	autoCodeLine = regexp.MustCompile(`(?im)(^|\n)\s*(func |def |class |package |import |const |let |var |#include|select |from |where |<html|<!doctype )`)

	autoCodingPhrases = []string{
		"write code", "fix the code", "debug", "compile", "stack trace", "unit test",
		"pull request", "code review", "refactor", "implement ", "programming", "repository",
		"github", "git diff", "compiler error", "runtime error", "segmentation fault",
		"写代码", "改代码", "修复代码", "调试", "编译错误", "单元测试", "代码审查",
		"重构", "实现功能", "编程", "仓库", "报错日志", "堆栈", "段错误", "补丁",
	}
)

// classifyAuto performs a deliberately conservative request classification.
// A request that contains a shape this pilot cannot preserve end to end is
// unsupported instead of being routed to a plausible-looking but incompatible
// model.
func classifyAuto(body []byte, protocol model.Protocol, input, output int64) autoTask {
	task := autoTask{name: AutoTaskUnsupported, input: input, output: output}
	if protocol != model.ProtocolOpenAIChatCompletion && protocol != model.ProtocolOpenAIResponse {
		return task
	}
	var request struct {
		Messages []struct {
			Role         string
			Content      json.RawMessage
			ToolCalls    json.RawMessage `json:"tool_calls"`
			FunctionCall json.RawMessage `json:"function_call"`
		}
		Tools        json.RawMessage
		Functions    json.RawMessage
		ToolChoice   json.RawMessage `json:"tool_choice"`
		FunctionCall json.RawMessage `json:"function_call"`
		Input        json.RawMessage
		Text         struct {
			Format struct {
				Type   string
				Schema json.RawMessage `json:"schema"`
			}
		}
		Modalities     []string
		ResponseFormat struct {
			Type   string
			Schema json.RawMessage `json:"json_schema"`
		} `json:"response_format"`
	}
	if json.Unmarshal(body, &request) != nil ||
		(len(request.Modalities) != 0 && (len(request.Modalities) != 1 || request.Modalities[0] != "text")) {
		return task
	}

	var textParts []string
	var intentParts []string
	hasTools := nonEmptyJSON(request.Tools) || nonEmptyJSON(request.Functions) ||
		nonEmptyJSON(request.ToolChoice) || nonEmptyJSON(request.FunctionCall)
	hasInput := false
	for _, message := range request.Messages {
		switch message.Role {
		case "system", "developer", "assistant", "user", "tool":
			hasInput = true
		case "function":
			hasInput = true
			hasTools = true
		default:
			return task
		}
		if nonEmptyJSON(message.ToolCalls) || nonEmptyJSON(message.FunctionCall) || message.Role == "tool" || message.Role == "function" {
			hasTools = true
		}
		text, ok := autoText(message.Content)
		if !ok {
			return task
		}
		textParts = append(textParts, text)
		if message.Role == "user" || message.Role == "tool" || message.Role == "function" {
			intentParts = append(intentParts, text)
		}
	}
	if protocol == model.ProtocolOpenAIResponse {
		inputText, ok := autoResponseInputText(request.Input)
		if !ok {
			return task
		}
		if strings.TrimSpace(inputText) != "" {
			hasInput = true
			textParts = append(textParts, inputText)
			intentParts = append(intentParts, inputText)
		}
	}
	if !hasInput || len(textParts) == 0 {
		return task
	}
	var nonASCII int64
	for _, value := range body {
		if value >= 128 {
			nonASCII++
		}
	}
	task.input = max(input, (int64(len(body))-nonASCII)/4+nonASCII+32)
	last := lastUserText(request.Messages)
	if protocol == model.ProtocolOpenAIResponse {
		last = lastResponseUserText(request.Input)
	}
	if hasTools {
		// The pilot currently promises round-trip compatibility for OpenAI Chat
		// function calls. Responses requests that merely contain tool markers are
		// left unsupported until its continuation semantics have dedicated coverage.
		if protocol == model.ProtocolOpenAIResponse {
			return task
		}
		task.name = AutoTaskTool
		return task
	}
	switch request.ResponseFormat.Type {
	case "json_schema":
		if nonEmptyJSON(request.ResponseFormat.Schema) {
			task.name = AutoTaskExtraction
			return task
		}
	}
	// Response API extraction uses text.format rather than response_format.
	if protocol == model.ProtocolOpenAIResponse &&
		strings.EqualFold(request.Text.Format.Type, "json_schema") &&
		nonEmptyJSON(request.Text.Format.Schema) {
		task.name = AutoTaskExtraction
		return task
	}
	allText := strings.ToLower(strings.TrimSpace(strings.Join(intentParts, "\n")))
	if isSummaryRequest(strings.ToLower(strings.TrimSpace(last))) {
		task.name = AutoTaskSummary
		return task
	}
	if isCodingRequest(allText) {
		task.name = AutoTaskCoding
		return task
	}
	switch request.ResponseFormat.Type {
	case "", "text":
		task.name = AutoTaskGeneral
	}
	return task
}

func nonEmptyJSON(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return boundaryValue(trimmed)
}

func boundaryValue(value string) bool {
	return value != "" && value != "null" && value != "{}" && value != "[]" && value != `""`
}

// autoText accepts a string and arrays containing text parts. Any image,
// audio, file, or unknown part makes the request unsupported. This protects the
// routing pilot from stripping multimodal content during protocol conversion.
func autoText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, true
	}
	var parts []struct {
		Type string
		Text string
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var texts []string
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" && part.Type != "output_text" {
			return "", false
		}
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, "\n"), true
}

func autoResponseInputText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var input string
	if json.Unmarshal(raw, &input) == nil {
		return input, true
	}
	var parts []struct {
		Role    string
		Type    string
		Content json.RawMessage
		Text    string
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "function_call" || part.Type == "function_call_output" {
			// The pilot has no Responses continuation/function-output
			// round-trip coverage. Refuse this shape instead of converting only
			// the visible text and dropping the call state.
			return "", false
		}
		itemType := part.Type
		if itemType == "" {
			itemType = "message"
		}
		if itemType != "message" || (part.Role != "user" && part.Role != "assistant" && part.Role != "system" && part.Role != "developer") {
			return "", false
		}
		rawContent := part.Content
		if len(rawContent) == 0 && part.Text != "" {
			rawContent, _ = json.Marshal(part.Text)
		}
		text, ok := autoText(rawContent)
		if !ok {
			return "", false
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), true
}

func lastUserText(messages []struct {
	Role         string
	Content      json.RawMessage
	ToolCalls    json.RawMessage `json:"tool_calls"`
	FunctionCall json.RawMessage `json:"function_call"`
}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			text, _ := autoText(messages[i].Content)
			return text
		}
	}
	return ""
}

func lastResponseUserText(raw json.RawMessage) string {
	var parts []struct {
		Role    string
		Content json.RawMessage
		Text    string
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Role == "user" {
			content := parts[i].Content
			if len(content) == 0 && parts[i].Text != "" {
				content, _ = json.Marshal(parts[i].Text)
			}
			text, _ := autoText(content)
			return text
		}
	}
	return ""
}

func isSummaryRequest(last string) bool {
	for _, prefix := range []string{
		"summarize", "summarise", "summary of", "give me a summary", "tl;dr",
		"请总结", "总结", "请摘要", "摘要", "概括", "概述",
		"請總結", "總結", "請摘要", "摘要", "概括", "概述",
	} {
		if strings.HasPrefix(last, prefix) {
			return true
		}
	}
	return false
}

func isCodingRequest(text string) bool {
	if strings.Contains(text, "```") || autoCodeLine.MatchString(text) {
		return true
	}
	for _, phrase := range autoCodingPhrases {
		if strings.Contains(text, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

// autoHasRequiredProtocol keeps item metadata and the fresh grant record in
// agreement. The group item cache is allowed to be stale while the grant is
// re-read at routing time.
func autoHasRequiredProtocol(itemProtocols, grantProtocols model.Protocol, required model.Protocol) bool {
	return itemProtocols&required != 0 && grantProtocols&required != 0
}

func autoEffectiveProtocol(itemProtocols, grantProtocols model.Protocol, preferred model.Protocol) bool {
	if itemProtocols&preferred == 0 {
		return false
	}
	if grantProtocols&preferred != 0 {
		return true
	}
	return false
}

func autoCandidates(group model.Group, task autoTask) (model.Group, []model.RelayRouteCandidate) {
	config := group.RelayConfig
	qualified := config.AutoGeneral
	verified := map[int]bool{}
	switch task.name {
	case AutoTaskSummary:
		if config.AutoSummary != "" {
			qualified = config.AutoSummary
		}
	case AutoTaskCoding:
		qualified = config.AutoCoding
	case AutoTaskTool:
		qualified = config.AutoTool
		for _, value := range strings.Split(config.AutoToolVerified, ",") {
			if id, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && id > 0 {
				verified[id] = true
			}
		}
	case AutoTaskExtraction:
		qualified = config.AutoExtract
	case AutoTaskUnsupported:
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
		contextOverride := config.AutoContextOverrides[strconv.Itoa(item.ChannelGrantID)]
		reason := autoExclusion(item, task, grants[item.ChannelGrantID], verified[item.ChannelGrantID], contextOverride)
		if reason != "" {
			excluded = append(excluded, model.RelayRouteCandidate{ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName, Reason: reason})
			continue
		}
		group.Items = append(group.Items, item)
		group.AutoRequestCosts[item.ID] = (float64(task.input)*item.UserPrice.Input + float64(task.output)*item.UserPrice.Output) / 1e6
	}
	return group, excluded
}

func autoExclusion(item model.GroupItem, task autoTask, qualified, toolVerified bool, contextOverride int) string {
	if !qualified {
		return "auto_not_qualified"
	}
	if !item.Available || item.Kind != model.MediaKindText {
		return "auto_member_unavailable"
	}
	required := model.ProtocolOpenAIChatCompletion
	var grant model.ChannelGrant
	switch task.name {
	case AutoTaskCoding:
		if item.Protocols&(model.ProtocolOpenAIChatCompletion|model.ProtocolOpenAIResponse) == 0 {
			return "auto_protocol_incompatible"
		}
		var err error
		grant, err = op.ChannelGrantGet(item.ChannelGrantID)
		if err != nil {
			return "auto_protocol_incompatible"
		}
		if item.Protocols&model.ProtocolOpenAIChatCompletion != 0 && grant.Protocols&model.ProtocolOpenAIChatCompletion != 0 {
			required = model.ProtocolOpenAIChatCompletion
		} else {
			required = model.ProtocolOpenAIResponse
		}
	case AutoTaskTool:
		if !toolVerified {
			return "auto_tool_unverified"
		}
		// The pilot only accepts native Chat-Completions for tool round trips.
		required = model.ProtocolOpenAIChatCompletion
		if item.Protocols&required == 0 {
			return "auto_protocol_incompatible"
		}
	}
	if grant.ID == 0 {
		var err error
		grant, err = op.ChannelGrantGet(item.ChannelGrantID)
		if err != nil {
			return "auto_protocol_incompatible"
		}
	}
	if !autoHasRequiredProtocol(item.Protocols, grant.Protocols, required) ||
		!autoEffectiveProtocol(item.Protocols, grant.Protocols, required) {
		return "auto_protocol_incompatible"
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil || (channel.Dialect != "" && channel.Dialect != model.DialectGeneric) || strings.TrimSpace(channel.ParamOverride) != "" {
		return "auto_channel_incompatible"
	}
	reference := op.ModelReferenceGet(grant.ChannelModel.Name)
	// Precedence follows the operator's ability to make a deliberate correction:
	// manual capability first, explicit group override second, then the synced
	// channel field and finally the global models.dev reference.
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
	if contextOverride > 0 {
		limit = contextOverride
	}
	if limit <= 0 {
		return "auto_context_unknown"
	}
	if task.output > int64(limit)-task.input || (maxOutput > 0 && task.output > int64(maxOutput)) {
		return "auto_token_limit"
	}
	return ""
}
