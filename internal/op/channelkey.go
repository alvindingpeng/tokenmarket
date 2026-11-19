package op

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// ChannelKeysByChannel 返回一个渠道的全部凭据(含已停用), 供按凭据名或凭据 ID 反查凭据的调用方使用,
// 例如生视频任务归属: 创建时记下实际凭据 ID, 查询与取消时按它还原同一把凭据。
// 返回的副本带凭据明文, 只能用于构造上游请求: 严禁写入持久化记录、日志或 API 响应。
func ChannelKeysByChannel(channelID int) []model.ChannelKey {
	keys := make([]model.ChannelKey, 0, 4)
	for _, key := range channelKeyCache.GetAll() {
		if key.ChannelID == channelID {
			keys = append(keys, key)
		}
	}
	return keys
}

// ChannelKeyLookup 按主键取回凭据, 不做可用性过滤。已创建的视频任务即使凭据停用仍须使用原凭据查询。
func ChannelKeyLookup(id int) (model.ChannelKey, error) {
	key, ok := channelKeyCache.Get(id)
	if !ok {
		return model.ChannelKey{}, fmt.Errorf("channel key not found")
	}
	return key, nil
}

// ChannelGrantLookup 按主键取授权并补齐模型与凭据, 不做可用性过滤。
// ChannelGrantGet 面向转发: 凭据停用即拒绝, 保证拿到即可直接转发。任务归属回查(生视频任务的查询与取消)
// 必须能定位创建时的授权, 即使它绑定的凭据后来被停用 —— 任务只在创建它的那把凭据名下可见,
// 换一把"可用"凭据去查只会把「查无此任务」伪装成上游的答复, 停用与否由调用方按场景定夺。
func ChannelGrantLookup(id int) (model.ChannelGrant, error) {
	grant, ok := channelGrantCache.Get(id)
	if !ok {
		return model.ChannelGrant{}, fmt.Errorf("channel grant not found")
	}
	channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
	if !ok {
		return model.ChannelGrant{}, fmt.Errorf("channel model %d not found", grant.ChannelModelID)
	}
	grant.ChannelModel = &channelModel
	if channelKey, ok := channelKeyCache.Get(grant.ChannelKeyID); ok {
		grant.ChannelKey = &channelKey
	}
	return grant, nil
}
