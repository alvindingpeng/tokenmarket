package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func balanceAlertSetup(t *testing.T) {
	t.Helper()
	// 换库前清空进程级缓存, 否则 -shuffle 下会读到上一个用例的旧库数据。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "balance_alert.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestBalanceAlertDefaultDisabled(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()

	view, err := BalanceAlertGet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled || view.ThresholdCNY != 0 {
		t.Fatalf("default view should be disabled with zero threshold: %+v", view)
	}
}

func TestBalanceAlertSetAndGet(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()

	enabled := true
	view, err := BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 10, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || view.ThresholdCNY != 10 {
		t.Fatalf("unexpected view: %+v", view)
	}

	// 再次保存是覆盖语义, 不是追加。
	view, err = BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 5})
	if err != nil {
		t.Fatal(err)
	}
	if view.ThresholdCNY != 5 || !view.Enabled {
		t.Fatalf("upsert should overwrite: %+v", view)
	}

	// 阈值为 0 归一成禁用。
	view, err = BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 0})
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled {
		t.Fatal("zero threshold should normalize to disabled")
	}
}

func TestBalanceAlertDelete(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()

	if _, err := BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 10}); err != nil {
		t.Fatal(err)
	}
	if err := BalanceAlertDelete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	view, err := BalanceAlertGet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled || view.ThresholdCNY != 0 {
		t.Fatalf("after delete should return default view: %+v", view)
	}
	// 重复删除不报错。
	if err := BalanceAlertDelete(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

// 核心行为: 低于阈值触发一次, 窗口内不重复, 恢复后再跌破才再次触发。
func TestBalanceAlertCheckTriggerAndDedup(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()
	enabled := true
	if _, err := BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 10, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}

	// 高于阈值: 不触发。
	triggered, err := BalanceAlertCheck(ctx, 1, 15)
	if err != nil || triggered {
		t.Fatalf("above threshold should not trigger: triggered=%v err=%v", triggered, err)
	}

	// 跌破阈值: 触发。
	triggered, err = BalanceAlertCheck(ctx, 1, 5)
	if err != nil || !triggered {
		t.Fatalf("below threshold should trigger: triggered=%v err=%v", triggered, err)
	}

	// 窗口内再跌破: 去重, 不触发。
	triggered, err = BalanceAlertCheck(ctx, 1, 3)
	if err != nil || triggered {
		t.Fatalf("within dedup window should not trigger again: triggered=%v err=%v", triggered, err)
	}

	// 禁用后: 不触发。
	disabled := false
	if _, err := BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 10, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	triggered, err = BalanceAlertCheck(ctx, 1, 1)
	if err != nil || triggered {
		t.Fatalf("disabled alert should not trigger: triggered=%v err=%v", triggered, err)
	}
}

// 用户隔离: A 的配置不能影响 B。
func TestBalanceAlertHistoryOnlyRecordsDedupWinner(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()
	enabled := true
	if _, err := BalanceAlertSet(ctx, 31, model.BalanceAlertUpsert{ThresholdCNY: 10, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	triggered, err := BalanceAlertCheck(ctx, 31, 5)
	if err != nil || !triggered {
		t.Fatalf("first trigger: %v %v", triggered, err)
	}
	triggered, err = BalanceAlertCheck(ctx, 31, 4)
	if err != nil || triggered {
		t.Fatalf("deduped trigger: %v %v", triggered, err)
	}
	rows, err := BalanceAlertHistoryList(ctx, 31)
	if err != nil || len(rows) != 1 {
		t.Fatalf("history should contain one dedup winner: len=%d err=%v", len(rows), err)
	}
	if rows[0].UserID != 31 || rows[0].Source != "balance_alert" || rows[0].TriggeredAt.IsZero() {
		t.Fatalf("bad history row: %+v", rows[0])
	}
}

func TestBalanceAlertHistoryScopedAndLimitedToNewestTen(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		at := time.Date(2026, 1, i, 0, 0, 0, 0, time.UTC)
		row := model.BalanceAlertHistory{UserID: 41, TriggeredAt: at, Source: "balance_alert"}
		if err := db.GetDB().Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	other := model.BalanceAlertHistory{UserID: 42, TriggeredAt: time.Now(), Source: "balance_alert"}
	if err := db.GetDB().Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := BalanceAlertHistoryList(ctx, 41)
	if err != nil || len(rows) != 10 {
		t.Fatalf("expected newest 10, got %d err=%v", len(rows), err)
	}
	if rows[0].TriggeredAt.Day() != 12 || rows[9].TriggeredAt.Day() != 3 {
		t.Fatalf("wrong order/window: first=%v last=%v", rows[0].TriggeredAt, rows[9].TriggeredAt)
	}
	for _, row := range rows {
		if row.UserID != 41 {
			t.Fatalf("cross-user row: %+v", row)
		}
	}
}

func TestBalanceAlertHistoryStorageIsBoundedToTen(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()
	enabled := true
	if _, err := BalanceAlertSet(ctx, 51, model.BalanceAlertUpsert{ThresholdCNY: 10, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		// 让每次跨过一小时去重窗, 仍不睡眠、不改全局时钟。
		at := time.Date(2026, 2, 1, 0, i, 0, 0, time.UTC)
		if err := db.GetDB().Model(&model.BalanceAlert{}).Where("user_id = ?", 51).Update("last_triggered_at", at.Add(-2*time.Hour)).Error; err != nil {
			t.Fatal(err)
		}
		triggered, err := BalanceAlertCheck(ctx, 51, 5)
		if err != nil || !triggered {
			t.Fatalf("trigger %d: %v %v", i, triggered, err)
		}
	}
	var persisted int64
	if err := db.GetDB().Model(&model.BalanceAlertHistory{}).Where("user_id = ?", 51).Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != 10 {
		t.Fatalf("history storage=%d, want bounded 10", persisted)
	}
	rows, err := BalanceAlertHistoryList(ctx, 51)
	if err != nil || len(rows) != 10 {
		t.Fatalf("list len=%d err=%v", len(rows), err)
	}
}

func TestBalanceAlertCheckIsScopedToUser(t *testing.T) {
	balanceAlertSetup(t)
	ctx := context.Background()
	enabled := true
	if _, err := BalanceAlertSet(ctx, 1, model.BalanceAlertUpsert{ThresholdCNY: 100, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}

	// 用户 2 没有配置: 余额再低也不触发。
	triggered, err := BalanceAlertCheck(ctx, 2, 0.01)
	if err != nil || triggered {
		t.Fatalf("user without config should not trigger: triggered=%v err=%v", triggered, err)
	}
}
