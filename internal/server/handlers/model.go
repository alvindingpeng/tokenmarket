package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func init() {
	router.NewGroupRouter("/api/v1/model").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listLLM),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(createLLM),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(updateLLM),
		).
		AddRoute(
			router.NewRoute("/delete", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(deleteLLM),
		).
		// 模型级自动添加条件: 按登录用户各存一份, 故与渠道路由同一档权限 ——
		// 谁管渠道谁就该能按模型调条件, 管理员与渠道商都要能进。
		AddRoute(
			router.NewRoute("/auto-add-rule/list", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(listAutoAddRules),
		).
		AddRoute(
			router.NewRoute("/auto-add-rule/save", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(saveAutoAddRule),
		).
		AddRoute(
			router.NewRoute("/auto-add-rule/delete", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(deleteAutoAddRule),
		).
		AddRoute(
			router.NewRoute("/update-price", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(updateLLMPrice),
		).
		AddRoute(
			router.NewRoute("/rebuild-price", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(rebuildLLMPrice),
		).
		AddRoute(
			router.NewRoute("/last-update-time", http.MethodGet).
				Handle(getLastUpdateTime),
		).
		AddRoute(
			router.NewRoute("/scores", http.MethodGet).
				Handle(listModelScores),
		).
		AddRoute(
			router.NewRoute("/capability", http.MethodGet).
				Handle(listModelCapability),
		).
		AddRoute(
			router.NewRoute("/capability/save", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(saveModelCapability),
		).
		AddRoute(
			router.NewRoute("/capability/delete", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(deleteModelCapability),
		).
		AddRoute(
			router.NewRoute("/task-capability", http.MethodGet).
				Handle(listModelTaskCapability),
		).
		AddRoute(
			router.NewRoute("/task-capability/save", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(saveModelTaskCapability),
		).
		AddRoute(
			router.NewRoute("/task-capability/delete", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(deleteModelTaskCapability),
		).
		AddRoute(
			router.NewRoute("/task-capability/probe-tool", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(probeModelToolCapability),
		)
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/models", http.MethodGet).
				Handle(getModelList),
		)
}

func getModelList(c *gin.Context) {
	// /v1/models 列出该 API Key 归属者自己的分组名(客户端模型名)。
	ownerID := uint(c.GetInt("api_key_user_id"))
	models := op.GroupListModel(model.Scope{ID: ownerID, Role: model.RoleUser})
	if allowed, ok := c.Get("supported_models"); ok {
		if names, _ := allowed.([]string); len(names) > 0 {
			models = lo.Filter(models, func(m string, _ int) bool {
				return lo.Contains(names, m)
			})
		}
	}

	if c.GetHeader("x-api-key") != "" {
		var anthropicModels []model.AnthropicModel
		for _, m := range models {
			anthropicModels = append(anthropicModels, model.AnthropicModel{
				ID:          m,
				CreatedAt:   "2024-01-01T00:00:00Z",
				DisplayName: m,
				Type:        "model",
			})
		}
		response := gin.H{
			"data":     anthropicModels,
			"has_more": false,
		}
		if len(anthropicModels) > 0 {
			response["first_id"] = anthropicModels[0].ID
			response["last_id"] = anthropicModels[len(anthropicModels)-1].ID
		}
		c.JSON(200, response)
	} else {
		var openAIModels []model.OpenAIModel
		for _, m := range models {
			openAIModels = append(openAIModels, model.OpenAIModel{
				ID:      m,
				Object:  "model",
				Created: 1763395200,
				OwnedBy: "switchyard",
			})
		}
		c.JSON(200, gin.H{
			"success": true,
			"data":    openAIModels,
			"object":  "list",
		})
	}
}

func listLLM(c *gin.Context) {
	type catalogItem struct {
		model.LLMInfo
		Reference model.ModelReference `json:"reference,omitzero"`
	}
	list := op.LLMList()
	items := make([]catalogItem, 0, len(list))
	for _, entry := range list {
		items = append(items, catalogItem{LLMInfo: entry, Reference: op.ModelReferenceGet(entry.Name)})
	}
	resp.Success(c, items)
}

// listAutoAddRules 返回当前用户为各模型设置的自动添加条件, 供模型页逐条展示与编辑。
// 条件属于「自动添加」这一功能, 与模型一一对应, 故挂在模型路由下而不是设置路由下。
func listAutoAddRules(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	resp.Success(c, op.AutoAddRuleList(userID))
}

// saveAutoAddRule 保存当前用户为某个模型设的自动添加条件; 全空条件等价于删除, 由 op 层归一。
// 条件只写自己的: 请求体里的 user_id 一律忽略, 免得用户 A 覆盖用户 B 的条件。
func saveAutoAddRule(c *gin.Context) {
	var rule model.ModelAutoAddRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	rule.UserID, _ = middleware.CurrentUser(c)
	saved, err := op.AutoAddRuleSave(rule, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.autoadd-rule", saved.Name, fmt.Sprintf("mode=%s price=[%s,%s] markup=[%s,%s] fill=%s",
		saved.Mode, saved.PriceMin, saved.PriceMax, saved.MarkupMin, saved.MarkupMax, saved.FillPrice))
	resp.Success(c, saved)
}

// deleteAutoAddRule 删除当前用户为某个模型设的自动添加条件, 让它回到全局默认。
func deleteAutoAddRule(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, _ := middleware.CurrentUser(c)
	if err := op.AutoAddRuleDelete(userID, body.Name, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.autoadd-rule-delete", body.Name, "")
	resp.Success(c, nil)
}

// createLLM 校验并创建自定义模型价格。
func createLLM(c *gin.Context) {
	var model model.LLMInfo
	if err := c.ShouldBindJSON(&model); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	model.Name = strings.ToLower(strings.TrimSpace(model.Name))
	if model.Name == "" {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	// 价格校验必须在落库前: 负价会被 LLMPrice.Cost 直接乘成负费用(调用反而加钱)。
	// 媒体模型早有 Validate, 文本模型此前只查了名称。400 而不是 500 —— 这是输入问题。
	if err := model.LLMPrice.Validate(); err != nil {
		logModelWriteRejection("create", model.Name, err)
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.LLMCreate(model, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.create", model.Name, fmt.Sprintf("input=%v output=%v cache_read=%v cache_write=%v", model.Input, model.Output, model.CacheRead, model.CacheWrite))
	resp.Success(c, model)
}

// updateLLM 校验并更新自定义模型价格。
func updateLLM(c *gin.Context) {
	var model model.LLMInfo
	if err := c.ShouldBindJSON(&model); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	model.Name = strings.ToLower(strings.TrimSpace(model.Name))
	if model.Name == "" {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := model.LLMPrice.Validate(); err != nil {
		logModelWriteRejection("update", model.Name, err)
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.LLMUpdate(model, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.update", model.Name, fmt.Sprintf("input=%v output=%v cache_read=%v cache_write=%v", model.Input, model.Output, model.CacheRead, model.CacheWrite))
	resp.Success(c, model)
}

// deleteLLM 校验模型名并删除自定义模型价格。
func deleteLLM(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if req.Name == "" {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.LLMDelete(req.Name, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.delete", req.Name, "")
	resp.Success(c, nil)
}

// listModelCapability 返回模型能力登记, 供价格库页面展示与排错。
// 与价格分开: 能力登记是"这个模型输出什么", 价格是"它值多少钱", 两者来源相同但语义不同。
func listModelCapability(c *gin.Context) {
	resp.Success(c, op.CapabilityList())
}

// saveModelCapability 人工补录或覆盖一条能力登记, 来源固定记为 manual, 后续同步不会覆盖它。
func saveModelCapability(c *gin.Context) {
	var capability model.ModelCapability
	if err := c.ShouldBindJSON(&capability); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.CapabilitySaveManual(capability, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.capability-save", capability.Name,
		fmt.Sprintf("input=%s output=%s context=%d", capability.Input, capability.Output, capability.Context))
	saved, _ := op.CapabilityGet(capability.Name)
	resp.Success(c, saved)
}

// deleteModelCapability 删除一条能力登记, 供清理错误条目。
func deleteModelCapability(c *gin.Context) {
	var request struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.CapabilityDelete(request.Name, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.capability-delete", request.Name, "")
	resp.Success(c, nil)
}

func listModelTaskCapability(c *gin.Context) {
	resp.Success(c, op.TaskCapabilityList())
}

func saveModelTaskCapability(c *gin.Context) {
	var item model.ModelTaskCapability
	if err := c.ShouldBindJSON(&item); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.TaskCapabilitySave(item, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.task-capability-save", item.ModelName+"/"+item.Task,
		fmt.Sprintf("score=%d verified=%t", item.Score, item.Verified))
	saved, _ := op.TaskCapabilityGet(item.ModelName, item.Task)
	resp.Success(c, saved)
}

func deleteModelTaskCapability(c *gin.Context) {
	var request struct {
		ModelName string `json:"model_name"`
		Task      string `json:"task"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.TaskCapabilityDelete(request.ModelName, request.Task, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "model.task-capability-delete", request.ModelName+"/"+request.Task, "")
	resp.Success(c, nil)
}

func probeModelToolCapability(c *gin.Context) {
	var request model.ModelToolProbeRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.ChannelGrantID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	request.Score = min(max(request.Score, 1), 100)
	grant, err := op.ChannelGrantGet(request.ChannelGrantID)
	if err != nil || grant.ChannelModel == nil || grant.ChannelKey == nil {
		resp.Error(c, http.StatusNotFound, "channel grant not found or unavailable")
		return
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	scope := scopeOf(c)
	if !scope.Owns(channel.UserID) {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	required := model.ProtocolOpenAIChatCompletion
	if request.Protocol == "anthropic_messages" {
		required = model.ProtocolAnthropicMessage
	}
	if grant.Protocols&required == 0 ||
		(channel.Dialect != "" && channel.Dialect != model.DialectGeneric) ||
		strings.TrimSpace(channel.ParamOverride) != "" {
		resp.Error(c, http.StatusBadRequest, "tool probe requires the requested native protocol with generic channel settings")
		return
	}
	client, err := probeHTTPClientForChannel(channel)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	runProbe := runModelToolProbe
	if required == model.ProtocolAnthropicMessage {
		runProbe = runAnthropicToolProbe
	}
	result, err := runProbe(c.Request.Context(), client, channel, grant, request.Score)
	if err != nil {
		result.Error = err.Error()
		audit(c, "model.tool-probe", grant.ChannelModel.Name,
			fmt.Sprintf("grant=%d protocol=%d success=%t two_round=%t", grant.ID, required, result.Success, result.TwoRound))
		c.JSON(http.StatusBadGateway, gin.H{
			"code": http.StatusBadGateway, "message": "tool capability probe failed", "data": result,
		})
		return
	}
	audit(c, "model.tool-probe", grant.ChannelModel.Name,
		fmt.Sprintf("grant=%d protocol=%d success=true two_round=true", grant.ID, required))
	if required == model.ProtocolAnthropicMessage {
		resp.Success(c, result)
		return
	}
	if groupIDs := op.GroupsContainingGrant(grant.ID); len(groupIDs) > 0 {
		list := op.GroupList(scope)
		for _, group := range list {
			if !slices.Contains(groupIDs, group.ID) ||
				group.RelayConfig.AutoTool == "" ||
				!slices.Contains(parseGrantIDsForProbe(group.RelayConfig.AutoTool), grant.ID) {
				continue
			}
			verified := parseGrantIDsForProbe(group.RelayConfig.AutoToolVerified)
			if slices.Contains(verified, grant.ID) {
				continue
			}
			verified = append(verified, grant.ID)
			group.RelayConfig.AutoToolVerified = joinGrantIDsForProbe(verified)
			updated, updateErr := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &group.RelayConfig}, scope, c.Request.Context())
			if updateErr != nil {
				result.Error = "verified, but failed to update an auto-routing group"
				result.Success = false
				c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": result.Error, "data": result})
				return
			}
			_ = updated
		}
	}
	resp.Success(c, result)
}

// runModelToolProbe exercises a complete OpenAI Chat function round trip. The
// first request must emit a tool call. The second request replays the assistant
// call and a role=tool result, and must produce a non-empty final answer. Only a
// successful second round writes a verified tool capability.
func runModelToolProbe(ctx context.Context, client *http.Client, channel model.Channel, grant model.ChannelGrant, score int) (model.ModelToolProbeResult, error) {
	result := model.ModelToolProbeResult{
		ChannelGrantID: grant.ID, ModelName: grant.ChannelModel.Name, Score: score,
	}
	base, err := url.Parse(strings.TrimSpace(channel.BaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return result, fmt.Errorf("invalid channel base URL")
	}
	path := strings.TrimSpace(channel.OpenAIChatCompletionPath)
	if path == "" {
		path = "/v1/chat/completions"
	}
	endpoint := base.ResolveReference(&url.URL{Path: path})
	probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	tool := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "switchyard_probe",
			"description": "Return the supplied probe nonce verbatim.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"nonce": map[string]any{"type": "string"}},
				"required":   []string{"nonce"},
			},
		},
	}
	nonce := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	first := map[string]any{
		"model":       grant.ChannelModel.Name,
		"messages":    []map[string]any{{"role": "user", "content": "Call switchyard_probe with nonce " + nonce + ". Do not answer directly."}},
		"tools":       []map[string]any{tool},
		"tool_choice": "auto",
		"max_tokens":  128,
		"temperature": 0,
	}
	firstResponse, err := postToolProbe(probeCtx, client, endpoint.String(), channel, grant.ChannelKey.Key, first, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return result, err
	}
	call, ok := extractProbeToolCall(firstResponse)
	if !ok {
		return result, fmt.Errorf("first round did not emit a valid tool call")
	}
	result.TwoRound = true
	second := map[string]any{
		"model": grant.ChannelModel.Name,
		"messages": []map[string]any{
			{"role": "user", "content": "Call switchyard_probe with nonce " + nonce + "."},
			{"role": "assistant", "content": nil, "tool_calls": []map[string]any{call}},
			{"role": "tool", "tool_call_id": call["id"], "content": nonce},
		},
		"tools":       []map[string]any{tool},
		"tool_choice": "none",
		"max_tokens":  128,
		"temperature": 0,
	}
	secondResponse, err := postToolProbe(probeCtx, client, endpoint.String(), channel, grant.ChannelKey.Key, second, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return result, err
	}
	if !probeHasFinalAnswer(secondResponse) {
		return result, fmt.Errorf("second round did not produce a final answer")
	}
	result.Success = true
	result.VerifiedAt = time.Now()
	if err := op.TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: grant.ChannelModel.Name, Task: model.AutoRoutingTaskTool, Score: score,
		Verified: true, Notes: "verified by two-round OpenAI Chat tool probe",
	}, ctx); err != nil {
		return result, fmt.Errorf("save verified tool capability: %w", err)
	}
	return result, nil
}

func postToolProbe(ctx context.Context, client *http.Client, endpoint string, channel model.Channel, key string, payload any, protocol model.Protocol) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if protocol == model.ProtocolAnthropicMessage {
		request.Header.Set("X-Api-Key", key)
		request.Header.Set("Anthropic-Version", "2023-06-01")
	} else {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for _, header := range channel.CustomHeader {
		if header.HeaderKey != "" {
			request.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return nil, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream tool probe returned HTTP %d", response.StatusCode)
	}
	return responseBody, nil
}

func extractProbeToolCall(body []byte) (map[string]any, bool) {
	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []map[string]any `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Choices) == 0 {
		return nil, false
	}
	call := response.Choices[0].Message.ToolCalls
	if len(call) == 0 || strings.TrimSpace(fmt.Sprint(call[0]["id"])) == "" {
		return nil, false
	}
	return call[0], true
}

func probeHasFinalAnswer(body []byte) bool {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	return json.Unmarshal(body, &response) == nil && len(response.Choices) > 0 &&
		strings.TrimSpace(response.Choices[0].Message.Content) != ""
}

func parseGrantIDsForProbe(value string) []int {
	ids := make([]int, 0)
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && id > 0 && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func joinGrantIDsForProbe(ids []int) string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			values = append(values, strconv.Itoa(id))
		}
	}
	return strings.Join(values, ",")
}

func updateLLMPrice(c *gin.Context) {
	result, err := price.SyncModelCatalog(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.update-price", "", fmt.Sprintf("sync from models.dev, references=%d channel_models_updated=%d", result.References, result.ChannelModelsUpdated))
	resp.Success(c, result)
}

// rebuildLLMPrice 清理幽灵模型并重新校准数据库中的剩余模型价格。
func rebuildLLMPrice(c *gin.Context) {
	ctx := c.Request.Context()
	if err := op.LLMCleanupGhosts(ctx); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	llmInfos := op.LLMList()
	for i := range llmInfos {
		llmInfos[i].LLMPrice = model.LLMPrice{}
		if modelPrice := price.GetLLMPrice(llmInfos[i].Name); modelPrice != nil {
			llmInfos[i].LLMPrice = *modelPrice
		}
	}
	if err := op.LLMBatchSave(llmInfos, ctx); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.rebuild-price", "", fmt.Sprintf("count=%d", len(llmInfos)))
	resp.Success(c, gin.H{"count": len(llmInfos)})
}

func getLastUpdateTime(c *gin.Context) {
	time := price.GetLastUpdateTime()
	resp.Success(c, time)
}

// listModelScores 返回各模型名的全局选路评分: 按「渠道模型」为单位统计的首响应耗时与成功率 EMA,
// 同名模型跨渠道的多份观测按样本数加权归并, 供模型页卡片展示。
func listModelScores(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	views := relay.ModelScoreViews()
	briefs := op.ChannelModelBriefs(model.Scope{ID: userID, Role: role})
	resp.Success(c, relay.ModelScoreBriefs(briefs, views))
}

// logModelWriteRejection 记录被拒的模型价格写入。
//
// 与渠道写入拒绝同一教训: 只回 400 不写日志, 事后从访问日志里只能看到"有人存失败",
// 想知道是哪个模型、哪个字段越界就得靠猜。原因里不含任何凭据, 可以直接落日志。
func logModelWriteRejection(action, name string, err error) {
	log.Warnf("model %s rejected: name=%s reason=%v", action, name, err)
}
