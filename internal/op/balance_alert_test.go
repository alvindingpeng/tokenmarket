package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func balanceAlertSetup(t *testing.T) {
	t.Helper()
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
