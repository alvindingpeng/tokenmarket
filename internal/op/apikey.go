package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

var apiKeyCache = cache.New[int, model.APIKey](16)
var apiVerifyCache = cache.New[string, bool](16)

func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return err
	}
	apiKeyCache.Set(key.ID, *key)
	apiVerifyCache.Set(key.APIKey, true)
	return nil
}

func APIKeyVerify(key string, ctx context.Context) bool {
	_, ok := apiVerifyCache.Get(key)
	return ok
}

func APIKeyList(ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for id, apiKey := range apiKeyCache.GetAll() {
		keys = append(keys, model.APIKey{
			ID:     id,
			APIKey: apiKey.APIKey,
			Name:   apiKey.Name,
		})
	}
	return keys, nil
}

func APIKeyDelete(id int, ctx context.Context) error {
	k := model.APIKey{
		ID: id,
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if result.Error != nil {
		return result.Error
	}
	apiKeyCache.Del(k.ID)
	apiVerifyCache.Del(k.APIKey)
	return nil
}

func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
		apiVerifyCache.Set(apiKey.APIKey, true)
	}
	return nil
}
