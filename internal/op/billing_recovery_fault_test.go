package op

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func recoveryFixture(t *testing.T, request uint64) (model.User, model.BillingSettlementTask) {
	t.Helper()
	payer, owner := billingTestSetup(t, "recovery-fault", 100)
	id := billingTestReserve(t, payer.ID, request, 10)
	r := billingTestRecord(payer, owner, request, 4, 2, 100)
	r.CreatedAt = time.Now().Add(-time.Hour)
	r.Status = model.BillingStatusSettled
	r.PlatformRevenue = 2
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	task := model.BillingSettlementTask{ReservationID: id, RequestID: request, UserID: payer.ID, Status: model.BillingSettlementStatusPending, Snapshot: string(body), NextAttemptAt: time.Now().Add(-time.Second)}
	if err := db.GetDB().Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return payer, task
}

func TestRecoveryReopensDatabaseAndReplaysOnce(t *testing.T) {
	payer, task := recoveryFixture(t, 8001)
	var database []struct {
		Name string
		File string
	}
	if err := db.GetDB().Raw("PRAGMA database_list").Scan(&database).Error; err != nil {
		t.Fatal(err)
	}
	var path string
	for _, d := range database {
		if d.Name == "main" {
			path = d.File
		}
	}
	if path == "" || !filepath.IsAbs(path) {
		t.Fatalf("missing database path %+v", database)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	userCache.Clear()
	userNameIndex.Clear()
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if got := billingTestState(t, payer.ID); got.Balance != 96 || got.Frozen != 0 {
		t.Fatalf("restart money %+v", got)
	}
	if billingTestRecordCount(t, task.RequestID) != 1 {
		t.Fatal("duplicate restart billing")
	}
}

func TestRecoveryFinalAttemptBookkeepingOnly(t *testing.T) {
	payer, task := recoveryFixture(t, 8030)
	var record model.BillingRecord
	if err := json.Unmarshal([]byte(task.Snapshot), &record); err != nil {
		t.Fatal(err)
	}
	if err := billingSettleApply(context.Background(), task.ReservationID, &record); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&task).Updates(map[string]any{"attempts": BillingSettlementMaxAttempts, "status": model.BillingSettlementStatusRetry}).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().First(&task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.BillingSettlementStatusCompleted || task.Attempts != BillingSettlementMaxAttempts {
		t.Fatalf("last attempt bookkeeping %+v", task)
	}
	if state := billingTestState(t, payer.ID); state.Balance != 96 || state.TotalSpent != 4 || state.Frozen != 0 {
		t.Fatalf("last attempt double charge %+v", state)
	}
	if billingTestRecordCount(t, 8030) != 1 {
		t.Fatal("last attempt duplicate")
	}
}

func TestLeaseSweepRechecksRenewedLegacyCandidate(t *testing.T) {
	payer, _ := billingTestSetup(t, "lease-recheck", 100)
	id := billingTestReserve(t, payer.ID, 8031, 10)
	cutoff := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", id).Updates(map[string]any{"lease_expires_at": nil, "created_at": cutoff.Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	// A sweeper has selected the legacy row; a heartbeat renews before terminal UPDATE.
	if err := BillingRenewLease(id, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := billingRelease(id, &cutoff); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Frozen != 10 {
		t.Fatalf("renewed candidate released %+v", state)
	}
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", id).Update("lease_expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingReleaseStale(time.Minute); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Frozen != 0 {
		t.Fatalf("legacy expiry ignored %+v", state)
	}
}

func TestRecoveryCompletedMoneyButTaskUpdateFails(t *testing.T) {
	payer, task := recoveryFixture(t, 8002)
	callback := "test:recovery_complete_failure"
	if err := db.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_settlement_tasks" {
			if values, ok := tx.Statement.Dest.(map[string]interface{}); ok && values["status"] == model.BillingSettlementStatusCompleted {
				tx.AddError(errors.New("injected completion update failure"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Update().Remove(callback) })
	if err := BillingRecoveryRun(context.Background(), 50); err == nil {
		t.Fatal("expected bookkeeping failure")
	}
	if got := billingTestState(t, payer.ID); got.Balance != 96 || got.Frozen != 0 {
		t.Fatalf("money transaction missing %+v", got)
	}
	_ = db.GetDB().Callback().Update().Remove(callback)
	if err := db.GetDB().Model(&task).Update("next_attempt_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if got := billingTestState(t, payer.ID); got.Balance != 96 || got.TotalSpent != 4 {
		t.Fatalf("double debit %+v", got)
	}
	if billingTestRecordCount(t, task.RequestID) != 1 {
		t.Fatal("double bill")
	}
}

func TestRecoveryEnqueueFailureDoesNotPromiseDurability(t *testing.T) {
	payer, owner := billingTestSetup(t, "enqueue-failure", 100)
	id := billingTestReserve(t, payer.ID, 8010, 10)
	callback := "test:reject_intent"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_settlement_tasks" {
			tx.AddError(errors.New("intent unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Create().Remove(callback) })
	err := BillingSettle(id, billingTestRecord(payer, owner, 8010, 4, 2, 100))
	if err == nil || errors.Is(err, ErrSettlementQueued) {
		t.Fatalf("false durability %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.BillingSettlementTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || billingTestRecordCount(t, 8010) != 0 {
		t.Fatal("failed enqueue persisted money or intent")
	}
	if state := billingTestState(t, payer.ID); state.Frozen != 10 || state.Balance != 100 {
		t.Fatalf("enqueue moved money %+v", state)
	}
}

func TestRecoveryBatchLimitAndConcurrentWorkers(t *testing.T) {
	payer, task := recoveryFixture(t, 8020)
	var original model.BillingRecord
	if err := json.Unmarshal([]byte(task.Snapshot), &original); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		r := original
		r.RequestID += uint64(i)
		id := billingTestReserve(t, payer.ID, r.RequestID, 10)
		body, _ := json.Marshal(r)
		added := model.BillingSettlementTask{ReservationID: id, RequestID: r.RequestID, UserID: payer.ID, Status: model.BillingSettlementStatusPending, Snapshot: string(body), NextAttemptAt: time.Now().Add(-time.Second)}
		if err := db.GetDB().Create(&added).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := BillingRecoveryRun(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var completed int64
	if err := db.GetDB().Model(&model.BillingSettlementTask{}).Where("status = ?", model.BillingSettlementStatusCompleted).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("batch limit ignored: %d", completed)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = BillingRecoveryRun(context.Background(), 50) }()
	}
	wg.Wait()
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Balance != 88 || state.Frozen != 0 || state.TotalSpent != 12 {
		t.Fatalf("concurrent recovery money %+v", state)
	}
	for i := uint64(0); i < 3; i++ {
		if billingTestRecordCount(t, 8020+i) != 1 {
			t.Fatal("duplicate worker billing")
		}
	}
}

func TestRecoveryDueBatchBoundAndCanceledContext(t *testing.T) {
	_, task := recoveryFixture(t, 8011)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := BillingRecoveryRun(ctx, 1); err == nil {
		t.Fatal("canceled context ignored")
	}
	if err := db.GetDB().First(&task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Attempts != 0 {
		t.Fatal("canceled worker claimed task")
	}
	if err := db.GetDB().Model(&task).Update("next_attempt_at", time.Now().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if billingTestRecordCount(t, task.RequestID) != 0 {
		t.Fatal("future retry executed")
	}
}

func TestRecoveryCorruptionAndExhaustionHoldFunds(t *testing.T) {
	for _, mode := range []string{"malformed", "negative", "media-price", "identity", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			payer, task := recoveryFixture(t, 8003)
			switch mode {
			case "malformed":
				task.Snapshot = "not json"
			case "negative":
				var r model.BillingRecord
				_ = json.Unmarshal([]byte(task.Snapshot), &r)
				r.UserCost = -1
				body, _ := json.Marshal(r)
				task.Snapshot = string(body)
			case "media-price", "identity":
				var r model.BillingRecord
				_ = json.Unmarshal([]byte(task.Snapshot), &r)
				if mode == "media-price" {
					r.MediaSupply.PerSecond = -1
				} else {
					r.RequestID++
				}
				body, _ := json.Marshal(r)
				task.Snapshot = string(body)
			case "exhausted":
				task.Attempts = BillingSettlementMaxAttempts
			}
			if err := db.GetDB().Save(&task).Error; err != nil {
				t.Fatal(err)
			}
			_ = BillingRecoveryRun(context.Background(), 50)
			if err := db.GetDB().First(&task, task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != model.BillingSettlementStatusManual {
				t.Fatalf("not manual %+v", task)
			}
			if err := BillingRelease(task.ReservationID); !errors.Is(err, ErrSettlementIntentPending) {
				t.Fatalf("released manual %v", err)
			}
			if got := billingTestState(t, payer.ID); got.Balance != 100 || got.Frozen != 10 {
				t.Fatalf("invalid money mutation %+v", got)
			}
		})
	}
}
