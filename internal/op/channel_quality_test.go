package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// trustTestEnv 建独立库并预热缓存; 每个测试用独立 TempDir, 互不串库。
// 观察期评估走 db.GetDB() 全局句柄与 channelCache, 故两者都要初始化。
func trustTestEnv(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel_quality.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 观察期门槛显式钉死, 断言不依赖默认设置取值。
	if err := SettingUpsert(model.SettingKeyChannelObservationDays, "7"); err != nil {
		t.Fatalf("set observation days: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyChannelObservationMinSamples, "100"); err != nil {
		t.Fatalf("set min samples: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyChannelObservationSuccessFlr, "0.98"); err != nil {
		t.Fatalf("set success floor: %v", err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

// seedObservationChannel 建一个处于观察期的渠道, 并写入昨日统计数据。
// startDaysAgo 决定观察期是否已到期: >7 表示已到期, 小值表示仍在观察中。
func seedObservationChannel(t *testing.T, success, failed int64, startDaysAgo int) model.Channel {
	t.Helper()
	start := time.Now().AddDate(0, 0, -startDaysAgo)
	until := start.AddDate(0, 0, 7)
	channel := model.Channel{
		ChannelConfig:    model.ChannelConfig{Name: "obs-" + time.Now().Format("150405.000000")},
		TrustLevel:       model.ChannelTrustObserving,
		ObservationStart: &start,
		ObservationUntil: &until,
		// 观察期只针对已过审渠道: 未过审渠道本就不能发布, 对它们做降级没有意义。
		ReviewStatus: model.ChannelReviewApproved,
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if success > 0 || failed > 0 {
		daily := model.StatsChannelDaily{
			ChannelID: channel.ID,
			Date:      time.Now().AddDate(0, 0, -1).Format("20060102"),
			StatsMetrics: model.StatsMetrics{
				RequestSuccess: success,
				RequestFailed:  failed,
				WaitTime:       100 * (success + failed),
			},
		}
		if err := db.GetDB().Create(&daily).Error; err != nil {
			t.Fatalf("create daily stats: %v", err)
		}
	}
	return channel
}

// loadTrust 读回渠道的信任级别与观察期截止时间。
func loadTrust(t *testing.T, id int) model.Channel {
	t.Helper()
	var channel model.Channel
	if err := db.GetDB().First(&channel, id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	return channel
}

// TestChannelTrustEvaluatePromote 达标渠道应转正。
// 100 样本 + 99% 成功率, 观察期已到期, 门槛 100 样本 / 98% → trusted。
func TestChannelTrustEvaluatePromote(t *testing.T) {
	trustTestEnv(t)
	channel := seedObservationChannel(t, 99, 1, 10)

	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := loadTrust(t, channel.ID); got.TrustLevel != model.ChannelTrustTrusted {
		t.Fatalf("trust_level = %q, want trusted", got.TrustLevel)
	}
	// 转正必须留审计, 便于事后解释。
	var audits int64
	db.GetDB().Model(&model.AuditLog{}).Where("action = ?", "channel.trust_promote").Count(&audits)
	if audits != 1 {
		t.Fatalf("promote audits = %d, want 1", audits)
	}
}

// TestChannelTrustEvaluateProbationThenRevoke 首次不达标进 probation, 再不合格进 revoked。
// 这验证「先给一次机会再收回」的两段式降级, 而不是一次判死。
func TestChannelTrustEvaluateProbationThenRevoke(t *testing.T) {
	trustTestEnv(t)
	// 成功率 50%, 观察期已到期 → 首次评估应进 probation。
	channel := seedObservationChannel(t, 50, 50, 10)

	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate 1: %v", err)
	}
	afterFirst := loadTrust(t, channel.ID)
	if afterFirst.TrustLevel != model.ChannelTrustProbation {
		t.Fatalf("after first eval trust_level = %q, want probation", afterFirst.TrustLevel)
	}
	if afterFirst.ObservationUntil == nil || !afterFirst.ObservationUntil.After(time.Now()) {
		t.Fatalf("probation should extend observation_until into the future")
	}

	// 第二轮仍不达标 → revoked。
	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate 2: %v", err)
	}
	if got := loadTrust(t, channel.ID); got.TrustLevel != model.ChannelTrustRevoked {
		t.Fatalf("after second eval trust_level = %q, want revoked", got.TrustLevel)
	}
}

// TestChannelTrustEvaluateWaitsForSamples 观察期未到期且样本不足时应保持 observing。
// 这是「新渠道刚上线不要被立刻判死」的核心保护。
func TestChannelTrustEvaluateWaitsForSamples(t *testing.T) {
	trustTestEnv(t)
	// 只有 10 个样本, 观察期还剩 6 天。
	channel := seedObservationChannel(t, 10, 0, 1)

	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := loadTrust(t, channel.ID); got.TrustLevel != model.ChannelTrustObserving {
		t.Fatalf("trust_level = %q, want observing (samples too few, not expired)", got.TrustLevel)
	}
}

// TestChannelTrustEvaluateExpiredNoSamples 观察期已到期但完全没样本 → probation(无法证明质量)。
func TestChannelTrustEvaluateExpiredNoSamples(t *testing.T) {
	trustTestEnv(t)
	channel := seedObservationChannel(t, 0, 0, 10)

	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := loadTrust(t, channel.ID); got.TrustLevel != model.ChannelTrustProbation {
		t.Fatalf("trust_level = %q, want probation (expired without samples)", got.TrustLevel)
	}
}

// TestChannelTrustStartKeepsTrusted 已转正的渠道复审时不应被重置回观察期。
// 复审(stale→approved)不该让稳定渠道重新跑一轮观察期, 否则一次配置微调就丢光信任。
func TestChannelTrustStartKeepsTrusted(t *testing.T) {
	trustTestEnv(t)
	start := time.Now().AddDate(0, 0, -30)
	channel := model.Channel{
		ChannelConfig:    model.ChannelConfig{Name: "trusted-one"},
		TrustLevel:       model.ChannelTrustTrusted,
		ObservationStart: &start,
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ChannelTrustStart(db.GetDB(), channel.ID); err != nil {
		t.Fatalf("trust start: %v", err)
	}
	if got := loadTrust(t, channel.ID); got.TrustLevel != model.ChannelTrustTrusted {
		t.Fatalf("trust_level = %q, want trusted (must not reset)", got.TrustLevel)
	}
}

// TestChannelTrustStartSetsObservation 未转正渠道应被置入观察期并写入起止时间。
func TestChannelTrustStartSetsObservation(t *testing.T) {
	trustTestEnv(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "fresh-one"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ChannelTrustStart(db.GetDB(), channel.ID); err != nil {
		t.Fatalf("trust start: %v", err)
	}
	got := loadTrust(t, channel.ID)
	if got.TrustLevel != model.ChannelTrustObserving {
		t.Fatalf("trust_level = %q, want observing", got.TrustLevel)
	}
	if got.ObservationStart == nil || got.ObservationUntil == nil {
		t.Fatalf("observation window not set: start=%v until=%v", got.ObservationStart, got.ObservationUntil)
	}
	// 截止时间应约为 7 天后(设置里钉死 7 天)。
	if days := got.ObservationUntil.Sub(*got.ObservationStart).Hours() / 24; days < 6.9 || days > 7.1 {
		t.Fatalf("observation window = %.2f days, want ~7", days)
	}
}

// TestChannelTrustEvaluateSkipsNoWindow 验证没有观察期窗口的渠道不参与判定。
//
// 存量渠道在 P1 迁移时就带着 trust_level 默认值 observing, 但从未进入过观察期(无窗口)。
// 若评估逻辑把它们当作「已到期且无样本」处理, 首次运行就会把全站存量渠道打成 probation,
// 给它们的评分统一乘 0.70 —— 一次没有任何错误日志的静默线上回归。
// 存量渠道由迁移 021 一次性转正; 这里守的是运行期不得再动它们。
func TestChannelTrustEvaluateSkipsNoWindow(t *testing.T) {
	trustTestEnv(t)
	// 列默认 observing 但没有窗口的存量渠道。
	legacy := model.Channel{ChannelConfig: model.ChannelConfig{Name: "legacy-no-window"}}
	if err := db.GetDB().Create(&legacy).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	// 给它一堆失败流量: 若被纳入判定, 必然降级。
	daily := model.StatsChannelDaily{
		ChannelID:    legacy.ID,
		Date:         time.Now().AddDate(0, 0, -1).Format("20060102"),
		StatsMetrics: model.StatsMetrics{RequestFailed: 500},
	}
	if err := db.GetDB().Create(&daily).Error; err != nil {
		t.Fatalf("create stats: %v", err)
	}

	if err := ChannelTrustEvaluate(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got := loadTrust(t, legacy.ID); got.TrustLevel != model.ChannelTrustObserving {
		t.Fatalf("trust_level = %q, want observing (no window must be skipped, not demoted)", got.TrustLevel)
	}
}
