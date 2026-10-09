package op

import (
	"context"
	"fmt"
	"sort"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

var apiKeyCache = cache.New[int, model.APIKey](16)
var apiKeyIDMap = cache.New[string, int](16)

func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	apiKeyCache.Set(key.ID, *key)
	apiKeyIDMap.Set(key.APIKey, key.ID)
	return nil
}

func APIKeyUpdate(key *model.APIKey, scope model.Scope, ctx context.Context) error {
	existing, ok := apiKeyCache.Get(key.ID)
	// 密钥是用户级凭据, 严格按归属隔离: 管理员也只可改自己的。
	if !ok || existing.UserID != scope.ID {
		return fmt.Errorf("API key not found")
	}
	// 归属不可经更新接口变更。
	key.UserID = existing.UserID
	if key.APIKey == "" {
		key.APIKey = existing.APIKey
	}
	if err := db.GetDB().WithContext(ctx).Save(key).Error; err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if key.APIKey != existing.APIKey {
		apiKeyIDMap.Del(existing.APIKey)
		apiKeyIDMap.Set(key.APIKey, key.ID)
	}
	apiKeyCache.Set(key.ID, *key)
	return nil
}

// APIKeyList 返回访问者自有的 API Key, 按主键升序定序。
// 密钥严格按归属隔离, 管理员也只见自己的; 设置页不提供排序开关,
// 而缓存遍历顺序随机, 故顺序须由此处定稿。
func APIKeyList(scope model.Scope, ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for _, apiKey := range apiKeyCache.GetAll() {
		if apiKey.UserID != scope.ID {
			continue
		}
		keys = append(keys, apiKey)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys, nil
}

// APIKeyGet 按主键取 API Key, 不做归属校验(转发链路内部使用)。
func APIKeyGet(id int, ctx context.Context) (model.APIKey, error) {
	apiKey, ok := apiKeyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

// APIKeyGetScoped 按主键取 API Key 并严格校验归属, 非归属按不存在处理。
func APIKeyGetScoped(id int, scope model.Scope, ctx context.Context) (model.APIKey, error) {
	apiKey, err := APIKeyGet(id, ctx)
	if err != nil || apiKey.UserID != scope.ID {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

func APIKeyGetByAPIKey(apiKey string, ctx context.Context) (model.APIKey, error) {
	id, ok := apiKeyIDMap.Get(apiKey)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return APIKeyGet(id, ctx)
}

func APIKeyDelete(id int, scope model.Scope, ctx context.Context) error {
	if existing, ok := apiKeyCache.Get(id); !ok || existing.UserID != scope.ID {
		return fmt.Errorf("API key not found")
	}
	k := model.APIKey{
		ID: id,
	}
	if err := StatsAPIKeyDel(id); err != nil {
		return fmt.Errorf("failed to delete stats API key: %v", err)
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	apiKeyCache.Del(k.ID)
	apiKeyIDMap.Del(k.APIKey)
	return nil
}

func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
		apiKeyIDMap.Set(apiKey.APIKey, apiKey.ID)
	}
	return nil
}
