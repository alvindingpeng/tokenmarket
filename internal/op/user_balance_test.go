package op

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestUserSetBalanceAdjustLedgerDelta 绝对写语义: 把余额设成目标值,
// 差额以库内当前值算并入流水, 同值重放不动钱也不留流水。
func TestUserSetBalanceAdjustLedgerDelta(t *testing.T) {
	user, _ := billingTestSetup(t, "adjust", 100)
	before := billingTestState(t, user.ID)

	if err := UserSetBalance(user.ID, 50, 1, "admin", "下调"); err != nil {
		t.Fatalf("set 50: %v", err)
	}
	mid := billingTestState(t, user.ID)
	if mid.Balance != 50 {
		t.Fatalf("balance=%v, want 50", mid.Balance)
	}
	var row model.BillingLedger
	if err := db.GetDB().Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindAdjust).
		First(&row).Error; err != nil {
		t.Fatalf("load adjust ledger: %v", err)
	}
	if row.Amount != -50 || row.BalanceAfter != 50 {
		t.Fatalf("adjust row=%+v, want amount -50 (50-100) and balance_after 50", row)
	}
	if row.ActorID != 1 || row.ActorName != "admin" || row.Note != "下调" {
		t.Fatalf("adjust row 留痕缺失: %+v", row)
	}

	// 同值再设: 绝对写已满足, 不动钱不记流水。
	if err := UserSetBalance(user.ID, 50, 1, "admin", "重放"); err != nil {
		t.Fatalf("set 50 again: %v", err)
	}
	if got := billingTestLedgerCount(t, user.ID, model.LedgerKindAdjust); got != 1 {
		t.Fatalf("adjust ledger rows=%d, want 1 (同值设置不留流水)", got)
	}

	// 再调整: 差额继续累计。
	if err := UserSetBalance(user.ID, 70, 0, "", ""); err != nil {
		t.Fatalf("set 70: %v", err)
	}
	after := billingTestState(t, user.ID)
	if after.Balance != 70 {
		t.Fatalf("balance=%v, want 70", after.Balance)
	}
	amount, _ := billingTestLedgerSum(t, user.ID, model.LedgerKindAdjust)
	if amount != -30 {
		t.Fatalf("adjust sum=%v, want -30 (70-100, 流水差额 == 实际资金变动)", amount)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceRejectsNegative 负余额拒绝, 资金与流水都不动。
func TestUserSetBalanceRejectsNegative(t *testing.T) {
	user, _ := billingTestSetup(t, "negative", 100)
	before := billingTestState(t, user.ID)
	if err := UserSetBalance(user.ID, -1, 1, "admin", ""); err == nil {
		t.Fatal("negative balance must be rejected")
	}
	after := billingTestState(t, user.ID)
	if after.Balance != 100 {
		t.Fatalf("balance=%v, want 100", after.Balance)
	}
	if got := billingTestLedgerCount(t, user.ID, model.LedgerKindAdjust); got != 0 {
		t.Fatalf("adjust ledger rows=%d, want 0", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceRejectsNonFinite NaN/Inf 会污染账本与库值(NaN 连负数检查都能绕过), 一律拒绝。
func TestUserSetBalanceRejectsNonFinite(t *testing.T) {
	user, _ := billingTestSetup(t, "non-finite", 100)
	before := billingTestState(t, user.ID)
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := UserSetBalance(user.ID, bad, 1, "admin", ""); err == nil {
			t.Fatalf("value %v must be rejected", bad)
		}
	}
	after := billingTestState(t, user.ID)
	if after.Balance != 100 {
		t.Fatalf("balance=%v, want 100 (非法值不得落库)", after.Balance)
	}
	if got := billingTestLedgerCount(t, user.ID, model.LedgerKindAdjust); got != 0 {
		t.Fatalf("adjust ledger rows=%d, want 0", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceRejectsBelowFrozen 目标余额不得低于在途冻结额: 冻结是已承诺的支出,
// 扣到冻结线之下会让可用余额变负、在途预扣结算/释放无处着落; 恰好等于冻结线允许(可用余额为 0)。
func TestUserSetBalanceRejectsBelowFrozen(t *testing.T) {
	user, _ := billingTestSetup(t, "below-frozen", 100)
	before := billingTestState(t, user.ID)
	reservationID := billingTestReserve(t, user.ID, 21, 10) // frozen 10

	if err := UserSetBalance(user.ID, 5, 1, "admin", ""); err == nil {
		t.Fatal("target below the frozen amount must be rejected")
	}
	rejected := billingTestState(t, user.ID)
	if rejected.Balance != 100 || rejected.Frozen != 10 {
		t.Fatalf("被拒调账不得动钱: balance=%v frozen=%v, want 100 and 10", rejected.Balance, rejected.Frozen)
	}
	if got := billingTestLedgerCount(t, user.ID, model.LedgerKindAdjust); got != 0 {
		t.Fatalf("adjust ledger rows=%d, want 0 (被拒不得留流水)", got)
	}

	// 恰好压到冻结线允许: 可用余额为 0, 不为负。
	if err := UserSetBalance(user.ID, 10, 1, "admin", "压到冻结线"); err != nil {
		t.Fatalf("target equal to frozen must be allowed: %v", err)
	}
	mid := billingTestState(t, user.ID)
	if mid.Balance != 10 || mid.Frozen != 10 {
		t.Fatalf("balance=%v frozen=%v, want 10 and 10", mid.Balance, mid.Frozen)
	}

	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release: %v", err)
	}
	after := billingTestState(t, user.ID)
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceDeltaFromDatabaseNotCache 调账回归: 差额必须以库内当前值算,
// 缓存里的旧余额不是账本 —— 用缓存算差额会让流水与真实资金变动对不上。
func TestUserSetBalanceDeltaFromDatabaseNotCache(t *testing.T) {
	user, _ := billingTestSetup(t, "stale-cache", 100)
	before := billingTestState(t, user.ID)

	// 缓存留一份 100 的旧副本; 库里余额被一笔并发入账改成 120 并留了流水,
	// 但那笔入账的缓存同步没来得及跑(或跑失败), 缓存与库不一致。
	userCache.Set(int(user.ID), user)
	if err := db.GetDB().Exec("UPDATE users SET balance = balance + 20 WHERE id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.BillingLedger{
		UserID: user.ID, Kind: model.LedgerKindTopup, Amount: 20, BalanceAfter: 120, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := UserSetBalance(user.ID, 150, 1, "admin", "以库为准"); err != nil {
		t.Fatalf("set balance: %v", err)
	}
	after := billingTestState(t, user.ID)
	if after.Balance != 150 {
		t.Fatalf("balance=%v, want 150", after.Balance)
	}
	var row model.BillingLedger
	if err := db.GetDB().Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindAdjust).
		First(&row).Error; err != nil {
		t.Fatalf("load adjust ledger: %v", err)
	}
	// 差额 = 150 - 120(库内当前值), 不是 150 - 100(缓存旧值): 流水差额必须等于实际资金变动。
	if row.Amount != 30 || row.BalanceAfter != 150 {
		t.Fatalf("adjust row=%+v, want amount 30 (150-120) and balance_after 150", row)
	}
	// 缓存必须被刷回库内口径, 不留旧副本。
	cached, err := UserGetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Balance != 150 {
		t.Fatalf("cached balance=%v, want 150 (缓存不得当账本, 也不得留旧值)", cached.Balance)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceConcurrentSets 并发绝对写: 每轮读库算差额 + CAS,
// 最终余额必是某个目标值, 流水差额之和恰等于实际资金变动, 一笔不丢一笔不重。
func TestUserSetBalanceConcurrentSets(t *testing.T) {
	user, _ := billingTestSetup(t, "adjust-race", 100)
	before := billingTestState(t, user.ID)
	targets := []float64{200, 210, 220, 230, 240}

	errs := make([]error, len(targets))
	start := make(chan struct{}) // 起跑栅栏: 5 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target float64) {
			defer wg.Done()
			<-start
			errs[i] = UserSetBalance(user.ID, target, uint(i+1), fmt.Sprintf("admin%d", i), "并发调账")
		}(i, target)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}

	after := billingTestState(t, user.ID)
	finalIsTarget := false
	for _, target := range targets {
		if after.Balance == target {
			finalIsTarget = true
		}
	}
	if !finalIsTarget {
		t.Fatalf("final balance=%v, 必须是目标值之一 %v (绝对写语义)", after.Balance, targets)
	}
	amount, _ := billingTestLedgerSum(t, user.ID, model.LedgerKindAdjust)
	if !billingMoneyEqual(amount, after.Balance-before.Balance) {
		t.Fatalf("adjust sum=%v, want %v (流水差额 == 实际资金变动)", amount, after.Balance-before.Balance)
	}
	if got := billingTestLedgerCount(t, user.ID, model.LedgerKindAdjust); got != int64(len(targets)) {
		t.Fatalf("adjust rows=%d, want %d (每个成功写入一笔)", got, len(targets))
	}
	billingTestAssertReconciled(t, before, after)
}

// TestUserSetBalanceConcurrentWithBilling 调账与计费并发: 谁都不能写丢谁的钱。
// 4 个并发绝对写 + 1 个结算 + 1 个释放抢同一笔预扣; 结束后逐笔对平:
// 流水 Amount 之和 == 可用余额变动、流水 Frozen 之和 == 冻结额变动,
// 且调账差额之和恰等于调账实际动的钱(计费那次资金变动单独扣除)。
func TestUserSetBalanceConcurrentWithBilling(t *testing.T) {
	user, owner := billingTestSetup(t, "adjust-vs-billing", 100)
	before := billingTestState(t, user.ID)
	reservationID := billingTestReserve(t, user.ID, 11, 10)

	targets := []float64{200, 210, 220, 230}
	results := make([]error, len(targets)+2)
	start := make(chan struct{}) // 起跑栅栏: 6 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target float64) {
			defer wg.Done()
			<-start
			results[i] = UserSetBalance(user.ID, target, uint(i+1), fmt.Sprintf("admin%d", i), "并发调账")
		}(i, target)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		results[len(targets)] = BillingSettle(reservationID, billingTestRecord(user, owner, 11, 4, 2, 100))
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		results[len(targets)+1] = BillingRelease(reservationID)
	}()
	close(start)
	wg.Wait()

	for i, err := range results {
		if err != nil && !errors.Is(err, ErrReservationReleased) && !errors.Is(err, ErrSettlementIntentPending) && !errors.Is(err, ErrSettlementQueued) {
			t.Fatalf("worker %d: unexpected error: %v", i, err)
		}
	}

	after := billingTestState(t, user.ID)
	if after.Frozen != 0 {
		t.Fatalf("frozen=%v, want 0", after.Frozen)
	}
	records := billingTestRecordCount(t, 11)
	settleRows := billingTestLedgerCount(t, user.ID, model.LedgerKindSettle)
	releaseRows := billingTestLedgerCount(t, user.ID, model.LedgerKindRelease)
	if settleRows+releaseRows != 1 {
		t.Fatalf("settle rows=%d release rows=%d, want exactly one finalization", settleRows, releaseRows)
	}
	// 计费那次资金变动: 结算扣 4, 释放不扣。
	billingMove := 0.0
	if records == 1 {
		billingMove = -4
	}
	adjustSum, _ := billingTestLedgerSum(t, user.ID, model.LedgerKindAdjust)
	wantAdjust := after.Balance - before.Balance - billingMove
	if !billingMoneyEqual(adjustSum, wantAdjust) {
		t.Fatalf("adjust sum=%v, want %v (调账流水差额必须等于调账实际资金变动)", adjustSum, wantAdjust)
	}
	billingTestAssertReconciled(t, before, after)
}
