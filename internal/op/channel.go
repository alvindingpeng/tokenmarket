package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/alvindingpeng/switchyard/internal/utils/xstrings"
)

var channelCache = cache.New[int, model.Channel](16) // channelCache 保存渠道配置的进程内副本。

// ChannelList 返回缓存中的全部渠道。
func ChannelList(ctx context.Context) ([]model.Channel, error) {
	channels := make([]model.Channel, 0, channelCache.Len())
	for _, channel := range channelCache.GetAll() {
		channels = append(channels, channel)
	}
	return channels, nil
}

// ChannelCreate 创建渠道并写入缓存。
func ChannelCreate(channel *model.Channel, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(channel).Error; err != nil {
		return err
	}
	channelCache.Set(channel.ID, *channel)
	return nil
}

// ChannelUpdate 更新请求中明确提供的渠道字段并刷新缓存。
func ChannelUpdate(req *model.ChannelUpdateRequest, ctx context.Context) (*model.Channel, error) {
	_, ok := channelCache.Get(req.ID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}

	var selectFields []string
	updates := model.Channel{ID: req.ID}

	if req.Name != nil {
		selectFields = append(selectFields, "name")
		updates.Name = *req.Name
	}
	if req.Type != nil {
		selectFields = append(selectFields, "type")
		updates.Type = *req.Type
	}
	if req.Enabled != nil {
		selectFields = append(selectFields, "enabled")
		updates.Enabled = *req.Enabled
	}
	if req.BaseURL != nil {
		selectFields = append(selectFields, "base_url")
		updates.BaseURL = *req.BaseURL
	}
	if req.Key != nil {
		selectFields = append(selectFields, "key")
		updates.Key = *req.Key
	}
	if req.Model != nil {
		selectFields = append(selectFields, "model")
		updates.Model = *req.Model
	}
	if req.CustomModel != nil {
		selectFields = append(selectFields, "custom_model")
		updates.CustomModel = *req.CustomModel
	}
	if req.Proxy != nil {
		selectFields = append(selectFields, "proxy")
		updates.Proxy = *req.Proxy
	}
	if req.AutoSync != nil {
		selectFields = append(selectFields, "auto_sync")
		updates.AutoSync = *req.AutoSync
	}
	if req.CustomHeader != nil {
		selectFields = append(selectFields, "custom_header")
		updates.CustomHeader = *req.CustomHeader
	}
	if req.ChannelProxy != nil {
		selectFields = append(selectFields, "channel_proxy")
		updates.ChannelProxy = req.ChannelProxy
	}
	if req.ParamOverride != nil {
		selectFields = append(selectFields, "param_override")
		updates.ParamOverride = req.ParamOverride
	}
	if req.MatchRegex != nil {
		selectFields = append(selectFields, "match_regex")
		updates.MatchRegex = req.MatchRegex
	}

	// 只有当有字段需要更新时才执行 UPDATE
	if len(selectFields) > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update channel: %w", err)
		}
	}

	// 刷新缓存并返回最新数据
	if err := channelRefreshCacheByID(req.ID, ctx); err != nil {
		return nil, err
	}

	channel, _ := channelCache.Get(req.ID)
	return &channel, nil
}

// ChannelEnabled 更新渠道启用状态。
func ChannelEnabled(id int, enabled bool, ctx context.Context) error {
	oldChannel, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
		return err
	}
	oldChannel.Enabled = enabled
	channelCache.Set(id, oldChannel)
	return nil
}

// ChannelDel 删除渠道及其关联数据。
func ChannelDel(id int, ctx context.Context) error {
	_, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}

	// 开启事务
	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取所有受影响的 GroupID，用于刷新缓存
	var affectedGroupIDs []int
	if err := tx.Model(&model.GroupItem{}).
		Where("channel_id = ?", id).
		Pluck("group_id", &affectedGroupIDs).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to get affected groups: %w", err)
	}
	var affectedModelIDs []int
	if err := tx.Model(&model.GroupItem{}).
		Where("channel_id = ?", id).
		Pluck("id", &affectedModelIDs).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to get affected models: %w", err)
	}

	// 模型统计同时通过渠道 ID 和分组项 ID 关联渠道，需在删除渠道前清理。
	if err := tx.Where("channel_id = ?", id).
		Or("id IN ?", affectedModelIDs).
		Delete(&model.StatsModel{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete model stats: %w", err)
	}

	// 删除所有引用该渠道的 GroupItem
	if err := tx.Where("channel_id = ?", id).Delete(&model.GroupItem{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete group items: %w", err)
	}

	// 删除统计数据
	if err := tx.Where("channel_id = ?", id).Delete(&model.StatsChannel{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete channel stats: %w", err)
	}

	// 删除渠道
	if err := tx.Delete(&model.Channel{}, id).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 删除缓存
	channelCache.Del(id)
	StatsChannelDel(id)
	statsModelCacheNeedUpdateLock.Lock()
	for _, modelID := range affectedModelIDs {
		statsModelCache.Del(modelID)
		delete(statsModelCacheNeedUpdate, modelID)
	}
	statsModelCacheNeedUpdateLock.Unlock()

	// 刷新受影响的分组缓存
	for _, groupID := range affectedGroupIDs {
		if err := groupRefreshCacheByID(groupID, ctx); err != nil {
			log.Warnf("failed to refresh group cache for group %d: %v", groupID, err)
		}
	}

	return nil
}

// ChannelLLMList 返回所有渠道暴露的模型。
func ChannelLLMList(ctx context.Context) ([]model.LLMChannel, error) {
	models := []model.LLMChannel{}
	for _, channel := range channelCache.GetAll() {
		modelNames := xstrings.SplitTrimCompact(",", channel.Model, channel.CustomModel)
		for _, modelName := range modelNames {
			if modelName == "" {
				continue
			}
			models = append(models, model.LLMChannel{
				Name:        modelName,
				Enabled:     channel.Enabled,
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
			})
		}
	}
	return models, nil
}

// ChannelGet 返回指定渠道的缓存副本。
func ChannelGet(id int, ctx context.Context) (*model.Channel, error) {
	channel, ok := channelCache.Get(id)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	return &channel, nil
}

// channelRefreshCache 从数据库刷新全部渠道缓存。
func channelRefreshCache(ctx context.Context) error {
	channels := []model.Channel{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Stats").
		Find(&channels).Error; err != nil {
		log.Warnf("failed to get channels: %v", err)
		return err
	}
	for _, channel := range channels {
		channelCache.Set(channel.ID, channel)
	}
	return nil
}

// channelRefreshCacheByID 从数据库刷新指定渠道缓存。
func channelRefreshCacheByID(id int, ctx context.Context) error {
	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).
		Preload("Stats").
		First(&channel, id).Error; err != nil {
		return err
	}
	channelCache.Set(channel.ID, channel)
	return nil
}
