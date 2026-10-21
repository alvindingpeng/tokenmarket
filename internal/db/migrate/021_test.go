package migrate

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestGrandfatherExistingChannels 验证存量渠道一次性转正(P2 迁移 021)。
//
// 这个迁移防的是一次静默线上回归: trust_level 列在 P1 就随 AutoMigrate 建出且默认 observing,
// 所有存量渠道因此都是 observing 但没有 observation_start。若不迁移, 首次 channel_trust_eval
// 会把它们全部判成「到期无样本 ⇒ 不达标 ⇒ probation」, 给全站存量渠道评分统一乘 0.70。
func TestGrandfatherExistingChannels(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 存量渠道: 列默认 observing, 但从未进入观察期(无窗口); 已过审是放行的前提。
	legacy := model.Channel{
		ChannelConfig: model.ChannelConfig{Name: "legacy"},
		ReviewStatus:  model.ChannelReviewApproved,
	}
	if err := conn.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	// 真正在观察期内的新渠道: 有窗口, 必须保持原状。
	observing := model.Channel{
		ChannelConfig:    model.ChannelConfig{Name: "new-observing"},
		TrustLevel:       model.ChannelTrustObserving,
		ObservationStart: ptrTime(),
		ObservationUntil: ptrTime(),
	}
	if err := conn.Create(&observing).Error; err != nil {
		t.Fatalf("create observing: %v", err)
	}
	// 已收回的渠道: 有窗口且已判定为 revoked, 不该被迁移洗白。
	revoked := model.Channel{
		ChannelConfig:    model.ChannelConfig{Name: "revoked"},
		TrustLevel:       model.ChannelTrustRevoked,
		ObservationStart: ptrTime(),
		ObservationUntil: ptrTime(),
	}
	if err := conn.Create(&revoked).Error; err != nil {
		t.Fatalf("create revoked: %v", err)
	}

	if err := grandfatherExistingChannels(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	load := func(id int) model.Channel {
		var channel model.Channel
		if err := conn.First(&channel, id).Error; err != nil {
			t.Fatalf("load %d: %v", id, err)
		}
		return channel
	}
	if got := load(legacy.ID).TrustLevel; got != model.ChannelTrustTrusted {
		t.Fatalf("legacy trust_level = %q, want trusted", got)
	}
	if got := load(observing.ID).TrustLevel; got != model.ChannelTrustObserving {
		t.Fatalf("observing trust_level = %q, want observing (must not be touched)", got)
	}
	if got := load(revoked.ID).TrustLevel; got != model.ChannelTrustRevoked {
		t.Fatalf("revoked trust_level = %q, want revoked (must not be whitewashed)", got)
	}
}

// TestGrandfatherExistingChannelsIdempotent 重复执行必须无副作用: 迁移会因失败重试而多次运行。
func TestGrandfatherExistingChannelsIdempotent(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	legacy := model.Channel{
		ChannelConfig: model.ChannelConfig{Name: "legacy"},
		ReviewStatus:  model.ChannelReviewApproved,
	}
	if err := conn.Create(&legacy).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := grandfatherExistingChannels(conn); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var channel model.Channel
	if err := conn.First(&channel, legacy.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if channel.TrustLevel != model.ChannelTrustTrusted {
		t.Fatalf("trust_level = %q, want trusted", channel.TrustLevel)
	}
}

// ptrTime 返回一个当前时间指针, 用于构造观察期窗口。
func ptrTime() *time.Time {
	now := time.Now()
	return &now
}

// TestGrandfatherSkipsUnapprovedChannels 验证迁移不碰未过审渠道。
//
// 部署时刻还停在 draft/pending 的渠道尚未经历新流程。若迁移把它们也标成 trusted,
// 它们日后审核通过时 ChannelTrustStart 会因「已是 trusted」而跳过观察期 —— 等于开后门。
func TestGrandfatherSkipsUnapprovedChannels(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pending := model.Channel{
		ChannelConfig: model.ChannelConfig{Name: "pending"},
		ReviewStatus:  model.ChannelReviewPending,
	}
	if err := conn.Create(&pending).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := grandfatherExistingChannels(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var channel model.Channel
	if err := conn.First(&channel, pending.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if channel.TrustLevel == model.ChannelTrustTrusted {
		t.Fatalf("pending channel was grandfathered to trusted — it would skip observation on approval")
	}
}
