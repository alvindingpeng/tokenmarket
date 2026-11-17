package relay

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 429 退避的基数与倍数: 基数取成员重试间隔, 指数递增, 封顶 8 倍。
//
// 缺陷背景(线上实测 2026-11-17): ErrorClassRate 分支原本是裸 continue, 打完 429 立刻重选
// 同一成员再打。单账号订阅渠道只有一个候选, MaxTotalAttempts 的 10 次在约 2 秒内
// (实测每次 215~317ms)全部砸向已满的上游窗口, 把用户侧从"偶发限流"放大成"必然失败"。
func TestRateRetryWaitGrowsAndCaps(t *testing.T) {
	group := model.Group{RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeSuccess)}
	// 默认成员重试间隔是 1 秒; 显式写明, 避免将来改默认值让断言失去意义。
	group.RelayConfig.MemberRetryIntervalSeconds = 1
	cases := []struct {
		retry int
		want  time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 8 * time.Second}, // 封顶
	}
	for _, tc := range cases {
		if got := rateRetryWait(group, tc.retry); got != tc.want {
			t.Fatalf("retry=%d 退避时长: got %v want %v", tc.retry, got, tc.want)
		}
	}
}

// 间隔配置为 0 或负值时必须有可用基数: 否则"退避 0 秒"等于没退避, 又退回猛打上游。
func TestRateRetryWaitHasFloor(t *testing.T) {
	group := model.Group{RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeSuccess)}
	group.RelayConfig.MemberRetryIntervalSeconds = 0
	if got := rateRetryWait(group, 0); got != time.Second {
		t.Fatalf("基数非法时必须回退到 1 秒, got %v", got)
	}
}

// 次数上限: 超过上限不再重试, 交给调用方透传限流错误。
func TestRateRetryBackoffStopsAtBudget(t *testing.T) {
	group := model.Group{RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeSuccess)}
	group.RelayConfig.MemberRetryIntervalSeconds = 1
	state := &RequestState{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, ok := rateRetryBackoff(ctx, state, group, rateRetryBackoffMaxRetries); ok {
		t.Fatal("达到上限后不得继续重试: 否则一次必然失败的请求只会让用户多等")
	}
}

// 退避不得越过请求 deadline。
//
// 若不检查剩余预算, 请求会在退避里撞上超时, 用户拿到的是 504 而不是上游真实的 429 ——
// 错误归因被自己的重试逻辑改写。
func TestRateRetryBackoffRespectsDeadline(t *testing.T) {
	group := model.Group{RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeSuccess)}
	group.RelayConfig.MemberRetryIntervalSeconds = 1
	state := &RequestState{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	wait, ok := rateRetryBackoff(ctx, state, group, 0)
	if ok {
		t.Fatal("剩余预算(100ms)小于退避(1s)时必须放弃重试, 直接透传上游 429")
	}
	// 关键断言: 必须"立刻"返回, 而不是等到 ctx 自己超时。
	//
	// 只看 ok==false 抓不住缺陷: 去掉 deadline 检查后函数会进入 timer 等待,
	// 由 ctx.Done() 返回同一个 false —— 返回值一样, 但用户已经白等到超时,
	// 且这段等待挤掉了本该透传的上游真实 429。
	if wait != 0 {
		t.Fatalf("放弃重试时不应返回退避时长, got %v", wait)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("deadline 不足时必须立刻返回(不等满退避), 实际耗时 %v", elapsed)
	}
}
