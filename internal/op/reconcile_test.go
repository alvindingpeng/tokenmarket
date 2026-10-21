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
