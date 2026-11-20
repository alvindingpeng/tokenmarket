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
		{"busy", "sqlite", busy, settlementMaxAttempts},
		{"locked", "sqlite", fmt.Errorf("outer: %w", errors.New("database table is locked: records")), settlementMaxAttempts},
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
			if tc.attempts == settlementMaxAttempts {
				// 退避必须单调增长且落在 [2^n×10ms, 2^n×10ms × 1.5) 区间内:
				// 单调是"越等越久"的承诺, 上界是"不会等到分钟级"的承诺。
				want := []time.Duration{10, 20, 40, 80, 160, 320, 500}
				if len(delays) != len(want) {
					t.Fatalf("delays=%v", delays)
				}
				for i, base := range want {
					floor := base * time.Millisecond
					limit := floor + floor/2
					if delays[i] < floor || delays[i] >= limit {
						t.Fatalf("第 %d 次退避 %v 不在 [%v, %v)", i, delays[i], floor, limit)
					}
					if i > 0 && delays[i] < delays[i-1] {
						t.Fatalf("退避必须单调增长: %v", delays)
					}
				}
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

// 日结的写事务必须在开头就取写锁: 并发写入者会"在锁上排队", 而不是让日结的读-写升级去撞
// SQLITE_BUSY_SNAPSHOT(517)。本用例用一个真实持有写事务的第二连接来钉这条语义:
//   - 日结在对方持锁期间既不失败也不写脏数据, 而是在锁上等待;
//   - 对方提交后日结正常完成, 全程没有出现 517;
//   - 聚合读取发生在取锁之前, 因此"取锁前刚提交的明细"会落在本次之外 —— 这是有意的时间窗,
//     由下一次重算(幂等 upsert)补齐, 用例把这条语义也钉住, 免得将来被误认为 bug。
func TestSettlementGenerateQueuesOnWriterLockAndRefreshes(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "queue")
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
	settlementRecord(t, buyer.ID, owner.ID, start.Add(time.Hour), 10, 8, 0)

	var conflicts atomic.Int32
	observe := func(tx *gorm.DB) {
		if tx.Error == nil {
			return
		}
		var e *sqlite.Error
		if errors.As(tx.Error, &e) && e.Code() == 517 {
			conflicts.Add(1)
		}
	}
	if err := database.Callback().Create().After("gorm:create").Register("queue_snapshot", observe); err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Update().After("gorm:update").Register("queue_snapshot", observe); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.Callback().Create().Remove("queue_snapshot")
		database.Callback().Update().Remove("queue_snapshot")
	})

	// 第二个连接持有一个真实写事务(已 INSERT, 未提交), 把写锁握住。
	hold := make(chan struct{})
	holding := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- database.Transaction(func(tx *gorm.DB) error {
			// 必须用事务自己的连接写入, 否则 gorm 会因为"事务内没有任何语句"直接不开事务,
			// 这条用例就退化成"没持锁也能过"的假绿。
			extra := model.BillingRecord{
				RequestID:       settlementRequestSeq,
				UserID:          buyer.ID,
				OwnerID:         owner.ID,
				ModelName:       "gpt-4o",
				GroupModel:      "gpt-4o",
				UserCost:        20,
				OwnerRevenue:    16,
				PlatformRevenue: 4,
				InputToken:      10,
				OutputToken:     20,
				Status:          model.BillingStatusSettled,
				CreatedAt:       start.Add(2 * time.Hour),
			}
			if err := tx.Create(&extra).Error; err != nil {
				return err
			}
			close(holding)
			<-hold
			return nil
		})
	}()
	<-holding

	genDone := make(chan error, 1)
	go func() { genDone <- SettlementGenerateDay(context.Background(), "20260920") }()
	// 对方还握着写锁: 日结不许抢先完成(那意味着它绕过了锁), 也不许以锁错误失败。
	select {
	case err := <-genDone:
		t.Fatalf("generation finished while a writer held the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(hold)
	if err := <-writerDone; err != nil {
		t.Fatalf("writer: %v", err)
	}
	select {
	case err := <-genDone:
		if err != nil {
			t.Fatalf("generation after writer commit: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("generation did not resume after the writer committed")
	}
	if conflicts.Load() != 0 {
		t.Fatalf("expected no SQLITE_BUSY_SNAPSHOT (517) once the write lock is taken first, got %d", conflicts.Load())
	}

	// 第一次日结的聚合读取早于对方的提交: 它看到的是 1 条明细。重算必须补齐到 2 条。
	var first model.SettlementStatement
	if err := database.Where("owner_id = ? AND period = ?", owner.ID, "20260920").First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if first.RequestCount != 1 || first.GrossRevenue != 8 {
		t.Fatalf("first generation should reflect the pre-lock snapshot, got %+v", first)
	}
	if err := SettlementGenerateDay(context.Background(), "20260920"); err != nil {
		t.Fatal(err)
	}
	var refreshed model.SettlementStatement
	if err := database.Where("owner_id = ? AND period = ?", owner.ID, "20260920").First(&refreshed).Error; err != nil {
		t.Fatal(err)
	}
	if refreshed.RequestCount != 2 || refreshed.GrossRevenue != 24 {
		t.Fatalf("regeneration must pick up the late record, got %+v", refreshed)
	}
	if conflicts.Load() != 0 {
		t.Fatalf("unexpected 517 during regeneration: %d", conflicts.Load())
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
