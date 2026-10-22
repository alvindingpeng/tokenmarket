package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
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
	resp.Success(c, op.LLMList())
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

func updateLLMPrice(c *gin.Context) {
	ctx := c.Request.Context()
	// 一次抓取同时更新价格表与模型能力登记: 能力登记决定探测渠道模型时预勾选的协议位。
	capabilities, err := price.UpdateLLMPrice(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	capabilityCount := 0
	if len(capabilities) > 0 {
		if err := op.CapabilityReplaceFromModelsDev(capabilities, ctx); err != nil {
			resp.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		capabilityCount = len(capabilities)
	}
	audit(c, "model.update-price", "", fmt.Sprintf("sync from models.dev, capabilities=%d", capabilityCount))
	resp.Success(c, nil)
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
