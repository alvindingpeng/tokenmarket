package relay

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
)

// TestQuotaUsageProviderRegistered 钉住 relay -> op 的注入接缝。
//
// 这是本批改动里唯一可能静默断掉的地方: 注入在 init 里完成,
// 若注册函数被改名或 init 未执行, op 侧 provider 为 nil, 接口直接 500。
// 各处单测都自己 mock provider, 发现不了; 这里断言"已就绪"这一层。
func TestQuotaUsageProviderRegistered(t *testing.T) {
	if !op.QuotaUsageProviderReady() {
		t.Fatal("provider not registered: 接口会 500, 页面会永远停在加载失败")
	}
}

// TestQuotaUsageReadsLiveCounters 断言注入的查询读到的是限流器的真实计数。
//
// 只把用量写进桶里再读回来, 不碰数据库: 目的就是验证 relay 注入的那个函数
// 用的是同一个 limiter 实例 —— 用别的实例会读回全零, 页面就永远显示"用了 0"。
func TestQuotaUsageReadsLiveCounters(t *testing.T) {
	const userID = 424242
	scope := ratelimit.Scope{Kind: model.RateScopeUser, ID: userID}
	decision := limiter.Reserve([]ratelimit.Scope{scope}, 100, 50)
	if !decision.Allowed {
		t.Fatalf("reserve rejected unexpectedly: %+v", decision)
	}
	t.Cleanup(func() { decision.Reservation.Settle(100, 50, true) })

	usedRPM, usedTPM, inFlight, resetAt := liveCounters(userID, "")
	if usedRPM < 1 {
		t.Fatalf("usedRPM = %d, want >= 1 (预留后应能看到用量)", usedRPM)
	}
	if usedTPM < 1 {
		t.Fatalf("usedTPM = %d, want >= 1", usedTPM)
	}
	if inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1 (预留持有 1 个并发租约)", inFlight)
	}
	if resetAt.IsZero() || !resetAt.After(time.Now().Add(-2*time.Minute)) {
		t.Fatalf("resetAt = %v, want a plausible window boundary", resetAt)
	}
}
