package op

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func quotaTestChannel(t *testing.T, name string) (model.Channel, []model.ChannelGrant) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, Enabled: true}, Shared: true, ReviewStatus: model.ChannelReviewApproved}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	var grants []model.ChannelGrant
	for i := 0; i < 2; i++ {
		key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: fmt.Sprint(i), Key: "secret", Enabled: true}}
		m := model.ChannelModel{ChannelID: channel.ID, Name: fmt.Sprint(i), Listed: true}
		if err := db.GetDB().Create(&key).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.GetDB().Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: m.ID, Protocols: model.ProtocolOpenAIChatCompletion}
		if err := db.GetDB().Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		grants = append(grants, grant)
	}
	return channel, grants
}

func TestChannelQuotaPausePersistentCacheExpiryAndIsolation(t *testing.T) {
	breakerTestSetup(t)
	if err := SettingUpsert(model.SettingKeyBreakerKeyHardMinutes, "37"); err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyBreakerKeyThreshold, "100"); err != nil {
		t.Fatal(err)
	}
	channel, grants := quotaTestChannel(t, "quota")
	other, otherGrants := quotaTestChannel(t, "healthy")
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	if err := ChannelStatsUpdate(channel.ID, model.StatsMetrics{RequestSuccess: 7}); err != nil {
		t.Fatal(err)
	}
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || stored.DisabledUntil == nil || time.Until(*stored.DisabledUntil).Round(time.Minute) != 37*time.Minute {
		t.Fatalf("wrong persisted pause: %+v", stored)
	}
	cached, _ := ChannelGet(channel.ID)
	if !ChannelQuotaPaused(channel.ID) || !cached.Enabled || cached.RequestSuccess != 7 || !cached.DisabledUntil.Equal(*stored.DisabledUntil) {
		t.Fatalf("wrong cached pause: %+v", cached)
	}
	for _, grant := range grants {
		if _, err := ChannelGrantGet(grant.ID); err == nil {
			t.Fatal("paused grant admitted")
		}
	}
	if _, err := SelectChannelKey(channel.ID, "failover"); err == nil {
		t.Fatal("paused channel key admitted")
	}
	if ChannelQuotaPaused(other.ID) {
		t.Fatal("other channel paused")
	}
	if _, err := ChannelGrantGet(otherGrants[0].ID); err != nil {
		t.Fatal(err)
	}
	// Every group reuses the live channel state rather than stale group copies.
	for _, grant := range grants {
		group := model.Group{Items: []model.GroupItem{{ChannelGrantID: grant.ID}, {ChannelGrantID: otherGrants[0].ID}}}
		snapshot := groupSnapshot(group, model.Scope{})
		if len(snapshot.Items) != 2 || snapshot.Items[0].Available || !snapshot.Items[1].Available {
			t.Fatalf("wrong group availability: %+v", snapshot.Items)
		}
	}
	ResetChannelKeyErrors(grants[0].ChannelKeyID)
	ResetChannelModelErrors(grants[0].ChannelModelID)
	if !ChannelQuotaPaused(channel.ID) {
		t.Fatal("single target success cleared channel quota pause")
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ChannelQuotaPaused(channel.ID) {
		t.Fatal("cache refresh/restart lost persistent pause")
	}
	expired := time.Now().Add(-time.Second)
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).Update("disabled_until", expired).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ChannelQuotaPaused(channel.ID) {
		t.Fatal("expired pause retained")
	}
	if _, err := ChannelGrantGet(grants[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectChannelKey(channel.ID, "failover"); err != nil {
		t.Fatal(err)
	}
	snapshot := groupSnapshot(model.Group{Items: []model.GroupItem{{ChannelGrantID: grants[1].ID}}}, model.Scope{})
	if len(snapshot.Items) != 1 || !snapshot.Items[0].Available {
		t.Fatal("expiry did not restore group")
	}
}

func TestChannelQuotaPauseNeverShortensAndMissingChannel(t *testing.T) {
	breakerTestSetup(t)
	channel, _ := quotaTestChannel(t, "long-pause")
	later := time.Now().Add(24 * time.Hour)
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).Update("disabled_until", later).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	cached, _ := ChannelGet(channel.ID)
	if cached.DisabledUntil == nil || !cached.DisabledUntil.Equal(later) {
		t.Fatal("existing pause shortened")
	}
	// A configuration writer holding a pre-error snapshot must not clear the pause.
	channel.DisabledUntil = nil
	setChannelCachePreservingQuota(channel)
	if !ChannelQuotaPaused(channel.ID) {
		t.Fatal("stale config snapshot cleared quota pause")
	}
	channelCache.Del(channel.ID)
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	if !ChannelQuotaPaused(channel.ID) {
		t.Fatal("cold-cache quota pause did not populate cache")
	}
	if err := RecordChannelQuotaError(-1); err == nil {
		t.Fatal("missing channel accepted")
	}
}

func TestChannelQuotaPauseSkipsRecoveryAndSurvivesInflightProbe(t *testing.T) {
	key := recoveryKey(t)
	if err := RecordChannelQuotaError(key.ChannelID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ProbeBreakerTarget = func(context.Context, int, int, int) error { calls++; return nil }
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("probe ran during channel quota pause")
	}
	var got model.ChannelKey
	if err := db.GetDB().First(&got, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.ProbeCount != 0 || got.DisabledUntil == nil {
		t.Fatal("skipped probe mutated child breaker")
	}
	// A quota failure arriving while an already-started child probe succeeds must survive.
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", key.ChannelID).Update("disabled_until", nil).Error; err != nil {
		t.Fatal(err)
	}
	ProbeBreakerTarget = func(context.Context, int, int, int) error { return RecordChannelQuotaError(key.ChannelID) }
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, key.ChannelID).Error; err != nil {
		t.Fatal(err)
	}
	if channel.DisabledUntil == nil || !channel.DisabledUntil.After(time.Now()) || !channel.Enabled {
		t.Fatal("single-target successful probe cleared channel pause")
	}
}

// quotaResumeChannel 建一个归属明确(41)的渠道, 并让第一个成员带凭据级与模型级熔断。
// 渠道级额度暂停与两者是三份独立状态: 恢复渠道时它们必须原样留着, 这正是本条用例要盯的点。
func quotaResumeChannel(t *testing.T) (model.Channel, []model.ChannelGrant) {
	t.Helper()
	channel, grants := quotaTestChannel(t, "resume")
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).Update("user_id", 41).Error; err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", grants[0].ChannelKeyID).
		Updates(map[string]any{"error_count": 3, "disabled_until": until}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&model.ChannelModel{}).Where("id = ?", grants[0].ChannelModelID).
		Updates(map[string]any{"error_count": 2, "disabled_until": until}).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	return channel, grants
}

// TestChannelQuotaResumeRestoresGroupAvailability 手动恢复的效果必须一路走到分组: 落库, 缓存, 成员可用性。
func TestChannelQuotaResumeRestoresGroupAvailability(t *testing.T) {
	breakerTestSetup(t)
	channel, grants := quotaResumeChannel(t)
	other, _ := quotaTestChannel(t, "resume-untouched")
	ctx := context.Background()
	scope := model.Scope{ID: 41, Role: model.RoleReseller}

	// 分组落库再刷新: 恢复流程自己会重建分组快照, 只塞缓存会被那次重建冲掉。
	group := model.Group{UserID: 41, Name: "quota-resume"}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for i, grant := range grants {
		item := model.GroupItem{GroupID: group.ID, ChannelGrantID: grant.ID, Priority: i + 1}
		if err := db.GetDB().Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := groupRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { groupCache.Del(group.ID) })

	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	// 另一条渠道同样进入额度暂停: 恢复只应作用于被点名的渠道。
	if err := RecordChannelQuotaError(other.ID); err != nil {
		t.Fatal(err)
	}
	// 暂停期间两个成员都不可用: 一个是被渠道级暂停挡住, 另一个另有凭据/模型级熔断。
	paused, err := GroupGet(group.ID, scope)
	if err != nil || len(paused.Items) != 2 || paused.Items[0].Available || paused.Items[1].Available {
		t.Fatalf("暂停期间成员可用性: %+v err=%v", paused.Items, err)
	}

	resumed, err := ChannelQuotaResume(channel.ID, scope, ctx)
	if err != nil || !resumed {
		t.Fatalf("resume = %v, %v", resumed, err)
	}
	if ChannelQuotaPaused(channel.ID) {
		t.Fatal("手动恢复后渠道仍处于额度暂停")
	}
	// 只清渠道级暂停: 人工开关与凭据/模型级熔断都保持原样, 否则真正的单项故障会被一起放行。
	var stored model.Channel
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DisabledUntil != nil || !stored.Enabled || stored.UserID != 41 {
		t.Fatalf("恢复改动了不该动的列: %+v", stored)
	}
	cached, err := ChannelGet(channel.ID)
	if err != nil || cached.DisabledUntil != nil {
		t.Fatalf("缓存未同步解除: %+v err=%v", cached, err)
	}
	if !ChannelQuotaPaused(other.ID) {
		t.Fatal("恢复一条渠道顺带解除了另一条渠道的额度暂停")
	}
	// 凭据级与模型级熔断是另外两份状态, 恢复渠道不该把它们一起清掉。
	var storedKey model.ChannelKey
	if err := db.GetDB().First(&storedKey, grants[0].ChannelKeyID).Error; err != nil {
		t.Fatal(err)
	}
	var storedModel model.ChannelModel
	if err := db.GetDB().First(&storedModel, grants[0].ChannelModelID).Error; err != nil {
		t.Fatal(err)
	}
	if storedKey.DisabledUntil == nil || storedKey.ErrorCount != 3 || storedModel.DisabledUntil == nil || storedModel.ErrorCount != 2 {
		t.Fatalf("恢复渠道动了子级熔断: key=%+v model=%+v", storedKey, storedModel)
	}
	restored, err := GroupGet(group.ID, scope)
	if err != nil || len(restored.Items) != 2 {
		t.Fatalf("恢复后分组读取: %+v err=%v", restored.Items, err)
	}
	if restored.Items[0].Available || !restored.Items[1].Available {
		t.Fatalf("恢复后成员可用性: %+v", restored.Items)
	}
	// 重复恢复没有可解除的东西, 必须返回 false 且不报错, 免得界面把它当失败。
	if resumed, err := ChannelQuotaResume(channel.ID, scope, ctx); err != nil || resumed {
		t.Fatalf("重复恢复 = %v, %v", resumed, err)
	}
}

// TestChannelQuotaResumeWithoutLivePause 没有活着的暂停(从未暂停 / 已过期)既不算一次解锁, 也不报错。
func TestChannelQuotaResumeWithoutLivePause(t *testing.T) {
	breakerTestSetup(t)
	channel, _ := quotaTestChannel(t, "no-live-pause")
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	// 管理员作用域同样走恢复入口: 本就没有暂停时不该落库, 也不该被算成解锁一条。
	admin := model.Scope{Role: model.RoleAdmin}
	if resumed, err := ChannelQuotaResume(channel.ID, admin, context.Background()); err != nil || resumed {
		t.Fatalf("无暂停时 resume = %v, %v", resumed, err)
	}
	expired := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).
		Update("disabled_until", expired).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resumed, err := ChannelQuotaResume(channel.ID, admin, context.Background()); err != nil || resumed {
		t.Fatalf("已过期时 resume = %v, %v", resumed, err)
	}
}

// TestChannelQuotaResumeKeepsManualDisable 恢复额度暂停不能顺手把人工停用的渠道打开。
func TestChannelQuotaResumeKeepsManualDisable(t *testing.T) {
	breakerTestSetup(t)
	channel, _ := quotaTestChannel(t, "manual-off")
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	if resumed, err := ChannelQuotaResume(channel.ID, model.Scope{ID: channel.UserID}, context.Background()); err != nil || !resumed {
		t.Fatalf("resume = %v, %v", resumed, err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("恢复额度暂停顺手打开了人工停用的渠道")
	}
}

// TestChannelQuotaResumeRejectsForeignScope 越权与不存在的渠道同口径报错, 且不改动暂停状态。
func TestChannelQuotaResumeRejectsForeignScope(t *testing.T) {
	breakerTestSetup(t)
	channel, _ := quotaTestChannel(t, "owned")
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	if resumed, err := ChannelQuotaResume(channel.ID, model.Scope{ID: 999, Role: model.RoleReseller}, context.Background()); err == nil || resumed {
		t.Fatalf("越权恢复被接受: %v, %v", resumed, err)
	}
	if !ChannelQuotaPaused(channel.ID) {
		t.Fatal("越权调用改动了暂停状态")
	}
	if _, err := ChannelQuotaResume(-1, model.Scope{Role: model.RoleAdmin}, context.Background()); err == nil {
		t.Fatal("不存在的渠道被接受")
	}
}

// TestChannelStatsExposesQuotaPause 管理台要能在列表里看到渠道级暂停: 字段必须真的拷到统计项与 JSON 上。
func TestChannelStatsExposesQuotaPause(t *testing.T) {
	breakerTestSetup(t)
	channel, _ := quotaTestChannel(t, "stats-pause")
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	admin := model.Scope{Role: model.RoleAdmin}
	var paused model.ChannelStats
	for _, item := range ChannelStatsList(admin) {
		if item.ChannelID == channel.ID {
			paused = item
		}
	}
	if paused.DisabledUntil == nil {
		t.Fatal("渠道统计列表漏拷 disabled_until")
	}
	raw, err := json.Marshal(paused)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"disabled_until"`) {
		t.Fatalf("统计项 JSON 缺 disabled_until: %s", raw)
	}
	if resumed, _ := ChannelQuotaResume(channel.ID, admin, context.Background()); !resumed {
		t.Fatal("resume failed")
	}
	for _, item := range ChannelStatsList(admin) {
		if item.ChannelID != channel.ID {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		// omitempty: 未暂停的渠道不该给出这个字段, 否则前端得自己按零值判断。
		if strings.Contains(string(raw), `"disabled_until"`) {
			t.Fatalf("未暂停仍外露 disabled_until: %s", raw)
		}
	}
}
