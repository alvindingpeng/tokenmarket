package op

import (
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// MarketCatalog 返回模型广场的挂牌表: 每个「已发布 + 已过审 + 已上架」的授权一行,
// 按小写模型名折叠成模型级分组。
//
// 供给侧口径逐条对齐 ChannelGrantCandidates(同一个闸口、同一份价格折算、同一套可用性判定),
// 走到这里的三处差异必须为零 —— 否则用户会在广场看到一条"可用"的挂牌, 加进分组却被告知不可用。
//
// 不含成功率/延迟: 那些来自 relay 的进程内指标, 而本包不 import relay。处理器侧
// 用 relay.ModelScoreViews() 按 ChannelModelID 合并, 与 GET /api/v1/model/scores 同一模式。
func MarketCatalog(scope model.Scope) []model.MarketModel {
	// 自有分组: 先把"哪些授权已在哪几个分组里"算出来, 供界面标"已加入"与重复提示。
	groupIDsByGrant := marketGroupsOf(scope)

	byName := make(map[string]*model.MarketModel, channelGrantCache.Len())
	// 展示名取出现次数最多的原写法: 上游对同一模型可能大小写不一致(deepseek-v4.1-flash / DeepSeek-V4.1-Flash),
	// 折叠成一屏一行后, 展示名不该取决于缓存遍历顺序。
	nameVotes := make(map[string]map[string]int, channelGrantCache.Len())
	for _, grant := range channelGrantCache.GetAll() {
		parts, ok := grantPartsOf(grant)
		if !ok {
			continue
		}
		// 供给侧隔离: 与对外候选同一道闸门, 含过审位。归属者与管理员也一视同仁 ——
		// 广场是"我能买到什么", 未发布/未过审的自家渠道属于渠道管理页的视野。
		if !parts.Channel.Shared || parts.Channel.ReviewStatus != model.ChannelReviewApproved || !parts.ChannelModel.Listed {
			continue
		}
		key := strings.ToLower(parts.ChannelModel.Name)
		channel := marketChannelOf(grant, parts)

		if byName[key] == nil {
			byName[key] = &model.MarketModel{
				Name:     parts.ChannelModel.Name,
				Brand:    MarketBrandOf(parts.ChannelModel.Name),
				Channels: make([]model.MarketChannel, 0, 4),
				// InGroups 先留空, 在折叠结束后统一汇总。
				// 之前在这里直接取 groupIDsByGrant[grant.ID] 是个依赖遍历顺序的缺陷:
				// 同一模型在不同渠道里可能大小写不一致(deepseek-v4.1-flash / DeepSeek-V4.1-Flash),
				// 折叠成一屏一行后只保留了"第一个到达的 grant"的归属; 若那个 grant 恰好不在任何分组里,
				// 而另一个在, 角标就丢了 —— 而哪个先到取决于 map 遍历顺序, 于是表现为偶发失败。
				InGroups: []int{},
			}
			nameVotes[key] = make(map[string]int, 2)
		}
		byName[key].Channels = append(byName[key].Channels, channel)
		nameVotes[key][parts.ChannelModel.Name]++
		// 同一折叠行要把所有成员的归属并起来: 只要任一渠道的授权进了分组, 这个模型就"在分组里"。
		for _, groupID := range groupIDsByGrant[grant.ID] {
			if !containsInt(byName[key].InGroups, groupID) {
				byName[key].InGroups = append(byName[key].InGroups, groupID)
			}
		}
	}

	out := make([]model.MarketModel, 0, len(byName))
	for key, entry := range byName {
		entry.Name = marketDisplayName(nameVotes[key], entry.Name)
		sortMarketChannels(entry.Channels)
		// InGroups 缺失时也要给出空数组: 读取侧承诺集合字段不为 null。
		if entry.InGroups == nil {
			entry.InGroups = []int{}
		}
		out = append(out, *entry)
	}
	// 定序按名称: 服务端已按 sort 参数定序时处理器会重排, 这里的缺省序只是稳定基准。
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// marketChannelOf 把一条授权转成广场挂牌行。
//
// 与 grantCandidateOf 取同一份价格与可用性(内部共用 grantLedgerPrices / grantAvailability),
// 但字段集更小: 渠道真名、凭据名、归属用户一律不出 —— 广场是面向全部登录用户的公开列表,
// 暴露面只能比分组页候选更小, 不能更大。
func marketChannelOf(grant model.ChannelGrant, parts grantParts) model.MarketChannel {
	_, _, currency, userLLM, userMedia, fxOK := grantLedgerPrices(parts)
	return model.MarketChannel{
		ChannelGrantID: grant.ID,
		ChannelModelID: grant.ChannelModelID,
		ChannelCode:    grantDisplayName(parts.Channel),
		Available:      grantAvailability(parts, fxOK),
		StatusReason:   marketStatusReason(parts, fxOK),
		UserPrice:      userLLM,
		UserMedia:      userMedia,
		Kind:           parts.ChannelModel.Kind,
		PriceCurrency:  currency,
		FxMissing:      !fxOK,
		Unpriced:       isZeroLLMPrice(userLLM) && isZeroMediaPrice(userMedia),
	}
}

// marketStatusReason 给出不可用/需提示的原因, 多因并存时按优先级取一条(与设计稿 §3.5 徽章表一致):
// 牌价缺失 > 渠道停用 > 凭据停用 > 熔断中 > 未定价。
//
// 优先级的用意: 前四项都让这条挂牌"当前不可用", 用户最该先知道的是最上游的那条原因;
// 未定价不是不可用(免费模型照常能跑), 只是价格位显示"未定价", 故排最后。
func marketStatusReason(parts grantParts, fxOK bool) string {
	switch {
	case !fxOK:
		return model.MarketStatusFxMissing
	case !parts.Channel.Enabled:
		return model.MarketStatusDisabled
	case isCircuitBroken(parts.Channel.DisabledUntil):
		// 渠道级 DisabledUntil 有两重语义: 手动停用之外, 额度暂停(ChannelQuotaPaused)也折进这一列。
		return model.MarketStatusDisabled
	case !parts.ChannelKey.Enabled:
		return model.MarketStatusKeyDisabled
	case isCircuitBroken(parts.ChannelKey.DisabledUntil), isCircuitBroken(parts.ChannelModel.DisabledUntil):
		return model.MarketStatusBreaker
	case isZeroLLMPrice(parts.ChannelModel.SupplyPrice) && isZeroMediaPrice(parts.ChannelModel.MediaSupply):
		return model.MarketStatusUnpriced
	}
	return ""
}

// sortMarketChannels 定序挂牌行: 可用优先 → 价格升 → 渠道编码升。
//
// 定序在服务端一次定稿, 前端不再重排: 广场的"最低价/渠道数"角标与排序逻辑若分散在前后端两处,
// 必然出现两套顺序不一致(与分组页候选列表"顺序须由此处定稿"的理由相同)。
func sortMarketChannels(channels []model.MarketChannel) {
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].Available != channels[j].Available {
			return channels[i].Available
		}
		if pi, pj := channels[i].SortPrice(), channels[j].SortPrice(); pi != pj {
			return pi < pj
		}
		return channels[i].ChannelCode < channels[j].ChannelCode
	})
}

// marketDisplayName 从同名的多种原写法里挑出现次数最多的; 次数相同则取字典序最小, 保证结果稳定。
func marketDisplayName(votes map[string]int, fallback string) string {
	best, bestCount := fallback, -1
	for name, count := range votes {
		if count > bestCount || (count == bestCount && name < best) {
			best, bestCount = name, count
		}
	}
	return best
}

// marketGroupsOf 返回「授权主键 → 我已把它加进哪些自有分组 id」。
//
// 只含自有分组: 分组严格按用户私有(GroupList 按 scope.ID 过滤), 管理员也看不到别人的分组,
// 因此这里的"已加入"角标对任何角色都只反映他自己的配置。未命中的授权不出现在 map 里(调用方拿 nil 切片)。
// containsInt 报告集合里是否已有该值。折叠汇总分组角标时用它去重。
func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func marketGroupsOf(scope model.Scope) map[int][]int {
	groups := GroupList(scope)
	out := make(map[int][]int, 0)
	for _, group := range groups {
		for _, item := range group.Items {
			out[item.ChannelGrantID] = append(out[item.ChannelGrantID], group.ID)
		}
	}
	for grantID, ids := range out {
		sort.Ints(ids)
		out[grantID] = ids
	}
	return out
}
