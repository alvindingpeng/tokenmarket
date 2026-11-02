package relay

import (
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
)

// liveCounters 读取某用户在当前窗口的实时计数。
//
// 这是读取用量的唯一实现: 注入给 op 层的函数直接调用它, 测试也调用它。
// 若两边各写一份, 测试验证的就不是线上真正跑的那段代码。
func liveCounters(userID uint, modelName string) (usedRPM, usedTPM, inFlight int64, resetAt time.Time) {
	scope := ratelimit.Scope{Kind: model.RateScopeUser, ID: int(userID), Model: modelName}
	requests, tokens, resetAt := limiter.Usage(scope)
	return requests, tokens, limiter.ConcurrentUsage(scope), resetAt
}

// init 把限流器的用量查询注入 op 层。
//
// 方向说明: 限流器实例活在本包, 而 op 是抽表层; 让 op 反向 import relay 会形成循环依赖。
// 所以由本包在启动时把查询能力注册出去, op 只管调用。
func init() {
	op.RegisterQuotaUsageProvider(func(userID uint, modelName string) (int64, int64, int64, time.Time, bool) {
		usedRPM, usedTPM, inFlight, resetAt := liveCounters(userID, modelName)
		return usedRPM, usedTPM, inFlight, resetAt, true
	})
}
