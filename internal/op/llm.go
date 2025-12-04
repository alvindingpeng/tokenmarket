package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

var llmModelCache = cache.New[string, model.LLMPrice](16)

func LLMList(ctx context.Context) ([]model.LLMInfo, error) {
	models := []model.LLMInfo{}
	for m, cost := range llmModelCache.GetAll() {
		models = append(models, model.LLMInfo{
			Name:     m,
			LLMPrice: cost,
		})
	}
	return models, nil
}

func LLMUpdate(model model.LLMInfo, ctx context.Context) error {
	_, ok := llmModelCache.Get(model.Name)
	if !ok {
		return fmt.Errorf("model not found")
	}
	if err := db.GetDB().WithContext(ctx).Save(model).Error; err != nil {
		return err
	}
	llmModelCache.Set(model.Name, model.LLMPrice)
	return nil
}

func LLMDelete(modelName string, ctx context.Context) error {
	_, ok := llmModelCache.Get(modelName)
	if !ok {
		return fmt.Errorf("model not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.LLMInfo{Name: modelName}).Error; err != nil {
		return err
	}
	llmModelCache.Del(modelName)
	return nil
}

func LLMCreate(model model.LLMInfo, ctx context.Context) error {
	_, ok := llmModelCache.Get(model.Name)
	if ok {
		return fmt.Errorf("model already exists")
	}
	if err := db.GetDB().WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	llmModelCache.Set(model.Name, model.LLMPrice)
	return nil
}

func LLMGet(name string) (model.LLMPrice, error) {
	price, ok := llmModelCache.Get(name)
	if !ok {
		return model.LLMPrice{}, fmt.Errorf("model not found")
	}
	return price, nil
}

func llmRefreshCache(ctx context.Context) error {
	models := []model.LLMInfo{}
	if err := db.GetDB().WithContext(ctx).Find(&models).Error; err != nil {
		return err
	}
	for _, model := range models {
		llmModelCache.Set(model.Name, model.LLMPrice)
	}
	return nil
}
