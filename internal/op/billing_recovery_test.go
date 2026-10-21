package op

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func TestBillingRecoveryDurableFailureAndReplay(t *testing.T) {
	payer, owner := billingTestSetup(t, "recovery-durable", 100)
	id := billingTestReserve(t, payer.ID, 7001, 10)
	record := billingTestRecord(payer, owner, 7001, 4, 2, 100)
	record.CreatedAt = time.Now().Add(-time.Hour)
	record.SupplyPrice.Input = 2
	callback := "test:billing_fail_record"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_records" {
			tx.AddError(errors.New("injected settlement write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Create().Remove(callback) })
	if err := BillingSettle(id, record); !errors.Is(err, ErrSettlementQueued) {
		t.Fatalf("want durable queued failure: %v", err)
	}
	var task model.BillingSettlementTask
	if err := db.GetDB().Where("reservation_id = ?", id).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	original := task.Snapshot
	if task.Status != model.BillingSettlementStatusRetry || task.Attempts != 1 {
		t.Fatalf("bad task %+v", task)
	}
	if err := BillingRelease(id); !errors.Is(err, ErrSettlementIntentPending) {
		t.Fatalf("pending released: %v", err)
	}
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", id).Update("lease_expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingReleaseStale(0); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Balance != 100 || state.Frozen != 10 {
		t.Fatalf("failed money state %+v", state)
	}
	if err := db.GetDB().Callback().Create().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&task).Update("next_attempt_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().First(&task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.BillingSettlementStatusCompleted || task.Snapshot != original {
		t.Fatalf("snapshot/status changed %+v", task)
	}
	var applied model.BillingRecord
	if err := db.GetDB().Where("request_id = ?", 7001).First(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied.SupplyPrice.Input != 2 || !applied.CreatedAt.Equal(record.CreatedAt) {
		t.Fatalf("price/time not replayed %+v", applied)
	}
	if state := billingTestState(t, payer.ID); state.Balance != 96 || state.Frozen != 0 || state.TotalSpent != 4 {
		t.Fatalf("replay money %+v", state)
	}
	if count := billingTestRecordCount(t, 7001); count != 1 {
		t.Fatalf("records=%d", count)
	}
}

func TestBillingLeaseExpiryAndLegacySweep(t *testing.T) {
	payer, _ := billingTestSetup(t, "lease-expiry", 100)
	id := billingTestReserve(t, payer.ID, 7002, 10)
	if err := BillingReleaseStale(0); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Frozen != 10 {
		t.Fatalf("live lease released %+v", state)
	}
	if err := BillingRenewLease(id, time.Minute); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", id).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := BillingRenewLease(id, time.Minute); err == nil {
		t.Fatal("expired lease resurrected")
	}
	if err := BillingReleaseStale(0); err != nil {
		t.Fatal(err)
	}
	if state := billingTestState(t, payer.ID); state.Frozen != 0 {
		t.Fatalf("expired lease retained %+v", state)
	}
}

func TestBillingRecoveryManualHoldAndConflictDoNotOverwrite(t *testing.T) {
	payer, owner := billingTestSetup(t, "manual-recovery", 100)
	id := billingTestReserve(t, payer.ID, 7010, 10)
	record := billingTestRecord(payer, owner, 7010, 4, 2, 100)
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	task := model.BillingSettlementTask{ReservationID: id, RequestID: 7010, UserID: payer.ID, Status: model.BillingSettlementStatusManual, Snapshot: string(body), Attempts: 8, NextAttemptAt: time.Now().Add(-time.Hour)}
	if err := db.GetDB().Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	conflict := billingTestRecord(payer, owner, 7010, 8, 2, 100)
	if err := BillingSettle(id, conflict); !errors.Is(err, ErrSettlementMismatch) {
		t.Fatalf("conflict accepted: %v", err)
	}
	if err := BillingRecoveryRun(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if err := BillingRelease(id); !errors.Is(err, ErrSettlementIntentPending) {
		t.Fatalf("manual hold released: %v", err)
	}
	var after model.BillingSettlementTask
	if err := db.GetDB().First(&after, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Snapshot != string(body) || after.Status != model.BillingSettlementStatusManual || after.Attempts != 8 {
		t.Fatalf("manual task changed %+v", after)
	}
	if state := billingTestState(t, payer.ID); state.Balance != 100 || state.Frozen != 10 {
		t.Fatalf("manual hold money changed %+v", state)
	}
}

func TestBillingRecoveryRejectsNonFinite(t *testing.T) {
	payer, owner := billingTestSetup(t, "invalid-recovery", 100)
	id := billingTestReserve(t, payer.ID, 7003, 10)
	record := billingTestRecord(payer, owner, 7003, math.NaN(), 2, 100)
	if err := BillingSettle(id, record); err == nil {
		t.Fatal("NaN accepted")
	}
	var count int64
	if err := db.GetDB().Model(&model.BillingSettlementTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid intent persisted")
	}
}
