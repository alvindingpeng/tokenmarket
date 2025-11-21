package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

var llmModelCache = cache.New[string, [2]float64](16)

func LLMModelList(ctx context.Context) ([]model.LLMModel, error) {
	models := []model.LLMModel{}
	for m, prices := range llmModelCache.GetAll() {
		models = append(models, model.LLMModel{
			Model:       m,
			InputPrice:  prices[0],
			OutputPrice: prices[1],
		})
	}
	return models, nil
}

func LLMModelUpdate(model *model.LLMModel, ctx context.Context) error {
	oldModel, ok := llmModelCache.Get(model.Model)
	if !ok {
		return fmt.Errorf("model not found")
	}
	if oldModel[0] == model.InputPrice && oldModel[1] == model.OutputPrice {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Save(model).Error; err != nil {
		return err
	}
	llmModelCache.Set(model.Model, [2]float64{model.InputPrice, model.OutputPrice})
	return nil
}

func LLMModelDelete(modelName string, ctx context.Context) error {
	_, ok := llmModelCache.Get(modelName)
	if !ok {
		return fmt.Errorf("model not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.LLMModel{Model: modelName}).Error; err != nil {
		return err
	}
	llmModelCache.Del(modelName)
	return nil
}

func LLMModelCreate(model *model.LLMModel, ctx context.Context) error {
	_, ok := llmModelCache.Get(model.Model)
	if ok {
		return fmt.Errorf("model already exists")
	}
	if err := db.GetDB().WithContext(ctx).Create(model).Error; err != nil {
		return err
	}
	llmModelCache.Set(model.Model, [2]float64{model.InputPrice, model.OutputPrice})
	return nil
}

func llmModelRefreshCache(ctx context.Context) error {
	models := []model.LLMModel{}
	if err := db.GetDB().WithContext(ctx).Find(&models).Error; err != nil {
		return err
	}
	for _, model := range models {
		llmModelCache.Set(model.Model, [2]float64{model.InputPrice, model.OutputPrice})
	}
	return nil
}
