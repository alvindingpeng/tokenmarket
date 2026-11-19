package op

import (
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
