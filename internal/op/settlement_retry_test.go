package op

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	sqlite "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"
)

func TestSettlementRetryPolicy(t *testing.T) {
	busy := fmt.Errorf("write: %w", errors.New("database is locked (5) (SQLITE_BUSY)"))
	for _, tc := range []struct {
		name, dialect string
		err           error
		attempts      int
	}{
		{"busy", "sqlite", busy, 5},
		{"locked", "sqlite", fmt.Errorf("outer: %w", errors.New("database table is locked: records")), 5},
		{"postgres", "postgres", busy, 1},
		{"mysql", "mysql", busy, 1},
		{"semantic", "sqlite", errors.New("statement database is locked has immutable status"), 1},
		{"constraint", "sqlite", errors.New("constraint failed: UNIQUE constraint failed"), 1},
		{"typed-code-wins-over-text", "sqlite", fmt.Errorf("database is locked: %w", &sqlite.Error{}), 1},
		{"data", "sqlite", errors.New("approved refunds reference missing billing records"), 1},
		{"cancel", "sqlite", fmt.Errorf("database is locked: %w", context.Canceled), 1},
		{"deadline", "sqlite", fmt.Errorf("database is locked: %w", context.DeadlineExceeded), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var delays []time.Duration
			got := settlementRetry(context.Background(), tc.dialect, func() error { calls++; return tc.err }, func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil })
			if got != tc.err || calls != tc.attempts || len(delays) != tc.attempts-1 {
				t.Fatalf("err=%v calls=%d delays=%v", got, calls, delays)
			}
			if tc.attempts == 5 && !reflect.DeepEqual(delays, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}) {
				t.Fatalf("delays=%v", delays)
			}
		})
	}
	calls := 0
	err := settlementRetry(context.Background(), "sqlite", func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil || calls != 3 {
		t.Fatalf("success: calls=%d err=%v", calls, err)
	}
}

func TestSettlementRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	go func() {
		done <- settlementRetry(ctx, "sqlite", func() error { calls++; return errors.New("database is locked") }, func(ctx context.Context, _ time.Duration) error {
			close(entered)
			return settlementRetryWait(ctx, time.Hour)
		})
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt backoff")
	}
	calls = 0
	if err := settlementRetry(ctx, "sqlite", func() error { calls++; return nil }, settlementRetryWait); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("pre-canceled: calls=%d err=%v", calls, err)
	}
}

type settlementRetryContextKey struct{}

// The generator has read its WAL snapshot before the callback signals. Another
// connection commits before it is allowed to write: this deterministically forces
// SQLITE_BUSY_SNAPSHOT, rather than relying on scheduler timing or sleeps.
func TestSettlementRetryWALSnapshot(t *testing.T) {
	for _, audit := range []bool{false, true} {
		t.Run(fmt.Sprintf("audit=%v", audit), func(t *testing.T) {
			buyer, owner, _ := settlementTestDB(t, "retry")
			database := db.GetDB()
			pool, err := database.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.Close() })
			pool.SetMaxOpenConns(4)
			var mode string
			if err := database.Raw("PRAGMA journal_mode=WAL").Scan(&mode).Error; err != nil || mode != "wal" {
				t.Fatalf("WAL: %s %v", mode, err)
			}
			start, _, err := settlementDayWindow("20260920")
			if err != nil {
				t.Fatal(err)
			}
			record := settlementRecord(t, buyer.ID, owner.ID, start.Add(time.Hour), 10, 8, 0)
			if audit {
				if err := SettlementGenerateDay(context.Background(), "20260920"); err != nil {
					t.Fatal(err)
				}
			}
			ready := make(chan struct{})
			resume := make(chan struct{})
			var writes atomic.Int32
			var conflicts atomic.Int32
			before := func(tx *gorm.DB) {
				if tx.Statement.Context.Value(settlementRetryContextKey{}) != true || tx.Statement.Table != "settlement_statements" {
					return
				}
				if writes.Add(1) == 1 {
					close(ready)
					select {
					case <-resume:
					case <-tx.Statement.Context.Done():
						tx.AddError(tx.Statement.Context.Err())
					}
				}
			}
			after := func(tx *gorm.DB) {
				if tx.Statement.Context.Value(settlementRetryContextKey{}) != true || tx.Error == nil {
					return
				}
				var e *sqlite.Error
				if errors.As(tx.Error, &e) && e.Code() == 517 {
					conflicts.Add(1)
				}
			}
			if err := database.Callback().Create().Before("gorm:create").Register("retry_snapshot_before", before); err != nil {
				t.Fatal(err)
			}
			if err := database.Callback().Create().After("gorm:create").Register("retry_snapshot_after", after); err != nil {
				t.Fatal(err)
			}
			if err := database.Callback().Update().Before("gorm:update").Register("retry_snapshot_before", before); err != nil {
				t.Fatal(err)
			}
			if err := database.Callback().Update().After("gorm:update").Register("retry_snapshot_after", after); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				database.Callback().Create().Remove("retry_snapshot_before")
				database.Callback().Create().Remove("retry_snapshot_after")
				database.Callback().Update().Remove("retry_snapshot_before")
				database.Callback().Update().Remove("retry_snapshot_after")
			})
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), settlementRetryContextKey{}, true), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- SettlementGenerateDay(ctx, "20260920") }()
			select {
			case <-ready:
			case err := <-result:
				t.Fatalf("generator returned before synchronization: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Commit fresh source data and a competing generation on the second connection.
			writerErr := database.Model(&model.BillingRecord{}).Where("id = ?", record.ID).Update("owner_revenue", 9).Error
			if writerErr == nil {
				writerErr = SettlementGenerateDay(context.Background(), "20260920")
			}
			if writerErr == nil && audit {
				writerErr = database.Model(&model.SettlementStatement{}).Where("owner_id = ?", owner.ID).Update("status", settlementStatusAudited).Error
			}
			var audited model.SettlementStatement
			if audit && writerErr == nil {
				writerErr = database.Where("owner_id = ?", owner.ID).First(&audited).Error
			}
			close(resume)
			err = <-result
			if writerErr != nil {
				t.Fatalf("writer: %v", writerErr)
			}
			if err != nil {
				t.Fatalf("retry generation: %v", err)
			}
			if conflicts.Load() != 1 {
				t.Fatalf("expected real typed BUSY_SNAPSHOT (517), got %d", conflicts.Load())
			}
			var rows []model.SettlementStatement
			if err := database.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].GrossRevenue != 9 || rows[0].RequestCount != 1 {
				t.Fatalf("not freshly aggregated/idempotent: %+v", rows)
			}
			if audit {
				if !reflect.DeepEqual(rows[0], audited) {
					t.Fatalf("audited row changed: got %+v want %+v", rows[0], audited)
				}
			} else if writes.Load() != 2 {
				t.Fatalf("expected fresh transaction write, got %d", writes.Load())
			}
			if err := SettlementGenerateDay(context.Background(), "20260920"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSettlementRetrySemanticRollback(t *testing.T) {
	for _, status := range []string{settlementStatusAudited, "held"} {
		t.Run(status, func(t *testing.T) {
			buyer, owner, _ := settlementTestDB(t, "semantic")
			database := db.GetDB()
			pool, _ := database.DB()
			t.Cleanup(func() { pool.Close() })
			second := settlementAddOwner(t, "second")
			start, end, _ := settlementDayWindow("20260920")
			settlementRecord(t, buyer.ID, owner.ID, start.Add(time.Hour), 10, 8, 0)
			settlementRecord(t, buyer.ID, second.ID, start.Add(time.Hour), 10, 8, 0)
			if err := SettlementGenerateDay(context.Background(), "20260920"); err != nil {
				t.Fatal(err)
			}
			if err := database.Model(&model.SettlementStatement{}).Where("owner_id = ?", second.ID).Updates(map[string]any{"status": status, "gross_revenue": 99}).Error; err != nil {
				t.Fatal(err)
			}
			var before, after []model.SettlementStatement
			if err := database.Order("id").Find(&before).Error; err != nil {
				t.Fatal(err)
			}
			attempts := 0
			err := settlementRetry(context.Background(), "sqlite", func() error {
				attempts++
				return settlementGenerateDayOnce(context.Background(), database, "20260920", start, end)
			}, func(context.Context, time.Duration) error { t.Fatal("semantic error retried"); return nil })
			if err == nil || attempts != 1 || (!strings.Contains(err.Error(), "audited") && !strings.Contains(err.Error(), "immutable status")) {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
			if err := database.Order("id").Find(&after).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("partial commit: before=%+v after=%+v", before, after)
			}
		})
	}
}
