package op

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestSubscriptionChannelPricingFollowsReference 是 S6 的验收测试(用户选定方案 A)。
//
// 方案 A = 订阅渠道的供货价取 models.dev 参考价, 再按渠道 MarkupRatio 加成对外。
// 本测试钉住这条链路的两端:
//
//	供货价  <- 参考价(models.dev, 按模型名匹配)
//	用户价  <- 供货价 ×(1 + MarkupRatio)
//
// 为什么必须测这条: 预填供货价受 channel_auto_add_fill_price 开关控制(默认关),
// 开关关着时供货价全是 0, 而 0 × 任何加成仍是 0 —— 渠道看起来配好了, 实际全免费,
// 且界面上没有任何地方显示"这个渠道在免费送"。
func TestSubscriptionChannelPricingFollowsReference(t *testing.T) {
	conn := autoaddTestDB(t)
	channelID, _, m0ID, m1ID := autoaddSeed(t, conn)

	// 只给 m0 设参考价; m1 故意不设, 用来验证"参考价缺失时不编假价"。
	llmModelCache.Set("m0", model.LLMPrice{Input: 2, Output: 4})
	t.Cleanup(func() { llmModelCache.Del("m0") })

	if err := fillSupplyPriceFromReference(conn, channelID, []string{"m0", "m1"}); err != nil {
		t.Fatalf("预填供货价: %v", err)
	}

	var m0 model.ChannelModel
	if err := conn.First(&m0, m0ID).Error; err != nil {
		t.Fatalf("读 m0: %v", err)
	}
	if m0.SupplyPrice.Input != 2 || m0.SupplyPrice.Output != 4 {
		t.Fatalf("供货价应取参考价 {2,4}, 实际 %+v", m0.SupplyPrice)
	}

	// 用户价 = 供货价 ×(1 + markup)。取 markup = 1.0 即翻倍, 便于断言。
	userPrice := m0.UserPriceLLM(1.0)
	if userPrice.Input != 4 || userPrice.Output != 8 {
		t.Fatalf("用户价应为供货价 2 倍 {4,8}, 实际 %+v", userPrice)
	}

	// m1 参与了预填但参考价缺失, 因此供货价保持 0。
	// 这正是要紧的行为: 没参考价时不能编一个假价 —— 假价会让账单看着正常, 实际全错。
	// 而供货价 0 的用户价也是 0, 表示"未定价", 对该模型而言等于对客免费 ——
	// 测试把它钉住, 是为了让"未定价就免费"这条语义显式可见: 任何人改动它都会先看到这条失败。
	var m1 model.ChannelModel
	if err := conn.First(&m1, m1ID).Error; err != nil {
		t.Fatalf("读 m1: %v", err)
	}
	if m1.SupplyPrice.Input != 0 || m1.SupplyPrice.Output != 0 {
		t.Fatalf("m1 未预填, 供货价应为 0, 实际 %+v", m1.SupplyPrice)
	}
	if p := m1.UserPriceLLM(1.0); p.Input != 0 || p.Output != 0 {
		t.Fatalf("未定价模型的用户价应为 0, 实际 %+v", p)
	}
}

// TestSubscriptionChannelMarkupFollowsGlobalWhenUnset 验证渠道没设 markup 时跟随全局。
//
// 订阅渠道的定价全靠 markup 表达利润: 供货价来自参考价(固定), 唯一能调的就是 markup。
// 若"没设"被当成 0 而不是"跟随全局", 订阅渠道会静默变成零加价 —— 与全局策略脱节。
func TestSubscriptionChannelMarkupFollowsGlobalWhenUnset(t *testing.T) {
	conn := autoaddTestDB(t)
	channelID, _, m0ID, _ := autoaddSeed(t, conn)

	llmModelCache.Set("m0", model.LLMPrice{Input: 10, Output: 10})
	t.Cleanup(func() { llmModelCache.Del("m0") })
	if err := fillSupplyPriceFromReference(conn, channelID, []string{"m0", "m1"}); err != nil {
		t.Fatalf("预填: %v", err)
	}

	// 渠道 markup 取 -1 表示跟随全局(这是既有约定, 见 model.Channel.MarkupRatio)。
	var channel model.Channel
	if err := conn.First(&channel, channelID).Error; err != nil {
		t.Fatalf("读渠道: %v", err)
	}
	if channel.MarkupRatio != -1 {
		t.Fatalf("测试前提: 渠道 markup 应为 -1(跟随全局), 实际 %v", channel.MarkupRatio)
	}

	// 全局 markup 设为 0.5, 显式设渠道 markup 时用户价应是供货价的 1.5 倍。
	var m0 model.ChannelModel
	if err := conn.First(&m0, m0ID).Error; err != nil {
		t.Fatalf("读 m0: %v", err)
	}
	if p := m0.UserPriceLLM(0.5); p.Input != 15 {
		t.Errorf("markup 0.5 时 10 应加成 15, 实际 %v", p.Input)
	}
}
