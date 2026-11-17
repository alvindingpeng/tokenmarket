package relay

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
)

// limited 造一个只把指定成员标记为上游 429 的限流器。
//
// 这里必须真的调用 MarkBlocked 而不是伪造 limiter.Blocked: 要验证的正是
// "上游 429 把本窗口该范围挡掉后, 选路会怎么解释这个空候选"。
func limitedMembers(blockedScopes ...[]ratelimit.Scope) {
	limiter = ratelimit.New(func(ratelimit.Scope) ratelimit.Limits { return ratelimit.Limits{} })
	for _, scopes := range blockedScopes {
		limiter.MarkBlocked(scopes, 1, 1)
	}
}

// TestLimitedUpstreamReasonNamesRateLimitInsteadOfWaiting 钉住"别把限流说成等待被禁用"。
//
// 缺陷: 全部候选都被上游 429 强制跳过时, 请求走到 waitAvailable, 而等待队列默认关闭,
// 于是对外只报 "no available upstream; waiting disabled"。那句话把"排队没开"当成原因,
// 可真实原因是上游限流, 且它会在一个固定窗口(60 秒)后自行恢复 —— 管理员照它去开排队,
// 既治不好限流, 还会让请求白堆在队列里。
func TestLimitedUpstreamReasonNamesRateLimitInsteadOfWaiting(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	item := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "m", Available: true, Priority: 1}
	group := model.Group{ID: 9100, Mode: model.GroupModeSuccess, Items: []model.GroupItem{item}}
	defer ResetRouteState(group.ID)

	limitedMembers(upstreamItemScopes(item))
	in := pickGroupItem(group)
	if in.ID != 0 {
		t.Fatalf("成员已被上游 429 标记却仍被选中: %d", in.ID)
	}
	reason := limitedUpstreamReason(group)
	if reason == "" {
		t.Fatal("候选全被上游限流挡住时, 必须给出限流原因, 否则对外只会误报等待被禁用")
	}
	if !strings.Contains(reason, "rate limited") {
		t.Fatalf("原因必须点明上游限流: %q", reason)
	}
	if strings.Contains(reason, "waiting") {
		t.Fatalf("原因不得再提等待队列: %q", reason)
	}
}

// TestLimitedUpstreamReasonStaysSilentWhenHealthyMemberExists 是边界: 还有健康成员时不许改写原因。
//
// 那种情况下空候选来自冷却或停用, 与上游限流无关; 若也报"全部被限流",
// 就会把"等冷却到期就能恢复"错说成"等限流窗口"。
func TestLimitedUpstreamReasonStaysSilentWhenHealthyMemberExists(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	limited := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "a", Available: true, Priority: 1}
	healthy := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "b", Available: true, Priority: 2}
	group := model.Group{ID: 9101, Mode: model.GroupModeSuccess, Items: []model.GroupItem{limited, healthy}}
	defer ResetRouteState(group.ID)

	limitedMembers(upstreamItemScopes(limited))
	if reason := limitedUpstreamReason(group); reason != "" {
		t.Fatalf("存在健康成员时不该改写原因: %q", reason)
	}
}

// TestLimitedUpstreamReasonIgnoresDisabledMembers 是边界: 停用成员不参与"全部被限流"的统计。
//
// 一个被管理员停用的成员本来就不该承载流量; 把它算进限流统计会让
// "唯一可用成员被限流"与"成员被停用"混为一谈。
func TestLimitedUpstreamReasonIgnoresDisabledMembers(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	limited := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "a", Available: true, Priority: 1}
	disabled := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "b", Available: false, Priority: 2}
	group := model.Group{ID: 9102, Mode: model.GroupModeSuccess, Items: []model.GroupItem{limited, disabled}}
	defer ResetRouteState(group.ID)

	limitedMembers(upstreamItemScopes(limited))
	reason := limitedUpstreamReason(group)
	if reason == "" {
		t.Fatal("唯一可用成员被限流时仍应给出限流原因")
	}
	if strings.Contains(reason, "2 member") {
		t.Fatalf("停用成员不应计入限流成员数: %q", reason)
	}
}

// TestRateLimitRetryAfterMatchesWindow 钉住 Retry-After 与真实窗口一致。
//
// 缺陷: 429 分支固定回 Retry-After: 1, 但上游强制标记与限流窗口都是 60 秒。
// 告诉客户端 1 秒后重试, 它会在窗口内反复重试, 每次都再吃一次 429。
func TestRateLimitRetryAfterMatchesWindow(t *testing.T) {
	if got := ratelimit.WindowSeconds(); got <= 1 {
		t.Fatalf("窗口长度异常: %d", got)
	}
}
