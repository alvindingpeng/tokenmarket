package op

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// ShareModel 分享页公开的模型条目: 名称、媒体形态与对客单价(供货价折算后上浮, 与计费同口径)。
// 价格一律先按当日牌价折算到账面币种再上浮, 因此 PriceCurrency 恒为账面币种 USD:
// 它描述的是「这些数字用什么单位读」, 而不是渠道当初的报价币种 —— 若两者混用,
// 页面会把折算后的 USD 数字贴上 CNY 标签, 又变成一次错价展示。
type ShareModel struct {
	Name          string           `json:"name"`
	Kind          model.MediaKind  `json:"kind,omitempty"`
	UserPrice     model.LLMPrice   `json:"user_price"`
	UserMedia     model.MediaPrice `json:"user_media,omitempty"`
	PriceCurrency string           `json:"price_currency,omitempty"` // 展示币种 = 账面币种(USD)。
	FxMissing     bool             `json:"fx_missing,omitempty"`     // 报价币种缺牌价: 价格未折算且已清零, 页面显示「暂缺」。
}

// ShareChannelModels 返回发布编码对应渠道公开上架的模型清单(对客价口径), 按名称定序。
// 供给侧隔离同内部口径: 未发布渠道与未上架模型一律不出现; 编码不存在返回错误。
func ShareChannelModels(code string) ([]ShareModel, error) {
	models, _, err := ShareChannelDetail(code)
	return models, err
}

// ShareChannelDetail 同时返回模型清单与脱敏质量摘要(P3)。
// 质量摘要只在有近期快照时给出: 没有数据时返回 nil, 分享页据此不渲染质量条, 而不是显示一排破折号。
func ShareChannelDetail(code string) ([]ShareModel, *ShareQuality, error) {
	// 兼容 "#1001" 形式: 界面展示带 # 前缀, 用户照抄来查也能中。
	code = strings.TrimPrefix(strings.TrimSpace(code), "#")
	if code == "" {
		return nil, nil, fmt.Errorf("share code required")
	}
	channelID := 0
	for _, item := range channelCache.GetAll() {
		if item.Shared && item.ShareCode == code {
			channelID = item.ID
			break
		}
	}
	if channelID == 0 {
		return nil, nil, fmt.Errorf("share code not found")
	}
	markup := ChannelMarkupFor(channelID)
	models := make([]ShareModel, 0)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != channelID || !channelModel.Listed {
			continue
		}
		// 多币种: 原币报价先折算成账面币种再上浮, 与选路/预扣/账单同一口径;
		// 缺牌价时价格清零并标记 FxMissing, 页面显示「暂缺」而不是把原币当 USD 或当 0 元。
		supply, media, _, ok := ledgerModelPrices(context.Background(), channelModel.SupplyPrice, channelModel.MediaSupply, channelModel.PriceCurrency)
		models = append(models, ShareModel{
			Name: channelModel.Name, Kind: channelModel.Kind,
			UserPrice: supply.Scale(markup), UserMedia: media.Scale(markup),
			PriceCurrency: model.LedgerCurrency, FxMissing: !ok,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, ShareQualityOf(channelID), nil
}
