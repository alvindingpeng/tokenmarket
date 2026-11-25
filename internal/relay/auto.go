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
	confidence    int
	rule          string
	protocol      model.Protocol
	stream        bool
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
	task := autoTask{name: AutoTaskUnsupported, input: input, output: output, confidence: 100, rule: "unsupported_shape", protocol: protocol}
	if protocol != model.ProtocolOpenAIChatCompletion && protocol != model.ProtocolOpenAIResponse && protocol != model.ProtocolAnthropicMessage {
		task.rule = "unsupported_protocol"
		return task
	}
	var request struct {
		Messages []struct {
			Role         string
			Content      json.RawMessage
			ToolCalls    json.RawMessage `json:"tool_calls"`
			FunctionCall json.RawMessage `json:"function_call"`
		}
		Tools              json.RawMessage
		Functions          json.RawMessage
		ToolChoice         json.RawMessage `json:"tool_choice"`
		FunctionCall       json.RawMessage `json:"function_call"`
		Input              json.RawMessage
		System             json.RawMessage
		Instructions       json.RawMessage
		Stream             bool
		PreviousResponseID string `json:"previous_response_id"`
		Conversation       json.RawMessage
		Text               struct {
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
		task.rule = "unsupported_multimodal_or_invalid"
		return task
	}
	task.stream = request.Stream
	if protocol == model.ProtocolOpenAIResponse && (request.PreviousResponseID != "" || nonEmptyJSON(request.Conversation)) {
		task.rule = "unsupported_response_state"
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
			task.rule = "unsupported_role"
			return task
		}
		if nonEmptyJSON(message.ToolCalls) || nonEmptyJSON(message.FunctionCall) || message.Role == "tool" || message.Role == "function" {
			hasTools = true
		}
		var text string
		var ok bool
		if protocol == model.ProtocolAnthropicMessage {
			var contentTools bool
			text, contentTools, ok = autoAnthropicContent(message.Content)
			if !ok {
				task.rule = "unsupported_content"
				return task
			}
			hasTools = hasTools || contentTools
		} else {
			text, ok = autoText(message.Content)
			if !ok {
				task.rule = "unsupported_content"
				return task
			}
		}
		textParts = append(textParts, text)
		if message.Role == "user" || message.Role == "tool" || message.Role == "function" {
			intentParts = append(intentParts, text)
		}
	}
	if protocol == model.ProtocolOpenAIResponse {
		inputText, inputTools, ok := autoResponseInputText(request.Input)
		if !ok {
			task.rule = "unsupported_response_input"
			return task
		}
		hasTools = hasTools || inputTools
		if strings.TrimSpace(inputText) != "" || inputTools {
			hasInput = true
			textParts = append(textParts, inputText)
			intentParts = append(intentParts, inputText)
		}
	}
	if !hasInput || len(textParts) == 0 {
		task.rule = "empty_or_unsupported_input"
		return task
	}
	task.input = 32
	for _, message := range request.Messages {
		var text string
		if protocol == model.ProtocolAnthropicMessage {
			text, _, _ = autoAnthropicContent(message.Content)
		} else {
			text, _ = autoText(message.Content)
		}
		task.input += 8 + autoTextTokens(text) + autoTextTokens(string(message.ToolCalls)) + autoTextTokens(string(message.FunctionCall))
	}
	for _, value := range []json.RawMessage{request.System, request.Instructions, request.Tools, request.Functions, request.ToolChoice, request.FunctionCall, request.Input, request.Text.Format.Schema, request.ResponseFormat.Schema} {
		task.input += autoTextTokens(string(value))
	}
	last := lastUserText(request.Messages)
	if protocol == model.ProtocolOpenAIResponse {
		last = lastResponseUserText(request.Input)
	}
	if hasTools {
		if protocol == model.ProtocolAnthropicMessage {
			if nonEmptyJSON(request.Tools) {
				var tools []struct{ Type string }
				if json.Unmarshal(request.Tools, &tools) != nil {
					task.rule = "unsupported_anthropic_tool_type"
					return task
				}
				for _, tool := range tools {
					if tool.Type != "" && tool.Type != "custom" {
						task.rule = "unsupported_anthropic_tool_type"
						return task
					}
				}
			}
		}
		if protocol == model.ProtocolOpenAIResponse && nonEmptyJSON(request.Tools) {
			var tools []struct{ Type string }
			if json.Unmarshal(request.Tools, &tools) != nil {
				return task
			}
			for _, tool := range tools {
				if tool.Type != "function" {
					task.rule = "unsupported_responses_tool_type"
					return task
				}
			}
		}
		task.name = AutoTaskTool
		task.confidence = 100
		task.rule = "tools_or_tool_history"
		return task
	}
	switch request.ResponseFormat.Type {
	case "json_schema":
		if nonEmptyJSON(request.ResponseFormat.Schema) {
			task.name = AutoTaskExtraction
			task.confidence = 100
			task.rule = "response_format_json_schema"
			return task
		}
	}
	// Response API extraction uses text.format rather than response_format.
	if protocol == model.ProtocolOpenAIResponse &&
		strings.EqualFold(request.Text.Format.Type, "json_schema") &&
		nonEmptyJSON(request.Text.Format.Schema) {
		task.name = AutoTaskExtraction
		task.confidence = 100
		task.rule = "response_text_format_json_schema"
		return task
	}
	allText := strings.ToLower(strings.TrimSpace(strings.Join(intentParts, "\n")))
	if isSummaryRequest(strings.ToLower(strings.TrimSpace(last))) {
		task.name = AutoTaskSummary
		task.confidence = 90
		task.rule = "summary_prefix"
		return task
	}
	if isCodingRequest(allText) {
		task.name = AutoTaskCoding
		task.confidence = 85
		task.rule = "code_or_programming_intent"
		return task
	}
	switch request.ResponseFormat.Type {
	case "", "text":
		task.name = AutoTaskGeneral
		task.confidence = 60
		task.rule = "default_text"
	}
	return task
}

func autoTextTokens(value string) int64 {
	var ascii, other int64
	for _, char := range value {
		if char < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+2)/3 + 2*other
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

// autoAnthropicContent parses Anthropic Messages content strings or block arrays,
// allowing text, tool_use, and text-only tool_result blocks while rejecting images,
// documents, or unknown multimodal blocks.
func autoAnthropicContent(raw json.RawMessage) (string, bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, true
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, false, true
	}
	var blocks []struct {
		Type    string
		Text    string
		Name    string
		Input   json.RawMessage
		Content json.RawMessage
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false, false
	}
	var texts []string
	hasTools := false
	for _, block := range blocks {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "tool_use":
			hasTools = true
			if block.Name != "" {
				texts = append(texts, block.Name)
			}
			if nonEmptyJSON(block.Input) {
				texts = append(texts, string(block.Input))
			}
		case "tool_result":
			hasTools = true
			resultText, ok := autoText(block.Content)
			if !ok {
				return "", false, false
			}
			if resultText != "" {
				texts = append(texts, resultText)
			}
		default:
			return "", false, false
		}
	}
	return strings.Join(texts, "\n"), hasTools, true
}

func autoResponseInputText(raw json.RawMessage) (string, bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, true
	}
	var input string
	if json.Unmarshal(raw, &input) == nil {
		return input, false, true
	}
	var parts []struct {
		Role             string
		Type             string
		Content          json.RawMessage
		Text             string
		CallID           string `json:"call_id"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false, false
	}
	var texts []string
	hasTools := false
	calls := make(map[string]bool)
	for _, part := range parts {
		if part.Type == "reasoning" && part.EncryptedContent != "" {
			continue
		}
		if part.Type == "function_call" || part.Type == "function_call_output" {
			if part.CallID == "" || (part.Type == "function_call_output" && !calls[part.CallID]) {
				return "", false, false
			}
			if part.Type == "function_call" {
				calls[part.CallID] = true
			}
			hasTools = true
			continue
		}
		itemType := part.Type
		if itemType == "" {
			itemType = "message"
		}
		if itemType != "message" || (part.Role != "user" && part.Role != "assistant" && part.Role != "system" && part.Role != "developer") {
			return "", false, false
		}
		rawContent := part.Content
		if len(rawContent) == 0 && part.Text != "" {
			rawContent, _ = json.Marshal(part.Text)
		}
		text, ok := autoText(rawContent)
		if !ok {
			return "", false, false
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), hasTools, true
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

type autoCandidateResult struct {
	Group            model.Group
	Excluded         []model.RelayRouteCandidate
	QualityRejected  int
	Unverified       int
	ExploreCandidate []model.GroupItem
}

func autoCandidates(group model.Group, task autoTask) (model.Group, []model.RelayRouteCandidate) {
	result := analyzeAutoCandidates(group, task)
	return result.Group, result.Excluded
}

// analyzeAutoCandidates 把资格过滤与质量门槛的中间结果保留下来。删除与回退必须
// 依据同一份证据决定，不能先丢掉候选再靠原因字符串猜测是否存在“未验证”成员。
func analyzeAutoCandidates(group model.Group, task autoTask) autoCandidateResult {
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
		verifiedGrants := config.AutoToolVerified
		switch task.protocol {
		case model.ProtocolOpenAIResponse:
			verifiedGrants = config.AutoResponsesToolVerified
		case model.ProtocolAnthropicMessage:
			verifiedGrants = config.AutoAnthropicToolVerified
		}
		for _, value := range strings.Split(verifiedGrants, ",") {
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
	group.AutoTaskScores = make(map[int]int, len(items))
	group.AutoTaskVerified = make(map[int]bool, len(items))
	group.AutoPerformancePenalty = make(map[int]float64, len(items))
	var excluded []model.RelayRouteCandidate
	var qualityRejected, unverified int
	var exploreCandidates []model.GroupItem
	for _, item := range items {
		contextOverride := config.AutoContextOverrides[strconv.Itoa(item.ChannelGrantID)]
		reason := autoExclusion(item, task, grants[item.ChannelGrantID], verified[item.ChannelGrantID], contextOverride)
		if reason != "" {
			excluded = append(excluded, model.RelayRouteCandidate{ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName, Reason: reason})
			continue
		}
		taskCapability, hasTaskCapability := op.TaskCapabilityGet(item.ModelName, task.name)
		taskScore := -1
		qualityEligible := true
		if hasTaskCapability {
			taskScore = taskCapability.Score
			qualityEligible = taskCapability.Verified
		}
		// 门槛为 0 是 v1.16.7 兼容模式: 管理员没有声明质量要求时不改变原有选路。
		// 一旦设置门槛，未验证和低于门槛的模型都不能进入候选，避免“便宜但不会做”的模型
		// 仅因价格取胜。
		if config.AutoMinTaskScore > 0 {
			switch {
			case !hasTaskCapability || !taskCapability.Verified:
				reason = "auto_quality_unverified"
				unverified++
			case taskCapability.Score < config.AutoMinTaskScore:
				reason = "auto_quality_below_threshold"
				qualityRejected++
			}
		}
		if reason != "" {
			excluded = append(excluded, model.RelayRouteCandidate{
				ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName,
				Reason: reason, TaskScore: taskScore, QualityEligible: qualityEligible,
			})
			continue
		}
		group.Items = append(group.Items, item)
		group.AutoRequestCosts[item.ID] = (float64(task.input)*item.UserPrice.Input + float64(task.output)*item.UserPrice.Output) / 1e6
		group.AutoTaskScores[item.ID] = taskScore
		group.AutoTaskVerified[item.ID] = hasTaskCapability && taskCapability.Verified
		group.AutoPerformancePenalty[item.ID] = autoPerformancePenalty(group.ID, item.ID, task)
	}
	// 受控探索只考虑通过协议、上下文、渠道形态等安全过滤、但缺质量证据的文本任务成员。
	// 工具任务不进入探索池，因为工具 -- 多轮往返不是价格或样本数量能替代的能力验证。
	if task.name != AutoTaskUnsupported && task.name != AutoTaskTool && config.AutoExplorePercent > 0 {
		for _, item := range items {
			contextOverride := config.AutoContextOverrides[strconv.Itoa(item.ChannelGrantID)]
			if autoExclusion(item, task, grants[item.ChannelGrantID], verified[item.ChannelGrantID], contextOverride) != "" {
				continue
			}
			capability, ok := op.TaskCapabilityGet(item.ModelName, task.name)
			if !ok || (!capability.Verified && capability.Score < config.AutoMinTaskScore) {
				exploreCandidates = append(exploreCandidates, item)
			}
		}
	}
	return autoCandidateResult{
		Group: group, Excluded: excluded, QualityRejected: qualityRejected,
		Unverified: unverified, ExploreCandidate: exploreCandidates,
	}
}

// autoTaskDecisionDetails 是写入尝试日志的结构化证据。字段只使用 JSON 基本类型，
// 这样历史日志在没有这些字段时仍能反序列化，新增字段也不要求数据库迁移。
type autoTaskDecisionDetails struct {
	ClassConfidence int                         `json:"class_confidence"`
	ClassRule       string                      `json:"class_rule"`
	EstimatedInput  int64                       `json:"estimated_input"`
	EstimatedOutput int64                       `json:"estimated_output"`
	Prediction      *autoPredictionDetails      `json:"prediction,omitempty"`
	Candidates      []model.RelayRouteCandidate `json:"candidates,omitempty"`
	RouteAction     string                      `json:"route_action,omitempty"`
	Sticky          *autoStickyDetails          `json:"sticky,omitempty"`
}

type autoPredictionDetails struct {
	ItemID    int     `json:"item_id"`
	Model     string  `json:"model"`
	CostUSD   float64 `json:"cost_usd"`
	TaskScore int     `json:"task_score"`
}

type autoStickyDetails struct {
	ItemID  int    `json:"item_id"`
	Model   string `json:"model"`
	Outcome string `json:"outcome"`
}

func autoDecisionDetailsJSON(task autoTask, prediction *autoPredictionDetails, candidates []model.RelayRouteCandidate, action string, sticky *autoStickyDetails) string {
	details := autoTaskDecisionDetails{
		ClassConfidence: task.confidence,
		ClassRule:       task.rule,
		EstimatedInput:  task.input,
		EstimatedOutput: task.output,
		Prediction:      prediction,
		Candidates:      candidates,
		RouteAction:     action,
		Sticky:          sticky,
	}
	raw, _ := json.Marshal(details)
	return string(raw)
}

// autoCandidatesForDetails 生成实际选路时的候选证据。excluded 已并入 decision，
// 所以这里只输出 qualified 成员的分数和估算成本；FinalScore 由调用方传入的
// 最终候选集计算，避免诊断看板与真实选路使用两套公式。
func autoCandidatesForDetails(group model.Group, finalScores map[int]float64) []model.RelayRouteCandidate {
	items := make([]model.RelayRouteCandidate, 0, len(group.Items))
	for _, item := range group.Items {
		items = append(items, model.RelayRouteCandidate{
			ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName,
			Reason: "auto_eligible", TaskScore: group.AutoTaskScores[item.ID],
			QualityEligible: true, EstimatedCost: group.AutoRequestCosts[item.ID],
			FinalScore: finalScores[item.ID],
		})
	}
	return items
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
		if task.protocol == model.ProtocolAnthropicMessage {
			break
		}
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
		required = model.ProtocolOpenAIChatCompletion
		switch task.protocol {
		case model.ProtocolOpenAIResponse:
			required = model.ProtocolOpenAIResponse
		case model.ProtocolAnthropicMessage:
			required = model.ProtocolAnthropicMessage
		}
		if item.Protocols&required == 0 {
			return "auto_protocol_incompatible"
		}
	}
	if task.protocol == model.ProtocolAnthropicMessage {
		required = model.ProtocolAnthropicMessage
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
