package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/model").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/channel", http.MethodGet).
				Handle(getChannelModelList),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getLLMModelList),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateLLMModel),
		).
		AddRoute(
			router.NewRoute("/delete/:name", http.MethodDelete).
				Handle(deleteLLMModel),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createLLMModel),
		)
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/models", http.MethodGet).
				Handle(getModelList),
		)
}

func getChannelModelList(c *gin.Context) {
	channelModels, err := op.ChannelModelList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channelModels)
}

func getModelList(c *gin.Context) {
	models, err := op.GroupListModel(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	if c.GetString("request_type") == "anthropic" {
		var anthropicModels []model.AnthropicModel
		for _, m := range models {
			anthropicModels = append(anthropicModels, model.AnthropicModel{
				ID:          m,
				CreatedAt:   "2024-01-01T00:00:00Z",
				DisplayName: m,
				Type:        "model",
			})
		}
		c.JSON(200, gin.H{
			"data":     anthropicModels,
			"first_id": anthropicModels[0].ID,
			"has_more": false,
			"last_id":  anthropicModels[len(anthropicModels)-1].ID,
		})
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

func getLLMModelList(c *gin.Context) {
	models, err := op.LLMModelList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, models)
}

func updateLLMModel(c *gin.Context) {
	var model model.LLMModel
	if err := c.ShouldBindJSON(&model); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.LLMModelUpdate(&model, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, model)
}

func createLLMModel(c *gin.Context) {
	var model model.LLMModel
	if err := c.ShouldBindJSON(&model); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.LLMModelCreate(&model, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, model)
}

func deleteLLMModel(c *gin.Context) {
	modelName := c.Param("name")
	if err := op.LLMModelDelete(modelName, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}
