package op

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestReconcileDoesNotFlagLiveLongRequestLease(t *testing.T) {
	payer, _ := billingTestSetup(t, "reconcile-lease", 100)
	reservationID := billingTestReserve(t, payer.ID, 900, 10)
	old := time.Now().Add(-time.Hour)
	future := time.Now().Add(10 * time.Minute)
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", reservationID).Updates(map[string]any{"created_at": old, "lease_expires_at": future}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := ReconcileDay(context.Background(), time.Now())
	if err != nil || result.StaleHold != 0 {
		t.Fatalf("live lease reported stale: %+v, %v", result, err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", reservationID).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	result, err = ReconcileDay(context.Background(), time.Now())
	if err != nil || result.StaleHold != 1 {
		t.Fatalf("expired lease not reported: %+v, %v", result, err)
	}
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", reservationID).Update("lease_expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	result, err = ReconcileDay(context.Background(), time.Now())
	if err != nil || result.StaleHold != 1 {
		t.Fatalf("legacy stale hold not reported: %+v, %v", result, err)
	}
}

func TestReconcilePreservesMissingEvidenceAfterLogRetention(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "evidence.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	previous := model.BillingReconcile{Day: "20261008", Missing: 321, Records: 147, Status: model.ReconcileStatusMismatch, Detail: "original evidence", CreatedAt: day, UpdatedAt: day}
	if err := db.GetDB().Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	var before model.BillingReconcile
	if err := db.GetDB().First(&before, previous.ID).Error; err != nil {
		t.Fatal(err)
	}
	got, err := ReconcileDay(context.Background(), day)
	if err == nil || !strings.Contains(err.Error(), "no retained request logs") {
		t.Fatalf("expected evidence coverage refusal, got %+v, %v", got, err)
	}
	var after model.BillingReconcile
	if err := db.GetDB().First(&after, previous.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("original exception changed: before=%+v after=%+v", before, after)
	}
}

func TestReconcileCanResolveMissingWithRetainedEvidence(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "resolved.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	previous := model.BillingReconcile{Day: "20261008", Missing: 1, Status: model.ReconcileStatusMismatch}
	if err := db.GetDB().Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.RelayRequest{ID: 1, StartedAt: day, PromptTokens: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.BillingRecord{RequestID: 1, CreatedAt: day, UserCost: 2, OwnerRevenue: 1, PlatformRevenue: 1}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := ReconcileDay(context.Background(), day)
	if err != nil || got.Missing != 0 || got.Status != model.ReconcileStatusBalanced {
		t.Fatalf("unexpected resolution %+v, %v", got, err)
	}
}

// 差异处置说明: 只改说明与状态, 数字一个都不动, 且重算不会把它抹掉。
func TestReconcileNoteRecordsWithoutTouchingFigures(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "note.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	original := model.BillingReconcile{
		Day: "20261008", Records: 147, UserCost: 0.640021305, OwnerRevenue: 0.6095441,
		PlatformRev: 0.030477205, Missing: 321, Status: model.ReconcileStatusMismatch,
		Detail: "original evidence", CreatedAt: day, UpdatedAt: day,
	}
	if err := db.GetDB().Create(&original).Error; err != nil {
		t.Fatal(err)
	}

	got, err := ReconcileNoteSet(context.Background(), "20261008", "全量计费修复前的 admin 免计费流量", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.ReconcileStatusExplained || got.Note == "" || got.NotedAt == nil || got.NotedBy != 7 {
		t.Fatalf("note not recorded: %+v", got)
	}
	// 数字与差异项必须与写说明前一致: 处置说明不是改数的后门。
	after := model.BillingReconcile{}
	if err := db.GetDB().Where("day = ?", "20261008").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Missing != original.Missing || after.Records != original.Records ||
		after.UserCost != original.UserCost || after.OwnerRevenue != original.OwnerRevenue ||
		after.PlatformRev != original.PlatformRev || after.Detail != original.Detail {
		t.Fatalf("figures changed by noting: before=%+v after=%+v", original, after)
	}

	// 重算(带保留证据, 此处差异已消失)不得覆盖已登记的说明。
	if err := db.GetDB().Create(&model.RelayRequest{ID: 1, StartedAt: day, PromptTokens: 1}).Error; err != nil {
		t.Fatal(err)
	}
	recomputed, err := ReconcileDay(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if recomputed.Note == "" || recomputed.NotedBy != 7 {
		t.Fatalf("recompute dropped the recorded note: %+v", recomputed)
	}
}

// 差异真的变了就落回对账自己算出的状态: 说明描述的是当时的差异, 不是永久豁免。
func TestReconcileNoteDropsWhenFiguresChange(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "note-change.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	if err := db.GetDB().Create(&model.BillingReconcile{
		Day: "20261008", Missing: 1, Status: model.ReconcileStatusMismatch, CreatedAt: day, UpdatedAt: day,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileNoteSet(context.Background(), "20261008", "已核对", 1); err != nil {
		t.Fatal(err)
	}
	// 留下证据并补上一条计费, 差异归零 —— 与说明里描述的那一天已经不同。
	if err := db.GetDB().Create(&model.RelayRequest{ID: 1, StartedAt: day, PromptTokens: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.BillingRecord{RequestID: 1, CreatedAt: day, UserCost: 2, OwnerRevenue: 1, PlatformRevenue: 1}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := ReconcileDay(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.ReconcileStatusBalanced {
		t.Fatalf("status = %q, want balanced after the difference disappeared", got.Status)
	}
}

// 撤销说明把状态交回对账自己算; 没有差异的日子不许挂说明。
func TestReconcileNoteClearAndGuard(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "note-clear.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	if err := db.GetDB().Create(&model.BillingReconcile{
		Day: "20261008", Missing: 5, Status: model.ReconcileStatusMismatch, CreatedAt: day, UpdatedAt: day,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileNoteSet(context.Background(), "20261008", "查过了", 1); err != nil {
		t.Fatal(err)
	}
	got, err := ReconcileNoteSet(context.Background(), "20261008", "   ", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "" || got.NotedAt != nil || got.NotedBy != 0 || got.Status != model.ReconcileStatusMismatch {
		t.Fatalf("note not cleared: %+v", got)
	}

	if err := db.GetDB().Create(&model.BillingReconcile{
		Day: "20261009", Status: model.ReconcileStatusBalanced, CreatedAt: day, UpdatedAt: day,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileNoteSet(context.Background(), "20261009", "无差异也要写", 1); err == nil {
		t.Fatal("expected refusal to note a balanced day")
	}
	if _, err := ReconcileNoteSet(context.Background(), "20261010", "不存在的一天", 1); err == nil {
		t.Fatal("expected refusal for a missing day")
	}
}

func TestReconcileDayUsesCalendarDayAcrossDST(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	original := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = original })
	for _, day := range []time.Time{time.Date(2026, 3, 8, 12, 0, 0, 0, zone), time.Date(2026, 11, 1, 12, 0, 0, 0, zone)} {
		t.Run(day.Format("20060102"), func(t *testing.T) {
			if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "reconcile.db"), false); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
			end := start.AddDate(0, 0, 1)
			rows := []model.BillingRecord{
				{RequestID: 1, UserCost: 2, OwnerRevenue: 1, PlatformRevenue: 1, CreatedAt: start},
				{RequestID: 2, UserCost: 2, OwnerRevenue: 1, PlatformRevenue: 1, CreatedAt: end.Add(-time.Minute)},
				{RequestID: 3, UserCost: 100, OwnerRevenue: 50, PlatformRevenue: 50, CreatedAt: end},
			}
			if err := db.GetDB().Create(&rows).Error; err != nil {
				t.Fatal(err)
			}
			got, err := ReconcileDay(context.Background(), day)
			if err != nil {
				t.Fatal(err)
			}
			if got.Records != 2 || got.UserCost != 4 || got.OwnerRevenue != 2 || got.Status != model.ReconcileStatusBalanced {
				t.Fatalf("unexpected day result: %+v", got)
			}
		})
	}
}
