package op

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func catchupTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catchup.db")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return path
}

func catchupNow() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local) }
func catchupPeriods(t *testing.T) []string {
	t.Helper()
	var periods []string
	if err := db.GetDB().Model(&model.SettlementRun{}).Order("period").Pluck("period", &periods).Error; err != nil {
		t.Fatal(err)
	}
	return periods
}

func TestSettlementCatchupFailureRestartCrossDay(t *testing.T) {
	path := catchupTestDB(t)
	ctx := context.Background()
	now := catchupNow()
	failure := errors.New("generation unavailable")
	var calls []string
	err := settlementCatchup(ctx, now, func(_ context.Context, day string) error {
		calls = append(calls, day)
		if day == "20261026" || day == "20261028" {
			return failure
		}
		return nil
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "20261026") || !strings.Contains(err.Error(), "20261028") {
		t.Fatalf("joined errors = %v", err)
	}
	want := []string{"20261025", "20261026", "20261027", "20261028", "20261029", "20261030", "20261031"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("oldest-first bounded calls = %v", calls)
	}
	if got := catchupPeriods(t); !reflect.DeepEqual(got, []string{"20261025", "20261027", "20261029", "20261030", "20261031"}) {
		t.Fatalf("failed dates marked successful: %v", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	calls = nil
	// A real reopened database, on the next calendar day: persisted successes are skipped.
	err = settlementCatchup(ctx, now.AddDate(0, 0, 1), func(_ context.Context, day string) error { calls = append(calls, day); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"20261026", "20261028", "20261101"}) {
		t.Fatalf("restart/cross-day calls = %v", calls)
	}
	calls = nil
	if err := settlementCatchup(ctx, now.AddDate(0, 0, 1), func(_ context.Context, day string) error { calls = append(calls, day); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("successful periods refreshed: %v", calls)
	}
}

func TestSettlementCatchupAuditedFailureDoesNotBlockOtherDays(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "catchup-audited")
	t.Cleanup(func() { _ = db.Close() })
	now := catchupNow()
	day := now.AddDate(0, 0, -6)
	period := day.Format("20060102")
	settlementRecord(t, buyer.ID, owner.ID, day, 10, 8, 1)
	if err := SettlementGenerateDay(context.Background(), period); err != nil {
		t.Fatal(err)
	}
	stmt := settlementMustGet(t, owner.ID, period)
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("id = ?", stmt.ID).Update("status", "audited").Error; err != nil {
		t.Fatal(err)
	}
	settlementRecord(t, buyer.ID, owner.ID, day, 10, 8, 1)
	settlementRecord(t, buyer.ID, owner.ID, now.AddDate(0, 0, -1), 10, 8, 1)
	if err := SettlementCatchupRun(context.Background(), now); err == nil {
		t.Fatal("audited discrepancy must surface")
	}
	periods := catchupPeriods(t)
	if len(periods) != 6 {
		t.Fatalf("checkpoints=%v", periods)
	}
	for _, p := range periods {
		if p == period {
			t.Fatal("failed audited period marked success")
		}
	}
	got := settlementMustGet(t, owner.ID, period)
	if got.GrossRevenue != stmt.GrossRevenue || got.Status != "audited" {
		t.Fatalf("audited statement overwritten: %+v", got)
	}
	if got := settlementMustGet(t, owner.ID, "20261031"); got.GrossRevenue != 8 {
		t.Fatalf("later date not generated: %+v", got)
	}
}

func TestSettlementCatchupOnlySevenCompletedDays(t *testing.T) {
	catchupTestDB(t)
	// Midnight and month boundary must not include today, tomorrow, or the eighth prior day.
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	for _, period := range []string{"20261024", "20261101", "20261102"} {
		if err := db.GetDB().Create(&model.SettlementRun{Period: period, SucceededAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	if err := settlementCatchup(context.Background(), now, func(_ context.Context, day string) error { calls = append(calls, day); return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"20261025", "20261026", "20261027", "20261028", "20261029", "20261030", "20261031"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("window = %v", calls)
	}
}

func TestSettlementCatchupCheckpointFailureRetries(t *testing.T) {
	catchupTestDB(t)
	failure := errors.New("checkpoint unavailable")
	database := db.GetDB()
	if err := database.Callback().Create().Before("gorm:create").Register("test:checkpoint_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "settlement_runs" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Callback().Create().Remove("test:checkpoint_failure") })
	calls := 0
	generate := func(context.Context, string) error { calls++; return nil }
	if err := settlementCatchup(context.Background(), catchupNow(), generate); !errors.Is(err, failure) {
		t.Fatalf("checkpoint error = %v", err)
	}
	if calls != 7 || len(catchupPeriods(t)) != 0 {
		t.Fatalf("calls=%d; checkpoint failure must not mark success", calls)
	}
	database.Callback().Create().Remove("test:checkpoint_failure")
	if err := settlementCatchup(context.Background(), catchupNow(), generate); err != nil {
		t.Fatal(err)
	}
	if calls != 14 || len(catchupPeriods(t)) != 7 {
		t.Fatalf("retry calls=%d", calls)
	}
}

func TestSettlementCatchupMissingTableFailsClosed(t *testing.T) {
	catchupTestDB(t)
	if err := db.GetDB().Migrator().DropTable(&model.SettlementRun{}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := settlementCatchup(context.Background(), catchupNow(), func(context.Context, string) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatalf("fail closed: calls=%d err=%v", calls, err)
	}
	// Existing manual entry point must remain independent of the new table.
	if err := SettlementGenerateDay(context.Background(), "20261031"); err != nil {
		t.Fatalf("manual generation depends on checkpoint: %v", err)
	}
}

func TestSettlementCatchupRealGenerationIdempotentNoPayment(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "catchup")
	t.Cleanup(func() { _ = db.Close() })
	now := catchupNow()
	settlementRecord(t, buyer.ID, owner.ID, now.AddDate(0, 0, -1), 10, 8, 1)
	settlementRecord(t, buyer.ID, owner.ID, now.AddDate(0, 0, -8), 10, 8, 1)
	settlementRecord(t, buyer.ID, owner.ID, now, 10, 8, 1)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- SettlementCatchupRun(context.Background(), now) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(catchupPeriods(t)) != 7 {
		t.Fatal("empty days must have checkpoints")
	}
	var statements []model.SettlementStatement
	if err := db.GetDB().Find(&statements).Error; err != nil {
		t.Fatal(err)
	}
	if len(statements) != 1 || statements[0].Period != "20261031" || statements[0].GrossRevenue != 8 {
		t.Fatalf("statements=%+v", statements)
	}
	// Late source data does not automatically refresh a successfully checkpointed day.
	settlementRecord(t, buyer.ID, owner.ID, now.AddDate(0, 0, -1), 10, 8, 1)
	if err := SettlementCatchupRun(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var got model.SettlementStatement
	if err := db.GetDB().First(&got, statements[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.GrossRevenue != 8 || !got.UpdatedAt.Equal(statements[0].UpdatedAt) {
		t.Fatalf("successful statement refreshed: %+v", got)
	}
	var balance model.User
	if err := db.GetDB().First(&balance, owner.ID).Error; err != nil {
		t.Fatal(err)
	}
	if balance.Balance != owner.Balance {
		t.Fatalf("generation paid owner: %v", balance.Balance)
	}
}
