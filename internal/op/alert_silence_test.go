package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// silenceTestDB 与 raiseTestDB 相同的临时库初始化: 静默判定读 db.GetDB(), 必须走 InitDB。
func silenceTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "silence.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// captureSilenceDispatcher 静默测试要把外发接到测试通道, 与告警测试共用机制。
func captureSilenceDispatcher(t *testing.T) chan alert.Alert {
	t.Helper()
	original := AlertDispatcher
	out := make(chan alert.Alert, 64)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		out <- a
		return true
	}
	t.Cleanup(func() {
		alertDispatchWait()
		AlertDispatcher = original
	})
	return out
}

// 静默窗口核心语义: 窗口内告警照常落库但不外发; 窗口外(或类型不匹配)照常外发。
// 这是"不打扰但可回看"承诺的测试面 —— 落库发生与否、外发发生与否, 各有断言。
func TestAlertSilenceSuppressesOutboundOnly(t *testing.T) {
	silenceTestDB(t)
	out := captureSilenceDispatcher(t)
	ctx := context.Background()

	now := time.Now()
	// 只静默 backup_failed: 其他类型的告警不受影响。
	if _, err := AlertSilenceCreate(ctx, model.AlertSilenceWindowInput{
		StartAt: now.Add(-time.Minute).Format(time.RFC3339),
		EndAt:   now.Add(time.Hour).Format(time.RFC3339),
		Kind:    "backup_failed",
		Note:    "维护",
	}, 0); err != nil {
		t.Fatalf("create silence: %v", err)
	}

	// 窗口内且类型匹配: 落库但不外发。
	if !RaiseAlertFacts(ctx, AlertFacts{Kind: "backup_failed", Reason: "disk full"}) {
		t.Fatal("alert should still be recorded (return true)")
	}
	// 窗口内但类型不匹配: 照常外发。
	if !RaiseAlertFacts(ctx, AlertFacts{Kind: "down", Reason: "channel error"}) {
		t.Fatal("unrelated kind should be raised normally")
	}

	// 去重窗口规避: 用不同的 reason, 确保是"静默"而非"去重"挡住了第二条。
	if !RaiseAlertFacts(ctx, AlertFacts{Kind: "backup_failed", Reason: "archive error"}) {
		t.Fatal("second silenced alert should also be recorded")
	}

	deadline := time.Now().Add(time.Second)
	sent := 0
	for time.Now().Before(deadline) {
		select {
		case <-out:
			sent++
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if sent != 1 {
		t.Fatalf("outbound count = %d, want exactly 1 (only unrelated kind)", sent)
	}

	// 落库计数: backup_failed 两条 + down 一条 = 3。
	var count int64
	if err := db.GetDB().Model(&model.ChannelAlert{}).Count(&count).Error; err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if count != 3 {
		t.Fatalf("recorded alerts = %d, want 3", count)
	}
}

// 窗口过期后外发恢复: end 取开区间, 到点即不再静默。
func TestAlertSilenceExpires(t *testing.T) {
	silenceTestDB(t)
	out := captureSilenceDispatcher(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := AlertSilenceCreate(ctx, model.AlertSilenceWindowInput{
		StartAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		EndAt:   now.Add(-time.Hour).Format(time.RFC3339),
		Note:    "已过期窗口",
	}, 0); err != nil {
		t.Fatalf("create expired silence: %v", err)
	}
	if AlertSilenced(ctx, "backup_failed") {
		t.Fatal("expired window must not silence")
	}
	if !RaiseAlertFacts(ctx, AlertFacts{Kind: "backup_failed", Reason: "post window"}) {
		t.Fatal("raise after window should succeed")
	}
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("alert after window expiry should be delivered")
	}
}

// 创建校验: 结束早于开始、超过时长上限、未知类型都要拦下。
func TestAlertSilenceCreateValidation(t *testing.T) {
	silenceTestDB(t)
	ctx := context.Background()
	now := time.Now()
	cases := []struct {
		name  string
		input model.AlertSilenceWindowInput
	}{
		{"end before start", model.AlertSilenceWindowInput{
			StartAt: now.Format(time.RFC3339), EndAt: now.Add(-time.Minute).Format(time.RFC3339)}},
		{"too long", model.AlertSilenceWindowInput{
			StartAt: now.Format(time.RFC3339), EndAt: now.Add(73 * time.Hour).Format(time.RFC3339)}},
		{"bad kind", model.AlertSilenceWindowInput{
			StartAt: now.Format(time.RFC3339), EndAt: now.Add(time.Hour).Format(time.RFC3339), Kind: "not_a_kind"}},
		{"bad start", model.AlertSilenceWindowInput{
			StartAt: "yesterday", EndAt: now.Add(time.Hour).Format(time.RFC3339)}},
	}
	for _, tc := range cases {
		_, err := AlertSilenceCreate(ctx, tc.input, 0)
		if err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}
	// 空类型(静默全部)与合法类型必须能建。
	if _, err := AlertSilenceCreate(ctx, model.AlertSilenceWindowInput{
		StartAt: now.Format(time.RFC3339), EndAt: now.Add(time.Hour).Format(time.RFC3339),
		Note: "deploy"}, 0); err != nil {
		t.Fatalf("valid all-kind window rejected: %v", err)
	}
}

// kind 闭集与 determineAlertEvent 的 case 清单一致: 新 kind 加进映射时必须同时加进静默校验。
// 这条测试钉住两处清单, 防止静默窗口对新增告警类型悄悄失效(创建时被拒)。
func TestAlertValidKindCoversDeterminableKinds(t *testing.T) {
	for _, kind := range []string{
		"down", "recovered", "degraded", "key_disabled", "model_disabled",
		"low_balance", "quota_exceeded", "mismatch", "billing_failed",
		"backup_failed", "archive_failed", "watchdog_alert",
		"route_persist_failed", "route_persist_recovered",
		"task_failed", "task_recovered",
	} {
		if !AlertValidKind(kind) {
			t.Errorf("kind %q is a known alert kind but rejected by AlertValidKind", kind)
		}
	}
	for _, kind := range []string{"", "not_a_kind", "Down", "down "} {
		if AlertValidKind(kind) {
			t.Errorf("kind %q should not pass the closed set", kind)
		}
	}
}
