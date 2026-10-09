package relay

import (
	"encoding/json"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/ratelimit"
)

// limiter 进程内 RPM/TPM 限流器; 策略解析走系统设置与限流策略表, 越具体越优先。
var limiter = ratelimit.NewResolved(func(scope ratelimit.Scope) ratelimit.Limits {
	rpm, tpm, concurrent := op.RateLimitsFor(scope.Kind, scope.ID, scope.Model)
	return ratelimit.Limits{RPM: rpm, TPM: tpm, Concurrent: concurrent}
}, func(scope ratelimit.Scope) []ratelimit.Scope {
	var result []ratelimit.Scope
	for _, name := range op.RateScopeModels(scope.Kind, scope.ID, scope.Model) {
		result = append(result, ratelimit.Scope{Kind: scope.Kind, ID: scope.ID, Model: name})
	}
	return result
})

// Limiter 暴露限流器, 供管理接口查询用量与触顶状态。
func Limiter() *ratelimit.Limiter { return limiter }

// userRateScopes 用户侧限流范围: 用户与 API Key 两个维度, 每个用户请求只预留一次。
func userRateScopes(userID uint, apiKeyID int, clientModel string) []ratelimit.Scope {
	return []ratelimit.Scope{
		{Kind: model.RateScopeUser, ID: int(userID), Model: clientModel},
		{Kind: model.RateScopeAPIKey, ID: apiKeyID, Model: clientModel},
	}
}

// upstreamRateScopes 上游侧限流范围: 渠道、凭据与渠道模型三个维度, 每次实际尝试独立计量。
func upstreamRateScopes(channelID, channelKeyID, channelModelID int, modelName string) []ratelimit.Scope {
	return []ratelimit.Scope{
		{Kind: model.RateScopeChannel, ID: channelID, Model: modelName},
		{Kind: model.RateScopeChannelKey, ID: channelKeyID, Model: modelName},
		{Kind: model.RateScopeChannelModel, ID: channelModelID, Model: ""},
	}
}

// upstreamItemScopes 选路侧按成员快照构造上游范围, 用于跳过受限成员。
func upstreamItemScopes(item model.GroupItem) []ratelimit.Scope {
	return upstreamRateScopes(item.ChannelID, item.ChannelKeyID, item.ChannelModelID, item.ModelName)
}

// estimateTokens 估算本次请求的输入与输出 token 数, 用于限流预留; 结束时按真实用量结算。
// 输入按请求体字符数粗略折算, 输出按余额预扣的输出上限保守预留。
func estimateTokens(body []byte) (estInput, estOutput int64) {
	estInput = int64(len(body))/4 + 32
	capTokens, err := op.SettingGetInt(model.SettingKeyBalanceReserveCap)
	if err != nil || capTokens <= 0 {
		capTokens = 4096
	}
	// An explicit output budget should be reserved as requested, not replaced
	// with the fallback; this avoids rejecting small requests as 4096 tokens.
	var limits struct {
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
		MaxOutputTokens     int64 `json:"max_output_tokens"`
		MaxTokens           int64 `json:"max_tokens"`
	}
	if json.Unmarshal(body, &limits) == nil {
		for _, value := range []int64{limits.MaxCompletionTokens, limits.MaxOutputTokens, limits.MaxTokens} {
			if value > 0 {
				return estInput, value
			}
		}
	}
	return estInput, int64(capTokens)
}
