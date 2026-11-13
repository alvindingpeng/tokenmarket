
package relay

// 上游凭据的令牌提供者。
//
// 这里的取舍是整个账号接入方案的核心: auth.APIKeyProvider 的 Get(ctx) 是**同步且不返回 error**
// 的接口, 而 OAuth 刷新是一次网络调用 —— 所以刷新绝不能在转发路径上做, 否则每转发一次
// 都可能挂上一个最长 20 秒的网络等待, 而且失败了也没有地方上报。
// 结论: 转发只读缓存里已经刷好的令牌, 刷新交给定时任务; 这里负责把"账号凭据"的令牌从
// 账号 JSON 里取出来, 取不到时退回空串并由上游以 401 暴露(那会走正常的熔断与换成员)。

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// channelKeyTokenProvider 从凭据里取本轮要用的访问令牌。
//
// 静态凭据直接用 Key 列; 账号凭据从 JSON 里解 access_token。
// 解析失败时不 panic 也不返回错误(接口不允许), 而是返回空串: 空 Bearer 会让上游回 401,
// 进而触发既有的凭据级熔断与换成员逻辑 —— 那条路已经能正确处理"这把凭据此刻不能用"。
type channelKeyTokenProvider struct {
	// token 是解析好的固定值。在构造时解析而不是每次 Get 再解析:
	// buildOutbound 每轮请求都会调用, 每轮 json.Unmarshal 一遍账号 JSON 是纯浪费。
	token string
}

// Get 实现 auth.APIKeyProvider。
func (p channelKeyTokenProvider) Get(_ context.Context) string {
	return p.token
}

// channelKeyTokenOf 从一条凭据取本轮使用的令牌。
//
// 账号凭据取 access_token; 非账号凭据原样返回 Key。
// 这里不做"是否过期"的判断 —— 过期与否由定时任务负责刷新; 若它因故停摆, 这里返回过期令牌,
// 上游回 401, 熔断机制会把这把凭据摘下来并告警。让失败走既有回路, 好过在这里静默兜底。
func channelKeyTokenOf(channelKey model.ChannelKey) string {
	if channelKey.AccountKind == "" {
		return channelKey.Key
	}
	credential, err := model.AccountJSONParse(channelKey.AccountPlatform, []byte(channelKey.Key))
	if err != nil {
		return ""
	}
	if credential.AccessToken != "" {
		return credential.AccessToken
	}
	return ""
}
