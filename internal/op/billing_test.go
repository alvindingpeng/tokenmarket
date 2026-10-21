package op

import (
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// billingTestSetup 初始化隔离测试库: 建一个带初始余额的付费用户与一个渠道归属者。
// 用户缓存是进程级单例, 一并清掉, 防止上一个测试的同 ID 旧副本串台。
func billingTestSetup(t *testing.T, username string, balance float64) (payer, owner model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "billing.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()
	payer = model.User{Username: username, Password: "secret", Role: model.RoleUser, Status: model.StatusActive, Balance: balance}
	if err := db.GetDB().Create(&payer).Error; err != nil {
		t.Fatalf("create payer: %v", err)
	}
	owner = model.User{Username: username + "-owner", Password: "secret", Role: model.RoleReseller, Status: model.StatusActive}
	if err := db.GetDB().Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	return payer, owner
}

// billingTestState 读取库内资金口径(余额/冻结/累计), 不看缓存。
func billingTestState(t *testing.T, userID uint) model.User {
	t.Helper()
	var fresh model.User
	if err := db.GetDB().First(&fresh, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return fresh
}

// billingTestReserve 预扣一笔估算费用, 返回预扣记录 ID。
func billingTestReserve(t *testing.T, userID uint, requestID uint64, estimate float64) uint64 {
	t.Helper()
	reservationID, err := BillingReserve(userID, requestID, estimate)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	return reservationID
}

// billingTestRecord 构造一份结算明细: cost 为用户支出, ownerRevenue 为发布者收入, tokens 为用量。
func billingTestRecord(payer, owner model.User, requestID uint64, cost, ownerRevenue float64, tokens int64) *model.BillingRecord {
	return &model.BillingRecord{
		RequestID:    requestID,
		UserID:       payer.ID,
		OwnerID:      owner.ID,
		ModelName:    "test-model",
		InputToken:   tokens,
		OutputToken:  tokens,
		UserCost:     cost,
		OwnerRevenue: ownerRevenue,
	}
}

// billingTestLedgerSum 汇总某用户余额流水的 Amount/Frozen 之和; kind 为空表示全部流水。
func billingTestLedgerSum(t *testing.T, userID uint, kind string) (amount, frozen float64) {
	t.Helper()
	rows := []model.BillingLedger{}
	if err := db.GetDB().Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	for _, row := range rows {
		if kind != "" && row.Kind != kind {
			continue
		}
		amount += row.Amount
		frozen += row.Frozen
	}
	return amount, frozen
}

// billingTestLedgerCount 统计某用户指定类型的流水条数。
func billingTestLedgerCount(t *testing.T, userID uint, kind string) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND kind = ?", userID, kind).Count(&count).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return count
}

// billingTestRequestLedgerCount 统计某笔请求的指定类型流水条数(多轮测试按请求号隔离计数)。
func billingTestRequestLedgerCount(t *testing.T, userID uint, requestID uint64, kind string) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND request_id = ? AND kind = ?", userID, requestID, kind).Count(&count).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return count
}

// billingTestRecordCount 统计某笔请求的计费明细条数。
func billingTestRecordCount(t *testing.T, requestID uint64) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.BillingRecord{}).
		Where("request_id = ?", requestID).Count(&count).Error; err != nil {
		t.Fatalf("count billing records: %v", err)
	}
	return count
}

// billingTestAssertReconciled 流水与资金必须逐笔对平:
// 可用余额变动 == 全部流水 Amount 之和, 冻结额变动 == 全部流水 Frozen 之和。
func billingTestAssertReconciled(t *testing.T, before, after model.User) {
	t.Helper()
	amount, frozen := billingTestLedgerSum(t, before.ID, "")
	availableBefore := before.Balance - before.Frozen
	availableAfter := after.Balance - after.Frozen
	if !billingMoneyEqual(availableAfter-availableBefore, amount) {
		t.Fatalf("流水 Amount 之和 %v != 可用余额变动 %v", amount, availableAfter-availableBefore)
	}
	if !billingMoneyEqual(after.Frozen-before.Frozen, frozen) {
		t.Fatalf("流水 Frozen 之和 %v != 冻结额变动 %v", frozen, after.Frozen-before.Frozen)
	}
}

func TestBillingReserveRejectsNonFiniteEstimate(t *testing.T) {
	payer, _ := billingTestSetup(t, "non-finite-reserve", 100)
	for _, estimate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := BillingReserve(payer.ID, uint64(time.Now().UnixNano()), estimate); err == nil {
			t.Fatalf("BillingReserve(%v) succeeded", estimate)
		}
	}
	if got := billingTestState(t, payer.ID).Frozen; got != 0 {
		t.Fatalf("invalid estimates changed frozen balance to %v", got)
	}
}

// TestBillingReserveReleaseRepeatedRelease 预扣冻结、释放退回; 重复释放幂等, 冻结额只退一次。
func TestBillingReserveReleaseRepeatedRelease(t *testing.T) {
	payer, _ := billingTestSetup(t, "release-twice", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 1, 10)

	mid := billingTestState(t, payer.ID)
	if mid.Balance != 100 || mid.Frozen != 10 {
		t.Fatalf("after reserve: balance=%v frozen=%v, want 100 and 10", mid.Balance, mid.Frozen)
	}

	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release #1: %v", err)
	}
	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release #2 (重复释放应幂等成功): %v", err)
	}
	after := billingTestState(t, payer.ID)
	if after.Balance != 100 || after.Frozen != 0 {
		t.Fatalf("after release: balance=%v frozen=%v, want 100 and 0 (冻结额只能退一次)", after.Balance, after.Frozen)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindRelease); got != 1 {
		t.Fatalf("release ledger rows=%d, want 1", got)
	}
	if err := BillingRelease(9999); err == nil {
		t.Fatal("不存在的预扣必须报错, 不能静默装成功")
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingSettleMovesMoneyOnce 正常结算: 解冻预估、扣实际支出、记发布者收入并写三方对平的明细。
func TestBillingSettleMovesMoneyOnce(t *testing.T) {
	payer, owner := billingTestSetup(t, "settle", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 2, 10)
	if err := BillingSettle(reservationID, billingTestRecord(payer, owner, 2, 4, 2, 100)); err != nil {
		t.Fatalf("settle: %v", err)
	}

	after := billingTestState(t, payer.ID)
	if after.Balance != 96 || after.Frozen != 0 || after.TotalSpent != 4 {
		t.Fatalf("payer after settle: balance=%v frozen=%v spent=%v, want 96/0/4", after.Balance, after.Frozen, after.TotalSpent)
	}
	if ownerAfter := billingTestState(t, owner.ID); ownerAfter.TotalRevenue != 2 {
		t.Fatalf("owner revenue=%v, want 2", ownerAfter.TotalRevenue)
	}

	var settleRow model.BillingLedger
	if err := db.GetDB().Where("user_id = ? AND kind = ?", payer.ID, model.LedgerKindSettle).
		First(&settleRow).Error; err != nil {
		t.Fatalf("load settle ledger: %v", err)
	}
	// 结算流水: 净变动 = 预估 10 - 实际 4, 冻结额全额解冻。
	if settleRow.Amount != 6 || settleRow.Frozen != -10 || settleRow.Cost != 4 || settleRow.BalanceAfter != 96 {
		t.Fatalf("settle ledger row: %+v, want amount 6 / frozen -10 / cost 4 / balance_after 96", settleRow)
	}

	var record model.BillingRecord
	if err := db.GetDB().Where("request_id = ?", uint64(2)).First(&record).Error; err != nil {
		t.Fatalf("load billing record: %v", err)
	}
	if record.UserCost != 4 || record.OwnerRevenue != 2 || record.PlatformRevenue != 2 {
		t.Fatalf("billing record money: %+v, want 4/2/2 (平台收入 = 用户支出 - 发布者收入)", record)
	}
	if record.Status != model.BillingStatusSettled {
		t.Fatalf("record status=%s, want %s", record.Status, model.BillingStatusSettled)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingSettleRepeatedSameUsageIdempotent 同一笔结算重复投递: 幂等成功, 不再动钱、不再记账。
func TestBillingSettleRepeatedSameUsageIdempotent(t *testing.T) {
	payer, owner := billingTestSetup(t, "settle-twice", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 3, 10)
	if err := BillingSettle(reservationID, billingTestRecord(payer, owner, 3, 4, 2, 100)); err != nil {
		t.Fatalf("settle #1: %v", err)
	}
	after := billingTestState(t, payer.ID)

	replay := billingTestRecord(payer, owner, 3, 4, 2, 100) // 与 #1 同用量同费用
	if err := BillingSettle(reservationID, replay); err != nil {
		t.Fatalf("settle #2 (同用量重放应幂等成功): %v", err)
	}
	final := billingTestState(t, payer.ID)
	if final.Balance != after.Balance || final.Frozen != after.Frozen || final.TotalSpent != after.TotalSpent {
		t.Fatalf("重放不得动钱: after=%+v final=%+v", after, final)
	}
	if got := billingTestRecordCount(t, 3); got != 1 {
		t.Fatalf("billing records=%d, want 1 (重放不得重复记账)", got)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindSettle); got != 1 {
		t.Fatalf("settle ledger rows=%d, want 1", got)
	}
	billingTestAssertReconciled(t, before, final)
}

// TestBillingSettleRepeatedDifferentUsageRejected 同一预扣再结算但用量/费用不同:
// 拒绝(ErrSettlementMismatch), 不静默吞掉不同的用量, 第一笔定稿的账保持不动。
func TestBillingSettleRepeatedDifferentUsageRejected(t *testing.T) {
	payer, owner := billingTestSetup(t, "usage-mismatch", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 4, 10)
	if err := BillingSettle(reservationID, billingTestRecord(payer, owner, 4, 4, 2, 100)); err != nil {
		t.Fatalf("settle #1: %v", err)
	}
	after := billingTestState(t, payer.ID)

	changedCost := billingTestRecord(payer, owner, 4, 9, 2, 100) // 费用不同
	if err := BillingSettle(reservationID, changedCost); !errors.Is(err, ErrSettlementMismatch) {
		t.Fatalf("different cost err=%v, want ErrSettlementMismatch", err)
	}
	changedUsage := billingTestRecord(payer, owner, 4, 4, 2, 999) // 用量不同
	if err := BillingSettle(reservationID, changedUsage); !errors.Is(err, ErrSettlementMismatch) {
		t.Fatalf("different usage err=%v, want ErrSettlementMismatch", err)
	}

	final := billingTestState(t, payer.ID)
	if final.Balance != after.Balance || final.Frozen != after.Frozen || final.TotalSpent != after.TotalSpent {
		t.Fatalf("被拒的重放不得动钱: after=%+v final=%+v", after, final)
	}
	if got := billingTestRecordCount(t, 4); got != 1 {
		t.Fatalf("billing records=%d, want 1 (第一笔定稿的账不能被改写)", got)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindSettle); got != 1 {
		t.Fatalf("settle ledger rows=%d, want 1", got)
	}
	billingTestAssertReconciled(t, before, final)
}

// TestBillingSettleAfterReleaseRejected 预扣已释放(钱已退回)再结算: 拒绝补扣, 冻结额不得被扣成负数。
func TestBillingSettleAfterReleaseRejected(t *testing.T) {
	payer, owner := billingTestSetup(t, "settle-after-release", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 5, 10)
	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release: %v", err)
	}

	err := BillingSettle(reservationID, billingTestRecord(payer, owner, 5, 4, 2, 100))
	if !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("settle after release err=%v, want ErrReservationReleased", err)
	}
	after := billingTestState(t, payer.ID)
	if after.Balance != 100 || after.Frozen != 0 {
		t.Fatalf("补扣被拒后资金必须原样: balance=%v frozen=%v, want 100 and 0", after.Balance, after.Frozen)
	}
	if got := billingTestRecordCount(t, 5); got != 0 {
		t.Fatalf("billing records=%d, want 0", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingReleaseAfterSettleDoesNotRefund 结算后收到释放: 幂等成功但绝不退款、绝不二次解冻。
func TestBillingReleaseAfterSettleDoesNotRefund(t *testing.T) {
	payer, owner := billingTestSetup(t, "release-after-settle", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 6, 10)
	if err := BillingSettle(reservationID, billingTestRecord(payer, owner, 6, 4, 2, 100)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	after := billingTestState(t, payer.ID)

	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release after settle (应幂等成功): %v", err)
	}
	final := billingTestState(t, payer.ID)
	if final.Balance != after.Balance || final.Frozen != 0 {
		t.Fatalf("已结算的预扣不得退款: after=%+v final=%+v", after, final)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindRelease); got != 0 {
		t.Fatalf("release ledger rows=%d, want 0 (已结算的预扣不得再退冻结)", got)
	}
	billingTestAssertReconciled(t, before, final)
}

// TestBillingSettleRecordMismatchRejected 结算明细与预扣归属不一致(错请求号): 拒绝, 资金不动。
func TestBillingSettleRecordMismatchRejected(t *testing.T) {
	payer, owner := billingTestSetup(t, "record-mismatch", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 12, 10)

	wrongRequest := billingTestRecord(payer, owner, 999, 4, 2, 100)
	if err := BillingSettle(reservationID, wrongRequest); err == nil {
		t.Fatal("外来请求号的结算明细必须被拒绝")
	}
	after := billingTestState(t, payer.ID)
	if after.Balance != 100 || after.Frozen != 10 {
		t.Fatalf("被拒结算不得动钱: balance=%v frozen=%v, want 100 and 10", after.Balance, after.Frozen)
	}
	if got := billingTestRecordCount(t, 12); got != 0 {
		t.Fatalf("billing records=%d, want 0", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingConcurrentSettleExactlyOnce 并发重复结算同一笔预扣(用量一致):
// 钱只动一次, 全部调用按幂等成功收场。
func TestBillingConcurrentSettleExactlyOnce(t *testing.T) {
	payer, owner := billingTestSetup(t, "settle-race", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 7, 10)

	const workers = 8
	errs := make([]error, workers)
	start := make(chan struct{}) // 起跑栅栏: 8 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = BillingSettle(reservationID, billingTestRecord(payer, owner, 7, 4, 2, 100))
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: 同用量并发结算应全部幂等成功: %v", i, err)
		}
	}

	after := billingTestState(t, payer.ID)
	if after.Balance != 96 || after.Frozen != 0 || after.TotalSpent != 4 {
		t.Fatalf("钱只能动一次: balance=%v frozen=%v spent=%v, want 96/0/4", after.Balance, after.Frozen, after.TotalSpent)
	}
	if got := billingTestRecordCount(t, 7); got != 1 {
		t.Fatalf("billing records=%d, want 1", got)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindSettle); got != 1 {
		t.Fatalf("settle ledger rows=%d, want 1", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingConcurrentReleaseExactlyOnce 并发重复释放同一笔预扣: 冻结额只退一次。
func TestBillingConcurrentReleaseExactlyOnce(t *testing.T) {
	payer, _ := billingTestSetup(t, "release-race", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 9, 10)

	const workers = 8
	errs := make([]error, workers)
	start := make(chan struct{}) // 起跑栅栏: 8 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = BillingRelease(reservationID)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: 并发释放应全部幂等成功: %v", i, err)
		}
	}

	after := billingTestState(t, payer.ID)
	if after.Balance != 100 || after.Frozen != 0 {
		t.Fatalf("冻结额只能退一次: balance=%v frozen=%v, want 100 and 0", after.Balance, after.Frozen)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindRelease); got != 1 {
		t.Fatalf("release ledger rows=%d, want 1", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingConcurrentSettleVsRelease 结算与释放真并发竞争(SQLite 实库, 起跑栅栏对齐, 多轮重复):
// 终态只属于一方, 冻结额只被动一次。谁抢到终态每轮都不确定, 但资金不变量必须每轮恒成立:
//   - 结算赢 → 扣费一次、明细一条, 释放方幂等空转;
//   - 释放赢 → 全额退回、明细零条, 结算方报 ErrReservationReleased(不得静默接受)。
func TestBillingConcurrentSettleVsRelease(t *testing.T) {
	payer, owner := billingTestSetup(t, "settle-vs-release", 1000)
	first := billingTestState(t, payer.ID)
	settleWins, releaseWins := 0, 0

	const rounds = 20
	for round := 0; round < rounds; round++ {
		requestID := uint64(100 + round)
		before := billingTestState(t, payer.ID)
		reservationID := billingTestReserve(t, payer.ID, requestID, 10)

		const settlers, releasers = 2, 2
		errs := make([]error, settlers+releasers)
		start := make(chan struct{}) // 起跑栅栏: 4 个 goroutine 同一时刻真并发进入。
		var wg sync.WaitGroup
		for i := 0; i < settlers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = BillingSettle(reservationID, billingTestRecord(payer, owner, requestID, 4, 2, 100))
			}(i)
		}
		for i := 0; i < releasers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[settlers+i] = BillingRelease(reservationID)
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil && !errors.Is(err, ErrReservationReleased) && !errors.Is(err, ErrSettlementIntentPending) && !errors.Is(err, ErrSettlementQueued) {
				t.Fatalf("round %d worker %d: 意外错误: %v", round, i, err)
			}
		}
		after := billingTestState(t, payer.ID)
		records := billingTestRecordCount(t, requestID)
		settleRows := billingTestRequestLedgerCount(t, payer.ID, requestID, model.LedgerKindSettle)
		releaseRows := billingTestRequestLedgerCount(t, payer.ID, requestID, model.LedgerKindRelease)
		if after.Frozen != before.Frozen {
			t.Fatalf("round %d: frozen=%v, want %v (冻结额只被动一次, 不得悬空或透支)", round, after.Frozen, before.Frozen)
		}
		switch {
		case records == 1:
			settleWins++
			if settleRows != 1 || releaseRows != 0 || after.Balance != before.Balance-4 || after.TotalSpent != before.TotalSpent+4 {
				t.Fatalf("round %d 结算赢: records=%d settle=%d release=%d user=%+v before=%+v",
					round, records, settleRows, releaseRows, after, before)
			}
		case records == 0:
			releaseWins++
			if settleRows != 0 || releaseRows != 1 || after.Balance != before.Balance || after.TotalSpent != before.TotalSpent {
				t.Fatalf("round %d 释放赢: records=%d settle=%d release=%d user=%+v before=%+v",
					round, records, settleRows, releaseRows, after, before)
			}
		default:
			t.Fatalf("round %d: records=%d, want 0 or 1", round, records)
		}
		billingTestAssertReconciled(t, first, after)
	}
	t.Logf("真并发 %d 轮: 结算赢 %d 轮, 释放赢 %d 轮", rounds, settleWins, releaseWins)
}

// TestBillingConcurrentIncompatibleSettleNotSilent 并发混入不同用量的结算: 钱只动一次、账只记一次,
// 与已落账用量一致的重放幂等成功, 不一致的一律 ErrSettlementMismatch —— 绝不静默接受不同的用量。
func TestBillingConcurrentIncompatibleSettleNotSilent(t *testing.T) {
	payer, owner := billingTestSetup(t, "mixed-usage-race", 200)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 14, 10)

	type attempt struct {
		cost   float64
		tokens int64
		err    error
	}
	attempts := []attempt{
		{cost: 4, tokens: 100}, {cost: 4, tokens: 100}, // 账 A: 两份相同的
		{cost: 9, tokens: 250}, {cost: 9, tokens: 250}, // 账 B: 费用与用量都不同
		{cost: 4, tokens: 7}, // 账 C: 费用同、用量不同
	}
	start := make(chan struct{}) // 起跑栅栏: 5 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			attempts[i].err = BillingSettle(reservationID,
				billingTestRecord(payer, owner, 14, attempts[i].cost, 2, attempts[i].tokens))
		}(i)
	}
	close(start)
	wg.Wait()

	// 谁抢到终态谁定稿; 每个调用的收场必须与"定稿的那笔账"自洽。
	var record model.BillingRecord
	if err := db.GetDB().Where("request_id = ?", uint64(14)).First(&record).Error; err != nil {
		t.Fatalf("load settled record: %v", err)
	}
	for i, a := range attempts {
		matches := billingMoneyEqual(a.cost, record.UserCost) && a.tokens == record.InputToken
		switch {
		case a.err == nil || errors.Is(a.err, ErrSettlementQueued):
			if !matches {
				t.Fatalf("worker %d: 不同用量被静默接受 (cost=%v tokens=%d, 落账 cost=%v tokens=%d)",
					i, a.cost, a.tokens, record.UserCost, record.InputToken)
			}
		case errors.Is(a.err, ErrSettlementMismatch):
			if matches {
				t.Fatalf("worker %d: 同用量重放却被拒绝: %v", i, a.err)
			}
		default:
			t.Fatalf("worker %d: 意外错误: %v", i, a.err)
		}
	}

	after := billingTestState(t, payer.ID)
	if after.Frozen != 0 || after.TotalSpent != record.UserCost || after.Balance != 200-record.UserCost {
		t.Fatalf("钱只能按定稿的那笔账动一次: user=%+v (落账 cost=%v)", after, record.UserCost)
	}
	if got := billingTestRecordCount(t, 14); got != 1 {
		t.Fatalf("billing records=%d, want 1", got)
	}
	if got := billingTestLedgerCount(t, payer.ID, model.LedgerKindSettle); got != 1 {
		t.Fatalf("settle ledger rows=%d, want 1", got)
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingConcurrentStaleSweepVsSettle 超时清理与正常结算并发(BillingReleaseStale 是定时任务的入口):
// 同样只有一方终态, 清理不得给已结算的预扣退款。
func TestBillingConcurrentStaleSweepVsSettle(t *testing.T) {
	payer, owner := billingTestSetup(t, "sweep-vs-settle", 100)
	before := billingTestState(t, payer.ID)
	reservationID := billingTestReserve(t, payer.ID, 10, 10)
	// 模拟升级前无租约预扣，确保清扫仍真正参与竞争，而不是只验证正常结算。
	if err := db.GetDB().Model(&model.BalanceReservation{}).Where("id = ?", reservationID).Update("lease_expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}

	errs := make([]error, 2)
	start := make(chan struct{}) // 起跑栅栏: 结算与超时清理真并发。
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs[0] = BillingSettle(reservationID, billingTestRecord(payer, owner, 10, 4, 2, 100))
	}()
	go func() {
		defer wg.Done()
		<-start
		// olderThan=0 让这笔预扣立即命中"超时未结算"条件。
		errs[1] = BillingReleaseStale(0)
	}()
	close(start)
	wg.Wait()
	for i, err := range errs {
		// 清理抢到终态时, 结算方按"预扣已释放"被拒是正确收场; 其余错误都算异常。
		if err != nil && !errors.Is(err, ErrReservationReleased) && !errors.Is(err, ErrSettlementIntentPending) && !errors.Is(err, ErrSettlementQueued) {
			t.Fatalf("worker %d: %v", i, err)
		}
	}

	after := billingTestState(t, payer.ID)
	settleRows := billingTestLedgerCount(t, payer.ID, model.LedgerKindSettle)
	releaseRows := billingTestLedgerCount(t, payer.ID, model.LedgerKindRelease)
	if settleRows+releaseRows != 1 {
		t.Fatalf("settle rows=%d release rows=%d, want exactly one finalization", settleRows, releaseRows)
	}
	if after.Frozen != 0 {
		t.Fatalf("frozen=%v, want 0", after.Frozen)
	}
	if settleRows == 1 {
		if after.Balance != 96 || after.TotalSpent != 4 || billingTestRecordCount(t, 10) != 1 {
			t.Fatalf("结算赢但账不对: %+v", after)
		}
	} else {
		if after.Balance != 100 || after.TotalSpent != 0 || billingTestRecordCount(t, 10) != 0 {
			t.Fatalf("清理赢但账不对: %+v", after)
		}
	}
	billingTestAssertReconciled(t, before, after)
}

// TestBillingSameUsageComparison 用量比较口径: 金额在 6 位小数舍入容差内视为同笔, 其余逐项精确。
func TestBillingSameUsageComparison(t *testing.T) {
	payer, owner := billingTestSetup(t, "same-usage", 100)
	base := billingTestRecord(payer, owner, 13, 4, 2, 100)
	base.PlatformRevenue = 2

	rounded := billingTestRecord(payer, owner, 13, 4+2e-7, 2, 100) // decimal(18,6) 舍入级别差异
	rounded.PlatformRevenue = 2
	if !billingSameUsage(base, rounded) {
		t.Fatal("decimal(18,6) 舍入级别的金额差异应视为同一笔结算")
	}
	for name, other := range map[string]*model.BillingRecord{
		"cost":   billingTestRecord(payer, owner, 13, 4.5, 2, 100),
		"tokens": billingTestRecord(payer, owner, 13, 4, 2, 101),
		"owner":  billingTestRecord(payer, owner, 13, 4, 2.5, 100),
	} {
		if billingSameUsage(base, other) {
			t.Fatalf("%s 不同应判为另一笔结算", name)
		}
	}
	foreign := billingTestRecord(payer, owner, 13, 4, 2, 100)
	foreign.OwnerID = owner.ID + 1
	if billingSameUsage(base, foreign) {
		t.Fatal("收款人不同应判为另一笔结算")
	}
}
