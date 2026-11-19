package op

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 本文件是"结算单并发审核 vs 重新生成"的竞争回归:
// 定稿(status=audited)的结算单不可被陈旧重算值覆盖, 未知状态不可被改写。
// 断言都是终态不变量, 不依赖谁先谁后; 确定性的一条直接构造"读快照后被定稿"的交错。

// TestSettlementStaleSnapshotWriteMustNotOverrideAudited 钉死核心缺陷:
// 生成事务早期读到的快照是未审核(settled、旧数字), 写入之前另一路把该单审核定稿 ——
// 拿陈旧快照继续写必须被状态守卫拦下、一行都改不到; 旧实现的 tx.Save 会把重算值连同状态列
// 一起盖回去, 定稿账被静默改写。
func TestSettlementStaleSnapshotWriteMustNotOverrideAudited(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "stale-audit")
	ctx := context.Background()
	day := time.Date(2025, 5, 1, 0, 0, 0, 0, time.Local)
	dayKey := day.Format("20060102")

	settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}

	// 生成事务早期的读快照: 状态 settled、旧数字。
	var stale model.SettlementStatement
	if err := db.GetDB().Where("owner_id = ? AND period = ?", owner.ID, dayKey).First(&stale).Error; err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if stale.Status != settlementStatusSettled {
		t.Fatalf("snapshot status = %q, want %q", stale.Status, settlementStatusSettled)
	}

	// 读完快照之后、写入之前: 管理员把该单审核定稿(状态与数字一起钉死)。
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("id = ?", stale.ID).
		Updates(map[string]any{"status": settlementStatusAudited, "request_count": 7}).Error; err != nil {
		t.Fatalf("mark audited: %v", err)
	}

	// 拿陈旧快照继续写(重算值与定稿不同): 必须被状态守卫拦下。
	next := model.SettlementStatement{
		StatementNo:     stale.StatementNo,
		OwnerID:         owner.ID,
		Period:          dayKey,
		RequestCount:    999,
		InputToken:      999,
		OutputToken:     999,
		GrossRevenue:    999,
		RefundDeduction: 999,
		NetRevenue:      0,
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		return settlementWriteRow(tx, &stale, &next, time.Now())
	})
	if err == nil {
		t.Fatal("stale write over an audited statement must be rejected")
	}
	if !strings.Contains(err.Error(), stale.StatementNo) || !strings.Contains(err.Error(), "audited") {
		t.Fatalf("error must name the statement and the audited conflict: %v", err)
	}

	final := settlementMustGet(t, owner.ID, dayKey)
	if final.Status != settlementStatusAudited || final.RequestCount != 7 || final.GrossRevenue != 100 {
		t.Fatalf("audited statement must stay untouched, got %+v", final)
	}
}

// TestSettlementRegenerateKeepsUnknownStatus 未知状态不可变:
// 未知状态(人工挂起等)拒绝重算，金额、计数、身份和时间戳全部保持不变。
func TestSettlementRegenerateKeepsUnknownStatus(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "unknown-status")
	ctx := context.Background()
	day := time.Date(2025, 5, 2, 0, 0, 0, 0, time.Local)
	dayKey := day.Format("20060102")

	settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	stmt := settlementMustGet(t, owner.ID, dayKey)
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("id = ?", stmt.ID).
		Update("status", "manual_hold").Error; err != nil {
		t.Fatalf("mark manual_hold: %v", err)
	}
	settlementRecord(t, buyer.ID, owner.ID, day.Add(11*time.Hour), 45, 30, 0.5)

	before := settlementMustGet(t, owner.ID, dayKey)
	err := SettlementGenerateDay(ctx, dayKey)
	if err == nil {
		t.Fatal("unknown status must reject regeneration")
	}
	if !strings.Contains(err.Error(), before.StatementNo) {
		t.Fatalf("rejection must name the statement: %v", err)
	}
	refreshed := settlementMustGet(t, owner.ID, dayKey)
	// 拒绝语义是"全字段不变", 不是"保状态刷数字": 金额、计数明确不刷新, 再用 DeepEqual 兜住其余字段。
	if refreshed.RequestCount != before.RequestCount || refreshed.InputToken != before.InputToken ||
		refreshed.OutputToken != before.OutputToken || refreshed.GrossRevenue != before.GrossRevenue ||
		refreshed.RefundDeduction != before.RefundDeduction || refreshed.NetRevenue != before.NetRevenue {
		t.Fatalf("counts/amounts must NOT refresh on unknown status: before=%+v after=%+v", before, refreshed)
	}
	if !reflect.DeepEqual(before, refreshed) {
		t.Fatalf("unknown-status row changed: before=%+v after=%+v", before, refreshed)
	}
}

// TestSettlementConcurrentAuditVsRegenerate 审核定稿与重新生成真并发(SQLite 实库, 起跑栅栏, 多轮):
// 谁先落库每轮不定, 但定稿不可变性每轮恒成立 —— 审核一落库(status=audited + 钉住数字),
// 无论生成事务从哪个窗口撞进来(成功、被拒或撞库锁报错), 终态都必须还是审核时那套数字与状态。
func TestSettlementConcurrentAuditVsRegenerate(t *testing.T) {
	buyer, _, _ := settlementTestDB(t, "audit-race")
	ctx := context.Background()
	const auditMarker = int64(424242)

	const rounds = 10
	for round := 0; round < rounds; round++ {
		owner := settlementAddOwner(t, fmt.Sprintf("audit-race-%d", round))
		day := time.Date(2025, 6, 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, round)
		dayKey := day.Format("20060102")

		settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
		if err := SettlementGenerateDay(ctx, dayKey); err != nil {
			t.Fatalf("round %d seed generate: %v", round, err)
		}
		stmt := settlementMustGet(t, owner.ID, dayKey)
		// 重算值(130)与定稿(100)不同: 覆盖一旦发生就能被终态断言抓到。
		settlementRecord(t, buyer.ID, owner.ID, day.Add(11*time.Hour), 45, 30, 0.5)

		start := make(chan struct{}) // 起跑栅栏: 两个 goroutine 同一时刻真并发进入。
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			// 允许成功或被拒(审核冲突/库锁), 终态断言兜底。
			_ = SettlementGenerateDay(ctx, dayKey)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = db.GetDB().Model(&model.SettlementStatement{}).Where("id = ?", stmt.ID).
				Updates(map[string]any{"status": settlementStatusAudited, "request_count": auditMarker})
		}()
		close(start)
		wg.Wait()

		final := settlementMustGet(t, owner.ID, dayKey)
		if final.Status != settlementStatusAudited || final.RequestCount != auditMarker {
			t.Fatalf("round %d: audited statement was rewritten: %+v", round, final)
		}
	}
}

// TestSettlementWriteRowRejectsNonRegenerableStatus 白名单在写入口自己兜底:
// 只有 settled/空状态可刷新, audited 与未知状态在任何 Updates 之前就被拒绝 —— 就算调用方
// 漏了跳过(如 audited 由调用方跳过), settlementWriteRow 单独面对它们也一行都改不到。
func TestSettlementWriteRowRejectsNonRegenerableStatus(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "write-row-guard")
	ctx := context.Background()
	day := time.Date(2025, 5, 3, 0, 0, 0, 0, time.Local)
	dayKey := day.Format("20060102")

	settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	next := model.SettlementStatement{
		StatementNo:  fmt.Sprintf("STM-%s-%d", dayKey, owner.ID),
		OwnerID:      owner.ID,
		Period:       dayKey,
		RequestCount: 999,
		GrossRevenue: 999,
		NetRevenue:   999,
	}
	for _, status := range []string{settlementStatusAudited, "manual_hold"} {
		if err := db.GetDB().Model(&model.SettlementStatement{}).Where("owner_id = ?", owner.ID).
			Update("status", status).Error; err != nil {
			t.Fatalf("mark %s: %v", status, err)
		}
		before := settlementMustGet(t, owner.ID, dayKey)
		err := db.GetDB().Transaction(func(tx *gorm.DB) error {
			return settlementWriteRow(tx, &before, &next, time.Now())
		})
		if err == nil {
			t.Fatalf("settlementWriteRow must reject status %q before any update", status)
		}
		if !strings.Contains(err.Error(), before.StatementNo) {
			t.Fatalf("rejection must name the statement: %v", err)
		}
		after := settlementMustGet(t, owner.ID, dayKey)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("status %q row changed despite rejection: before=%+v after=%+v", status, before, after)
		}
	}
}
