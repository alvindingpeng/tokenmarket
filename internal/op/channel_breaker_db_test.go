package op

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// breakerTestSetup 初始化隔离测试库, 并把熔断参数调成"每次错误都熔断"以便观察时长递增。
func breakerTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "breaker.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
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
	if reloaded.BreakerTrips != 0 || reloaded.ErrorCount != 0 {
		t.Fatalf("成功后应清零, 实得 trips=%d error_count=%d", reloaded.BreakerTrips, reloaded.ErrorCount)
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
}
