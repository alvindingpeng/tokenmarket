package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
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
				OwnedBy: "octopus",
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

func updateLLMPrice(c *gin.Context) {
	err := price.UpdateLLMPrice(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "model.update-price", "", "sync from models.dev")
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
// 同名模型跨渠道的多份观测在此归并(按样本数加权), 供模型页卡片展示。
func listModelScores(c *gin.Context) {
	views := relay.ModelScoreViews()
	userID, role := middleware.CurrentUser(c)
	briefs := op.ChannelModelBriefs(model.Scope{ID: userID, Role: role})
	byID := make(map[int]op.ChannelModelBrief, len(briefs))
	for _, b := range briefs {
		byID[b.ID] = b
	}
	type channelRef struct {
		Channel string  `json:"channel"`
		WaitMs  float64 `json:"wait_ms"`
		Success float64 `json:"success"`
		Samples int     `json:"samples"`
	}
	type modelScore struct {
		Name     string       `json:"name"`
		WaitMs   float64      `json:"wait_ms"`
		Success  float64      `json:"success"`
		Samples  int          `json:"samples"`
		Channels []channelRef `json:"channels"`
	}
	agg := make(map[string]*modelScore)
	for _, v := range views {
		brief, ok := byID[v.ChannelModelID]
		if !ok {
			continue
		}
		key := strings.ToLower(brief.Name)
		entry := agg[key]
		if entry == nil {
			entry = &modelScore{Name: brief.Name}
			agg[key] = entry
		}
		entry.Channels = append(entry.Channels, channelRef{Channel: brief.ChannelName, WaitMs: v.WaitMs, Success: v.Success, Samples: v.Samples})
		entry.Samples += v.Samples
	}
	out := make([]modelScore, 0, len(agg))
	for _, e := range agg {
		var wSum, sSum float64
		for _, ch := range e.Channels {
			wSum += ch.WaitMs * float64(ch.Samples)
			sSum += ch.Success * float64(ch.Samples)
		}
		if e.Samples > 0 {
			e.WaitMs = wSum / float64(e.Samples)
			e.Success = sSum / float64(e.Samples)
		}
		sort.Slice(e.Channels, func(i, j int) bool { return e.Channels[i].Channel < e.Channels[j].Channel })
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	resp.Success(c, out)
}
