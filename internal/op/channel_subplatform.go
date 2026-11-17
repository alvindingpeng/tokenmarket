package op

import (
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/subplatform"
)

// applySubscriptionPlatform 把订阅渠道的配置按平台描述补齐。
//
// 订阅渠道的全部意义是"不填调用信息", 所以这里做的事就是: 用户选了平台名,
// 系统据此填上方言与地址。用户填了的地址不覆盖 —— 平台描述是默认值, 不是强制值
// (自建中转、企业专线都要求能改地址)。
//
// 加平台时漏登记会在 subplatform.Validate 与建渠道两处暴露, 不会静默降级。
func applySubscriptionPlatform(config *model.ChannelConfig) error {
	// 非订阅方言不参与: 它们的地址与方言本来就是手填的, 覆盖只会破坏既有配置。
	if !config.Dialect.Subscribable() {
		// 订阅方言却给了平台名, 或非订阅方言给了平台名, 两者都是自相矛盾。
		if strings.TrimSpace(config.SubPlatform) != "" {
			return fmt.Errorf("只有订阅渠道可以指定平台, 当前方言为 %q", config.Dialect)
		}
		return nil
	}
	platformID := strings.TrimSpace(config.SubPlatform)
	if platformID == "" {
		return fmt.Errorf("订阅渠道必须指定平台")
	}
	platform, ok := subplatform.Of(platformID)
	if !ok {
		return fmt.Errorf("未登记的订阅平台: %s", platformID)
	}
	config.SubPlatform = platform.ID
	// 方言由平台描述决定: 订阅渠道的方言恒为 DialectSubscription, 不让前端另填一个值
	// 造成"方言 subscription 但平台是别的"这种自相矛盾状态。
	config.Dialect = platform.Dialect
	// 地址只在用户没填时补默认值。
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = platform.BaseURL
	}
	return nil
}
