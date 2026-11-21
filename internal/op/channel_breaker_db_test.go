package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// breakerTestSetup 初始化隔离测试库, 并把熔断参数调成"每次错误都熔断"以便观察时长递增。
func breakerTestSetup(t *testing.T) {
	t.Helper()
	opTestDBSetup(t, "breaker.db")
	for key, value := range map[model.SettingKey]string{
		model.SettingKeyBreakerKeyThreshold:   "1",
		model.SettingKeyBreakerKeyMinutes:     "5",
		model.SettingKeyBreakerModelThreshold: "1",
		model.SettingKeyBreakerModelMinutes:   "5",
		model.SettingKeyBreakerMaxMinutes:     "60",
	} {
		if err := SettingUpsert(key, value); err != nil {
			t.Fatalf("设置 %s: %v", key, err)
		}
	}
}

// createBreakerTestChannel 建一个测试渠道并返回其 ID: 凭据与渠道模型都有指向渠道的外键。
func createBreakerTestChannel(t *testing.T) int {
	t.Helper()
	channel := model.Channel{}
	channel.ChannelConfig = model.ChannelConfig{Name: "breaker-test", Enabled: true}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("创建渠道: %v", err)
	}
	return channel.ID
}

// TestRecordChannelKeyErrorEscalates 验证凭据级熔断: 每次熔断的隔离时长按累计次数翻倍, 封顶后不再增长。
// 这是本次改动的核心行为 —— 旧实现无论失败多少次都只停同样的 5 分钟, 一个 0% 成功率的上游
// 会永远在"停 5 分钟 → 探一次 → 再失败"的循环里打转。
func TestRecordChannelKeyErrorEscalates(t *testing.T) {
	breakerTestSetup(t)

	key := model.ChannelKey{ChannelID: createBreakerTestChannel(t)}
	key.ChannelKeyConfig = model.ChannelKeyConfig{Name: "k1", Key: "secret", Enabled: true}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("创建凭据: %v", err)
	}
	channelKeyCache.Set(key.ID, key)

	want := []struct {
		trips    int
		duration time.Duration
	}{
		{1, 5 * time.Minute},
		{2, 10 * time.Minute},
		{3, 20 * time.Minute},
		{4, 40 * time.Minute},
		{5, 60 * time.Minute}, // 80 分钟超过上限, 取 60
	}
	for _, expect := range want {
		RecordChannelKeyError(key.ID, "overload")

		var reloaded model.ChannelKey
		if err := db.GetDB().First(&reloaded, key.ID).Error; err != nil {
			t.Fatalf("重读凭据: %v", err)
		}
		if reloaded.BreakerTrips != expect.trips {
			t.Fatalf("第 %d 次熔断后 breaker_trips = %d", expect.trips, reloaded.BreakerTrips)
		}
		if reloaded.DisabledUntil == nil {
			t.Fatalf("第 %d 次熔断后应有 disabled_until", expect.trips)
		}
		if got := time.Until(*reloaded.DisabledUntil).Round(time.Minute); got != expect.duration {
			t.Fatalf("第 %d 次熔断时长 = %v, want %v", expect.trips, got, expect.duration)
		}
		cached, ok := channelKeyCache.Get(key.ID)
		if !ok || cached.DisabledUntil == nil || !cached.DisabledUntil.Equal(*reloaded.DisabledUntil) {
			t.Fatalf("缓存未同步熔断状态: %+v", cached)
		}
	}
}

// TestResetChannelKeyErrorsClearsTrips 验证成功一次就清零: 否则自愈的成员会被历史熔断次数继续压着。
func TestResetChannelKeyErrorsClearsTrips(t *testing.T) {
	breakerTestSetup(t)

	key := model.ChannelKey{ChannelID: createBreakerTestChannel(t)}
	key.ChannelKeyConfig = model.ChannelKeyConfig{Name: "k2", Key: "secret", Enabled: true}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("创建凭据: %v", err)
	}
	RecordChannelKeyError(key.ID, "overload")
	RecordChannelKeyError(key.ID, "overload")

	ResetChannelKeyErrors(key.ID)

	var reloaded model.ChannelKey
	if err := db.GetDB().First(&reloaded, key.ID).Error; err != nil {
		t.Fatalf("重读凭据: %v", err)
	}
	if reloaded.BreakerTrips != 0 || reloaded.ErrorCount != 0 || reloaded.DisabledUntil != nil {
		t.Fatalf("成功后应清零并解除熔断, 实得 trips=%d error_count=%d disabled_until=%v", reloaded.BreakerTrips, reloaded.ErrorCount, reloaded.DisabledUntil)
	}
}

// TestRecordChannelKeyAuthErrorEscalates 验证鉴权/额度错误走硬熔断时同样递增。
func TestRecordChannelKeyAuthErrorEscalates(t *testing.T) {
	breakerTestSetup(t)
	if err := SettingUpsert(model.SettingKeyBreakerKeyHardMinutes, "30"); err != nil {
		t.Fatalf("设置硬熔断时长: %v", err)
	}

	key := model.ChannelKey{ChannelID: createBreakerTestChannel(t)}
	key.ChannelKeyConfig = model.ChannelKeyConfig{Name: "k3", Key: "dead", Enabled: true}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("创建凭据: %v", err)
	}

	RecordChannelKeyError(key.ID, "auth")
	var first model.ChannelKey
	if err := db.GetDB().First(&first, key.ID).Error; err != nil {
		t.Fatalf("重读凭据: %v", err)
	}
	if got := time.Until(*first.DisabledUntil).Round(time.Minute); got != 30*time.Minute {
		t.Fatalf("首次鉴权熔断 = %v, want 30m", got)
	}

	RecordChannelKeyError(key.ID, "auth")
	var second model.ChannelKey
	if err := db.GetDB().First(&second, key.ID).Error; err != nil {
		t.Fatalf("重读凭据: %v", err)
	}
	if got := time.Until(*second.DisabledUntil).Round(time.Minute); got != 60*time.Minute {
		t.Fatalf("二次鉴权熔断 = %v, want 60m", got)
	}
}

// TestRecordChannelModelErrorEscalates 验证渠道模型级熔断同样递增。
func TestRecordChannelModelErrorEscalates(t *testing.T) {
	breakerTestSetup(t)

	channelModel := model.ChannelModel{ChannelID: createBreakerTestChannel(t), Name: "gpt-test"}
	if err := db.GetDB().Create(&channelModel).Error; err != nil {
		t.Fatalf("创建渠道模型: %v", err)
	}
	channelModelCache.Set(channelModel.ID, channelModel)

	RecordChannelModelError(channelModel.ID, "overload")
	var first model.ChannelModel
	if err := db.GetDB().First(&first, channelModel.ID).Error; err != nil {
		t.Fatalf("重读渠道模型: %v", err)
	}
	if got := time.Until(*first.DisabledUntil).Round(time.Minute); got != 5*time.Minute {
		t.Fatalf("首次模型熔断 = %v, want 5m", got)
	}

	RecordChannelModelError(channelModel.ID, "overload")
	var second model.ChannelModel
	if err := db.GetDB().First(&second, channelModel.ID).Error; err != nil {
		t.Fatalf("重读渠道模型: %v", err)
	}
	if second.BreakerTrips != 2 {
		t.Fatalf("breaker_trips = %d, want 2", second.BreakerTrips)
	}
	if got := time.Until(*second.DisabledUntil).Round(time.Minute); got != 10*time.Minute {
		t.Fatalf("二次模型熔断 = %v, want 10m", got)
	}
	cached, ok := channelModelCache.Get(channelModel.ID)
	if !ok || cached.DisabledUntil == nil || !cached.DisabledUntil.Equal(*second.DisabledUntil) {
		t.Fatalf("模型缓存未同步熔断状态: %+v", cached)
	}
	ResetChannelModelErrors(channelModel.ID)
	cached, ok = channelModelCache.Get(channelModel.ID)
	if !ok || cached.DisabledUntil != nil || cached.ErrorCount != 0 || cached.BreakerTrips != 0 {
		t.Fatalf("模型缓存未同步重置状态: %+v", cached)
	}
}

// TestBreakerResetPathsClearProbeCount 三条人工/成功重置出口都必须把探测计数归零。
//
// 自动探测的准入条件是 probe_count < BreakerProbeLimit, 而只有**探测成功**才会给它清零。
// 于是探满 3 次都没探通的成员会永久退出自动探测队列: 一旦再崩, 熔断时长按 breaker_trips
// 指数翻到 60 分钟上限, 而且没有探活帮它提前恢复, 只能等人来点。三条重置路径原先都只清
// error_count / disabled_until(其中 ChannelBreakerReset 连 breaker_trips 都不清), 正是这个洞。
func TestBreakerResetPathsClearProbeCount(t *testing.T) {
	breakerTestSetup(t)
	channelID := createBreakerTestChannel(t)
	// 人工解锁路径读 channelCache, 建行不会自动进缓存。
	seedChannelCache(t, channelID)
	until := time.Now().Add(-time.Minute)
	probedAt := time.Now().Add(-3 * time.Minute)
	// 探满上限的成员: probe_count == 3 正是"已经不会再被自动探测"的状态。
	exhausted := BreakerProbeLimit

	key := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "k-probe", Key: "secret", Enabled: true,
		ErrorCount: 3, BreakerTrips: 4, DisabledUntil: &until,
		ProbeCount: exhausted, ProbeAt: &probedAt,
	}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("创建凭据: %v", err)
	}
	channelKeyCache.Set(key.ID, key)
	t.Cleanup(func() { channelKeyCache.Del(key.ID) })

	channelModel := model.ChannelModel{ChannelID: channelID, Name: "m-probe",
		ErrorCount: 3, BreakerTrips: 4, DisabledUntil: &until,
		ProbeCount: exhausted, ProbeAt: &probedAt,
	}
	if err := db.GetDB().Create(&channelModel).Error; err != nil {
		t.Fatalf("创建渠道模型: %v", err)
	}
	channelModelCache.Set(channelModel.ID, channelModel)
	t.Cleanup(func() { channelModelCache.Del(channelModel.ID) })

	assertKeyReset := func(t *testing.T, source string) {
		t.Helper()
		var got model.ChannelKey
		if err := db.GetDB().First(&got, key.ID).Error; err != nil {
			t.Fatalf("%s 重读凭据: %v", source, err)
		}
		if got.ProbeCount != 0 || got.ProbeAt != nil {
			t.Fatalf("%s 后 probe_count=%d probe_at=%v, want 0/nil (留着等于永久放弃自动探测)", source, got.ProbeCount, got.ProbeAt)
		}
		if got.ErrorCount != 0 || got.DisabledUntil != nil || got.BreakerTrips != 0 {
			t.Fatalf("%s 后 error_count=%d disabled_until=%v breaker_trips=%d, 期望全清", source, got.ErrorCount, got.DisabledUntil, got.BreakerTrips)
		}
		cached, ok := channelKeyCache.Get(key.ID)
		if !ok || cached.ProbeCount != 0 || cached.ProbeAt != nil || cached.BreakerTrips != 0 {
			t.Fatalf("%s 后缓存未同步探测态: %+v", source, cached)
		}
	}
	assertModelReset := func(t *testing.T, source string) {
		t.Helper()
		var got model.ChannelModel
		if err := db.GetDB().First(&got, channelModel.ID).Error; err != nil {
			t.Fatalf("%s 重读渠道模型: %v", source, err)
		}
		if got.ProbeCount != 0 || got.ProbeAt != nil {
			t.Fatalf("%s 后 probe_count=%d probe_at=%v, want 0/nil", source, got.ProbeCount, got.ProbeAt)
		}
		if got.ErrorCount != 0 || got.DisabledUntil != nil || got.BreakerTrips != 0 {
			t.Fatalf("%s 后 error_count=%d disabled_until=%v breaker_trips=%d, 期望全清", source, got.ErrorCount, got.DisabledUntil, got.BreakerTrips)
		}
		cached, ok := channelModelCache.Get(channelModel.ID)
		if !ok || cached.ProbeCount != 0 || cached.ProbeAt != nil || cached.BreakerTrips != 0 {
			t.Fatalf("%s 后缓存未同步探测态: %+v", source, cached)
		}
	}

	t.Run("ResetChannelKeyErrors", func(t *testing.T) {
		ResetChannelKeyErrors(key.ID)
		assertKeyReset(t, "ResetChannelKeyErrors")
	})
	t.Run("ResetChannelModelErrors", func(t *testing.T) {
		ResetChannelModelErrors(channelModel.ID)
		assertModelReset(t, "ResetChannelModelErrors")
	})
	t.Run("ChannelBreakerReset", func(t *testing.T) {
		// 上两个子用例已经把行清干净了, 这里重新造一份探满上限的状态再走人工解锁路径。
		exhaustedUntil := time.Now().Add(-time.Minute)
		if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", key.ID).Updates(map[string]any{
			"error_count": 3, "breaker_trips": 4, "disabled_until": &exhaustedUntil,
			"probe_count": exhausted, "probe_at": &probedAt,
		}).Error; err != nil {
			t.Fatalf("重造凭据状态: %v", err)
		}
		if err := db.GetDB().Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).Updates(map[string]any{
			"error_count": 3, "breaker_trips": 4, "disabled_until": &exhaustedUntil,
			"probe_count": exhausted, "probe_at": &probedAt,
		}).Error; err != nil {
			t.Fatalf("重造模型状态: %v", err)
		}
		// 重造的是库内行, 缓存里仍是刚才清空的那份: 人工解锁读的是缓存, 得把它同步回去。
		cachedKey, _ := channelKeyCache.Get(key.ID)
		cachedKey.ErrorCount, cachedKey.BreakerTrips = 3, 4
		cachedKey.DisabledUntil, cachedKey.ProbeCount, cachedKey.ProbeAt = &exhaustedUntil, exhausted, &probedAt
		channelKeyCache.Set(key.ID, cachedKey)
		cachedModel, _ := channelModelCache.Get(channelModel.ID)
		cachedModel.ErrorCount, cachedModel.BreakerTrips = 3, 4
		cachedModel.DisabledUntil, cachedModel.ProbeCount, cachedModel.ProbeAt = &exhaustedUntil, exhausted, &probedAt
		channelModelCache.Set(channelModel.ID, cachedModel)

		resetCount, err := ChannelBreakerReset(channelID, []string{"k-probe"}, []string{"m-probe"}, model.Scope{Role: model.RoleAdmin}, context.Background())
		if err != nil {
			t.Fatalf("ChannelBreakerReset: %v", err)
		}
		if resetCount != 2 {
			t.Fatalf("reset = %d, want 2 (凭据 + 模型各一条)", resetCount)
		}
		assertKeyReset(t, "ChannelBreakerReset")
		assertModelReset(t, "ChannelBreakerReset")
	})
}

// TestBreakerProbeGateReopensAfterReset 重置后探测 gate 必须重新放行:
// 这是上面那组断言的**行为侧**验证 —— 只断言字段归零不足以说明"下一次还能被探到"。
func TestBreakerProbeGateReopensAfterReset(t *testing.T) {
	breakerTestSetup(t)
	channelID := createBreakerTestChannel(t)
	seedChannelCache(t, channelID)
	until := time.Now().Add(-time.Minute)
	probedAt := time.Now().Add(-3 * time.Minute)
	key := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "k-gate", Key: "secret", Enabled: true, DisabledUntil: &until,
		ProbeCount: BreakerProbeLimit, ProbeAt: &probedAt,
	}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("创建凭据: %v", err)
	}
	channelKeyCache.Set(key.ID, key)
	t.Cleanup(func() { channelKeyCache.Del(key.ID) })

	oldTarget := ProbeBreakerTarget
	t.Cleanup(func() { ProbeBreakerTarget = oldTarget })
	var probed int
	ProbeBreakerTarget = func(context.Context, int, int, int) error { probed++; return nil }

	// 探满上限且熔断已到期: 本轮不该被选中(probe_count 到顶)。
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatalf("BreakerRecoveryProbe(探满): %v", err)
	}
	if probed != 0 {
		t.Fatalf("探满上限后仍被探测 %d 次, want 0 (准入条件是 probe_count < 上限)", probed)
	}

	ResetChannelKeyErrors(key.ID)

	// 重置把 disabled_until 也清掉了, 成员此刻是"健康"的, 自然不该被探测。
	// 要验证 gate 真的重新放行, 得让它再崩一次: 新的一次熔断到期后(disabled_until 已过期)
	// 必须能被自动探测 —— 而这正是原先 probe_count 残留到顶时做不到的事。
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", key.ID).
		Update("disabled_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("重造到期熔断: %v", err)
	}
	cached, _ := channelKeyCache.Get(key.ID)
	expiredUntil := time.Now().Add(-time.Minute)
	cached.DisabledUntil = &expiredUntil
	channelKeyCache.Set(key.ID, cached)

	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatalf("BreakerRecoveryProbe(重置后再崩): %v", err)
	}
	if probed != 1 {
		t.Fatalf("重置后再崩时探测 %d 次, want 1 (gate 必须重新放行; 未重置时 probe_count 到顶会永久放弃)", probed)
	}
}

// seedChannelCache 把刚建好的渠道行放进 channelCache: 部分路径(如 ChannelBreakerReset)读缓存而非库。
func seedChannelCache(t *testing.T, channelID int) {
	t.Helper()
	var channel model.Channel
	if err := db.GetDB().First(&channel, channelID).Error; err != nil {
		t.Fatalf("重读渠道: %v", err)
	}
	channelCache.Set(channel.ID, channel)
	t.Cleanup(func() { channelCache.Del(channel.ID) })
}
