package op

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// ShareModel 分享页公开的模型条目: 名称、媒体形态与对客单价(供货价上浮后, 与计费同口径)。
type ShareModel struct {
	Name      string           `json:"name"`
	Kind      model.MediaKind  `json:"kind,omitempty"`
	UserPrice model.LLMPrice   `json:"user_price"`
	UserMedia model.MediaPrice `json:"user_media,omitempty"`
}

// ShareChannelModels 返回发布编码对应渠道公开上架的模型清单(对客价口径), 按名称定序。
// 供给侧隔离同内部口径: 未发布渠道与未上架模型一律不出现; 编码不存在返回错误。
func ShareChannelModels(code string) ([]ShareModel, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("share code required")
	}
	channelID := 0
	for _, item := range channelCache.GetAll() {
		if item.Shared && item.ShareCode == code {
			channelID = item.ID
			break
		}
	}
	if channelID == 0 {
		return nil, fmt.Errorf("share code not found")
	}
	markup := SettingGetFloat(model.SettingKeyMarkupRatio)
	models := make([]ShareModel, 0)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != channelID || !channelModel.Listed {
			continue
		}
		models = append(models, ShareModel{
			Name:      channelModel.Name,
			Kind:      channelModel.Kind,
			UserPrice: channelModel.UserPriceLLM(markup),
			UserMedia: channelModel.UserPriceMedia(markup),
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}
