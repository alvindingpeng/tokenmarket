package op

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 授权展示口径的单点: 可用性判定、对外展示名、候选构造。
//
// 为什么抽出来: 这三件事此前各写一套 —— ChannelGrantCandidates 判可用性与展示名,
// groupSnapshot 判可用性与展示名, 广场(P1)会是第三处。一旦某处漏改, 用户就会看到
// "广场显示可用、加进分组后不可用"或"同一个渠道两处显示不同编码"。
// 抽成单点后, 新增读取面只需复用, 无需再抄第二遍表达式。
//
// 注意: 供给侧闸口(是否已发布/已过审/已上架)不在此处定, 由各调用方按自己的语义决定 ——
// 对外候选与广场要求"已发布 + 已过审 + 已上架"; 分组成员的存量快照只要求"已发布 + 已上架"
// (复审导致的 draft/stale 不应让已在分组里的存量成员消失, 这是既有语义, 不在本次收口范围)。

// grantParts 是一条授权的三侧:{渠道, 凭据, 渠道模型}。
// ok=false 表示任一侧已从缓存消失(被删除), 调用方应跳过该授权。
type grantParts struct {
	Channel      model.Channel
	ChannelKey   model.ChannelKey
	ChannelModel model.ChannelModel
}

// grantPartsOf 解析一条授权的三侧实体。
func grantPartsOf(grant model.ChannelGrant) (grantParts, bool) {
	channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
	channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
	if !modelOK || !keyOK {
		return grantParts{}, false
	}
	channel, channelOK := channelCache.Get(channelModel.ChannelID)
	if !channelOK {
		return grantParts{}, false
	}
	return grantParts{Channel: channel, ChannelKey: channelKey, ChannelModel: channelModel}, true
}

// grantAvailability 判定一条授权当前能否转发, 三处读取代必须走这一份:
// 价格折算成功(fxOK) 且渠道、凭据、渠道模型三者都启用且都不在熔断窗口内。
//
// 为什么把 fxOK 放进来而不是各点自行 &&: 牌价取不到时价格已被清零, 若仍标"可用",
// 选路会按 0 元计价把它排到第一位并免费结算 —— 保守排除是唯一安全的处理。
func grantAvailability(parts grantParts, fxOK bool) bool {
	return fxOK &&
		parts.Channel.Enabled && !isCircuitBroken(parts.Channel.DisabledUntil) &&
		parts.ChannelKey.Enabled && !isCircuitBroken(parts.ChannelKey.DisabledUntil) &&
		!isCircuitBroken(parts.ChannelModel.DisabledUntil)
}

// grantDisplayName 返回授权的渠道展示名: 已发布渠道一律显示发布唯一编码(#1001 形式),
// 上游真名只在归属者与管理员的自有渠道管理页出现。
func grantDisplayName(channel model.Channel) string {
	if channel.Shared && channel.ShareCode != "" {
		return "#" + channel.ShareCode
	}
	return channel.Name
}

// grantLedgerPrices 折算一条授权的用户价, 三处读取代共用同一口径。
// fxOK=false 表示牌价缺失: 价格已被清零, 调用方须把该授权判为不可用。
func grantLedgerPrices(parts grantParts) (supply model.LLMPrice, media model.MediaPrice, currency string, userLLM model.LLMPrice, userMedia model.MediaPrice, fxOK bool) {
	supply, media, currency, fxOK = ledgerModelPrices(
		context.Background(), parts.ChannelModel.SupplyPrice, parts.ChannelModel.MediaSupply, parts.ChannelModel.PriceCurrency,
	)
	markup := ChannelMarkupFor(parts.Channel.ID)
	return supply, media, currency, supply.Scale(markup), media.Scale(markup), fxOK
}

// grantCandidateOf 构造一条对外授权候选, 是 ChannelGrantCandidate 的唯一构造点。
// 供给侧闸口由调用方在调用前判定(见文件头注释), 本函数只负责展示字段与脱敏。
func grantCandidateOf(grant model.ChannelGrant, parts grantParts, scope model.Scope) model.ChannelGrantCandidate {
	supply, media, currency, userLLM, userMedia, fxOK := grantLedgerPrices(parts)
	candidate := model.ChannelGrantCandidate{
		ID:            grant.ID,
		ChannelID:     parts.Channel.ID,
		ChannelName:   grantDisplayName(parts.Channel),
		ModelName:     parts.ChannelModel.Name,
		KeyName:       parts.ChannelKey.Name,
		Protocols:     grant.Protocols,
		Available:     grantAvailability(parts, fxOK),
		Listed:        parts.ChannelModel.Listed,
		SupplyPrice:   supply,
		UserPrice:     userLLM,
		MediaSupply:   media,
		UserMedia:     userMedia,
		Kind:          parts.ChannelModel.Kind,
		PriceCurrency: currency,
		FxMissing:     !fxOK,
	}
	// 凭据名按归属脱敏: 非归属者(含普通用户)看不到凭据名。
	if !scope.Owns(parts.Channel.UserID) {
		candidate.KeyName = ""
	}
	return candidate
}
