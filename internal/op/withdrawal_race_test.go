package op

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 本文件是"提现成熟收益快照 vs 结算入账/退款冲回"的竞争回归:
// 冻结判定必须用事务内行写锁之后重算的当前账, 不是事务外读到的陈旧快照。
//
// 时序用"在途写事务"钉死: 另一个事务先写库占住行写锁、挂住不提交 —— 被测申请的事务外读
// 只能看到旧账(未提交不可见), 写锁放行后冻结判定才见到新账。这个交错每轮都确定发生,
// 断言本身又是无条件的业务不变量(冷却中的收入不可提 / 成熟收入不被误杀), 不依赖调度运气。

// withdrawalRaceHoldWrite 起一个"在途写事务": 先写库占住行写锁, 挂住不提交, 直到返回的
// release 放行并确认提交。write 在事务内执行, 模拟并发的结算入账或退款冲回。
func withdrawalRaceHoldWrite(t *testing.T, write func(tx *gorm.DB) error) (release func()) {
	t.Helper()
	holding := make(chan struct{})
	releaseCh := make(chan struct{})
	done := make(chan error, 1)
	// 放锁只生效一次, 且收尾兜底: 测试中途失败也必须放掉写锁, 否则持锁协程挂死、库关不掉。
	var releaseOnce sync.Once
	safeRelease := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(safeRelease)
	go func() {
		done <- db.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := write(tx); err != nil {
				return err
			}
			close(holding)
			<-releaseCh // 持锁在途, 与被测调用重叠
			return nil
		})
	}()
	// write 失败时 holding 永远不会关: 必须在 holding/done/超时之间选路, 不能干等。
	select {
	case <-holding:
	case err := <-done:
		t.Fatalf("holding transaction failed before taking the lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("holding transaction did not take the write lock in time")
	}
	return func() {
		safeRelease()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("holding transaction: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("holding transaction did not commit after release")
		}
	}
}

// TestWithdrawApplyMaturityRecomputedAfterRowLock 正方向(危险方向):
// 结算入账的新收入在申请读完快照之后、冻结之前落账, 且仍在冷却期 —— 冻结判定必须按锁内
// 重算的成熟收益拒绝申请。事务外的陈旧快照会把冷却中的 50 当成可提, 让 150 全额出池。
func TestWithdrawApplyMaturityRecomputedAfterRowLock(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "maturity-race", 0)
	if err := SettingUpsert(model.SettingKeyWithdrawalCooldownDays, "7"); err != nil {
		t.Fatal(err)
	}
	// 成熟收益 100(10 天前), 收益池 100。
	past := time.Now().AddDate(0, 0, -10)
	if err := db.GetDB().Create(&model.BillingRecord{
		OwnerID: reseller.ID, OwnerRevenue: 100, UserCost: 100,
		Status: model.BillingStatusSettled, CreatedAt: past,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE users SET total_revenue = 100 WHERE id = ?", reseller.ID).Error; err != nil {
		t.Fatal(err)
	}

	// 在途结算入账: +50 收益与"今天"的账单一起占住写锁但未提交。
	release := withdrawalRaceHoldWrite(t, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE users SET total_revenue = total_revenue + 50 WHERE id = ?", reseller.ID).Error; err != nil {
			return err
		}
		return tx.Create(&model.BillingRecord{
			OwnerID: reseller.ID, OwnerRevenue: 50, UserCost: 50,
			Status: model.BillingStatusSettled, CreatedAt: time.Now(),
		}).Error
	})

	result := make(chan error, 1)
	go func() {
		_, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 150))
		result <- err
	}()
	// 让申请先读完旧快照、堵在写锁上, 再放行在途入账: 新账在冻结判定之前到达。
	time.Sleep(300 * time.Millisecond)
	release()

	if err := <-result; err == nil {
		t.Fatal("revenue still cooling down must not back a withdrawal")
	}

	// 被拒不留单、不动冻结额; 收益池落在入账后的 150。
	var count int64
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("user_id = ?", reseller.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected apply must not leave an order, got %d", count)
	}
	var user model.User
	if err := db.GetDB().First(&user, reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.WithdrawFrozen != 0 || user.TotalRevenue != 150 {
		t.Fatalf("frozen=%v revenue=%v, want 0 and 150", user.WithdrawFrozen, user.TotalRevenue)
	}

	// 成熟的 100 照常可提; 冷却中的 50 继续锁住。
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100)); err != nil {
		t.Fatalf("matured revenue must stay withdrawable: %v", err)
	}
	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Frozen != 100 || summary.Revenue != 150 {
		t.Fatalf("frozen=%v revenue=%v, want 100 and 150", summary.Frozen, summary.Revenue)
	}
}

// TestWithdrawApplyConcurrentRefundNotFalseRejected 反方向(误杀方向):
// 在途退款把未成熟收入冲掉之后, 成熟 100 恰好可提 —— 陈旧快照按旧账多算 50 不可提,
// 会把这笔合法申请错误拒绝。锁内重算后必须放行。
func TestWithdrawApplyConcurrentRefundNotFalseRejected(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "refund-race", 0)
	if err := SettingUpsert(model.SettingKeyWithdrawalCooldownDays, "7"); err != nil {
		t.Fatal(err)
	}
	// 成熟 100(10 天前) + 未成熟 50(今天), 收益池 150。
	past := time.Now().AddDate(0, 0, -10)
	if err := db.GetDB().Create(&model.BillingRecord{
		OwnerID: reseller.ID, OwnerRevenue: 100, UserCost: 100,
		Status: model.BillingStatusSettled, CreatedAt: past,
	}).Error; err != nil {
		t.Fatal(err)
	}
	immature := model.BillingRecord{
		OwnerID: reseller.ID, OwnerRevenue: 50, UserCost: 50,
		Status: model.BillingStatusSettled, CreatedAt: time.Now(),
	}
	if err := db.GetDB().Create(&immature).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE users SET total_revenue = 150 WHERE id = ?", reseller.ID).Error; err != nil {
		t.Fatal(err)
	}

	// 在途退款冲回: 与 DisputeReview 批准退款同样的落账效果(账单置 refunded + 冲回渠道商收益)。
	release := withdrawalRaceHoldWrite(t, func(tx *gorm.DB) error {
		if err := tx.Model(&model.BillingRecord{}).Where("id = ?", immature.ID).
			Update("status", model.BillingStatusRefunded).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE users SET total_revenue = total_revenue - ? WHERE id = ?", immature.OwnerRevenue, reseller.ID).Error
	})

	result := make(chan error, 1)
	go func() {
		_, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
		result <- err
	}()
	time.Sleep(300 * time.Millisecond)
	release()

	if err := <-result; err != nil {
		t.Fatalf("matured revenue must stay withdrawable through the concurrent refund: %v", err)
	}
	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatal(err)
	}
	// 退款后池子 100, 全部是成熟收益, 已被这笔申请冻结。
	if summary.Revenue != 100 || summary.Frozen != 100 || summary.Available != 0 {
		t.Fatalf("revenue=%v frozen=%v available=%v, want 100/100/0", summary.Revenue, summary.Frozen, summary.Available)
	}
}
