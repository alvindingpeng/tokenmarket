package handlers

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/op"
)

// handlerTestResetCaches 清空跨包的进程级缓存, 必须在每次换库后调用。
//
// handlers 的用例各自建临时库, 而 op 包的用户/设置/限流策略缓存是进程级的。
// 不清理时, 一个用例写入的设置或策略会留给下一个用例, 表现为 -shuffle=on 下
// 随机失败(默认顺序下全绿, 所以长期没被发现)。
//
// 上界缓存(activityMax)与库身份绑定, 换库自然失效, 这里一并清掉是为了让
// "换库"这件事只有一个入口, 不必逐个记住哪个缓存该手动清。
func handlerTestResetCaches(t *testing.T) {
	t.Helper()
	op.ResetProcessCachesForTest()
	resetActivityMaxCacheForTest()
}
