package op

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// ChannelMarkupFollowGlobal 渠道未覆盖上浮比例的哨兵值(存在 channels.markup_ratio 列里)。
const ChannelMarkupFollowGlobal = -1.0

// ChannelMarkupFor 返回某渠道生效的上浮比例: 渠道覆盖值(>=0)优先, 否则全局设置。
// 渠道不在缓存(已删除/未加载)时回落全局 —— 展示路径宁可退到全局口径也不能算出 0 上浮。
// 所有"用户价 = 供货价 × (1+markup)"的读取点必须经本函数取值, 不允许各点自行判哨兵。
func ChannelMarkupFor(channelID int) float64 {
	if channel, ok := channelCache.Get(channelID); ok && channel.MarkupRatio >= 0 {
		return channel.MarkupRatio
	}
	return SettingGetFloat(model.SettingKeyMarkupRatio)
}

// ChannelMarkupForWithTenant 返回考虑租户定价策略后的生效上浮比例。
// 优先级: 租户渠道/模型特定策略 > 租户默认策略 > 渠道覆盖 > 全局设置。
func ChannelMarkupForWithTenant(ctx context.Context, channelID int, tenantID uint, modelName string) float64 {
	// 1. 首先检查租户定价策略
	if tenantID > 0 {
		// 查找租户特定的定价策略 (channel-specific)
		var policies []model.TenantPricingPolicy
		err := db.GetDB().WithContext(ctx).Where(
			"tenant_id = ? AND channel_id = ? AND model_name = ? AND is_active = ?",
			tenantID, channelID, modelName, true).
			Order("priority DESC").Limit(1).Find(&policies).Error
		if err == nil && len(policies) > 0 {
			return policies[0].MarkupRatio
		}

		// 查找租户默认定价策略 (no channel, no model)
		err = db.GetDB().WithContext(ctx).Where(
			"tenant_id = ? AND (channel_id IS NULL OR channel_id = 0) AND (model_name = '' OR model_name IS NULL) AND is_active = ?",
			tenantID, true).
			Order("priority DESC").Limit(1).Find(&policies).Error
		if err == nil && len(policies) > 0 {
			return policies[0].MarkupRatio
		}
	}

	// 2. 回落到渠道级别配置
	if channel, ok := channelCache.Get(channelID); ok && channel.MarkupRatio >= 0 {
		return channel.MarkupRatio
	}

	// 3. 回落到全局设置
	return SettingGetFloat(model.SettingKeyMarkupRatio)
}

// ChannelMarkupForWithTenantAndChannelModel 根据 ChannelModel 解析租户定价。
func ChannelMarkupForWithTenantAndChannelModel(ctx context.Context, channelID int, tenantID uint, channelModelID uint) float64 {
	if tenantID == 0 {
		return ChannelMarkupFor(channelID)
	}

	// 尝试获取 ChannelModel 以便查找模型特定策略
	var cm model.ChannelModel
	err := db.GetDB().WithContext(ctx).Where("id = ?", channelModelID).First(&cm).Error
	modelName := ""
	if err == nil {
		modelName = cm.Name
	}
	return ChannelMarkupForWithTenant(ctx, channelID, tenantID, modelName)
}
