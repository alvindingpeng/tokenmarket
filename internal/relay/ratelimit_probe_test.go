package relay

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
)

// TestRateLimitedMemberGetsProbeInsteadOfBeingBanished 钉住"触顶成员也要能探测"。
//
// 缺陷: 选路把上游 429 触顶的成员直接 continue 出局, 整个 60 秒窗口内没有任何候选。
// 线上代价(实测): 订阅渠道只有一个账号, 一次瞬时 429 之后每个请求都在 2~3 毫秒内
// 被网关拒绝 —— 连一次上游都没打。而上游往往几秒就恢复了, 网关却空等到窗口重置。
// 用户侧表现就是"一个账号都没跑通"。
func TestRateLimitedMemberGetsProbeInsteadOfBeingBanished(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	item := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "m", Available: true, Priority: 1}
	group := model.Group{ID: 9200, Mode: model.GroupModeSuccess, Items: []model.GroupItem{item}}
	defer ResetRouteState(group.ID)

	limitedMembers(upstreamItemScopes(item))
	chosen := pickGroupItem(group)
	if chosen.ID == 0 {
		t.Fatal("触顶成员被完全排除: 该请求连一次上游都不会打, 只能空等窗口重置")
	}
	if chosen.ID != item.ID {
		t.Fatalf("应选中唯一的触顶成员做探测: got %d", chosen.ID)
	}
}

// TestProbeSuccessClearsRateLimitBlock 钉住"探测成功必须解除封锁"。
//
// 只放开探测而不撤销标记, 探测就只证明了上游已恢复, 却解不开那个锁:
// 后续请求仍被判触顶, 直到 60 秒窗口自然结束。
func TestProbeSuccessClearsRateLimitBlock(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	item := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "m", Available: true, Priority: 1}
	scopes := upstreamItemScopes(item)

	limitedMembers(scopes)
	if !upstreamItemLimited(item) {
		t.Fatal("前置条件: 成员应处于上游 429 触顶状态")
	}

	limiter.ClearBlocked(scopes)
	if upstreamItemLimited(item) {
		t.Fatal("探测成功后标记应被撤销: 否则后续请求继续被判触顶直到窗口重置")
	}
}

// TestClearBlockedDoesNotAffectOtherScopes 是边界: 只撤自己那一份标记。
//
// 渠道/凭据/模型是三个独立范围; 清理一个成员不该顺手放开别的成员,
// 否则一个成员恢复会把另一个确实受限的成员一起放回选路。
func TestClearBlockedDoesNotAffectOtherScopes(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	a := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "a", Available: true, Priority: 1}
	b := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "b", Available: true, Priority: 1}
	limitedMembers(upstreamItemScopes(a), upstreamItemScopes(b))

	limiter.ClearBlocked(upstreamItemScopes(a))
	if upstreamItemLimited(a) {
		t.Fatal("a 的标记应被撤销")
	}
	if !upstreamItemLimited(b) {
		t.Fatal("清理 a 却把 b 的标记一起放开: 会把确实受限的成员放回选路")
	}
}

// TestProbeIsSingleSlotForLimitedMembers 是边界: 触顶成员同时只占一个探测名额。
//
// 若每个触顶成员都能无限探测, 一次窗口内的 429 风暴会把所有受限成员反复打向上游,
// 反而放大限流。
func TestProbeIsSingleSlotForLimitedMembers(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()

	a := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "a", Available: true, Priority: 1}
	b := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "b", Available: true, Priority: 2}
	group := model.Group{ID: 9201, Mode: model.GroupModeSuccess, Items: []model.GroupItem{a, b}}
	defer ResetRouteState(group.ID)

	limitedMembers(upstreamItemScopes(a), upstreamItemScopes(b))
	first := pickGroupItem(group)
	if first.ID == 0 {
		t.Fatal("至少应放一个探测")
	}
	second := pickGroupItem(group)
	if second.ID != 0 {
		t.Fatalf("探测名额应只有一个, 否则一次 429 风暴会被反复放大: got %d", second.ID)
	}
	_ = ratelimit.WindowSeconds
}
