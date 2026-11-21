package op

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// withdrawalTestSetup 初始化隔离库: 建一个带收益池的渠道商与一个普通用户。
func withdrawalTestSetup(t *testing.T, username string, revenue float64) (model.User, model.User) {
	t.Helper()
	opTestDBSetup(t, "withdraw.db")
	for key, value := range map[model.SettingKey]string{
		model.SettingKeyRechargeRate:               "7.0",
		model.SettingKeyWithdrawalMinAmount:        "50",
		model.SettingKeyWithdrawalFeeRate:          "0.01",
		model.SettingKeyWithdrawalFeeMin:           "1",
		model.SettingKeyWithdrawalCooldownDays:     "0",
		model.SettingKeyWithdrawalDailyCountLimit:  "0",
		model.SettingKeyWithdrawalDailyAmountLimit: "0",
		model.SettingKeyWithdrawalAccountLockDays:  "0",
	} {
		if err := SettingUpsert(key, value); err != nil {
			t.Fatalf("seed setting %s: %v", key, err)
		}
	}
	reseller := model.User{Username: username, Password: "secret", Role: model.RoleReseller, Status: model.StatusActive, TotalRevenue: revenue}
	if err := db.GetDB().Create(&reseller).Error; err != nil {
		t.Fatalf("create reseller: %v", err)
	}
	normal := model.User{Username: username + "-user", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&normal).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return reseller, normal
}

// withdrawalSpec 造一份申请参数。
func withdrawalSpec(userID uint, role model.Role, amount float64) WithdrawSpec {
	return WithdrawSpec{
		UserID: userID, Role: role, Amount: amount,
		Method: model.WithdrawMethodAlipay, AccountName: "张三", AccountNo: "6222021234567890",
		PayoutCurrency: "CNY", // 默认存量口径; 显式传 USD 的行为在专门用例覆盖。
	}
}

// TestWithdrawTimeoutRevoke 审批超时未打款的提现单必须被自动撤销并把冻结额精确退回可提。
//
// 这是"approved 单向门"的回归: 审批只改状态不动 withdraw_frozen, 而解冻只发生在打款成功
// 或打款回滚两条路径上, 于是管理员点过"通过"忘了打款的单会把用户的钱永久冻住 ——
// 用户提不走(已冻结)、退不回(没有取消入口)、管理员不回头就再也不会解冻。
func TestWithdrawTimeoutRevoke(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "approve-timeout", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatalf("review: %v", err)
	}
	// 审批时间推到时限之外: 这是唯一让任务认定"超时"的量。用 reviewed_at 而不是 applied_at
	// 或 updated_at 计时, 后续任何写操作都不会把它意外重置。
	stale := time.Now().Add(-WithdrawApproveTimeout - time.Hour)
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).Update("reviewed_at", stale).Error; err != nil {
		t.Fatal(err)
	}

	revoked, err := WithdrawTimeoutRevoke(context.Background())
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked=%d, want 1", revoked)
	}
	var fresh model.Withdrawal
	if err := db.GetDB().First(&fresh, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	// cancelled 而不是 rejected: 管理员是审过了的, 只是没在时限内打款, 不该被记成"拒绝过"。
	if fresh.Status != model.WithdrawStatusCancelled {
		t.Fatalf("status=%s, want cancelled", fresh.Status)
	}
	// 冻结额必须精确减回: 多减是白送钱, 少减是钱还冻着。
	var user model.User
	if err := db.GetDB().First(&user, reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.WithdrawFrozen != 0 {
		t.Fatalf("frozen=%v, want 0 after revoke", user.WithdrawFrozen)
	}
	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Available != 100 || summary.Frozen != 0 || summary.Withdrawn != 0 {
		t.Fatalf("available=%v frozen=%v withdrawn=%v, want 100/0/0", summary.Available, summary.Frozen, summary.Withdrawn)
	}
	// 解冻流水必须留痕: 资金迁移没有流水等于对不上账。
	var unfreeze int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND kind = ?", reseller.ID, model.LedgerKindWithdrawUnfreeze).Count(&unfreeze).Error; err != nil {
		t.Fatal(err)
	}
	if unfreeze != 1 {
		t.Fatalf("withdraw_unfreeze rows=%d, want 1", unfreeze)
	}
}

// TestWithdrawTimeoutRevokeKeepsFreshApproved 未超时的 approved 单不许动: 管理员可能正在打款。
func TestWithdrawTimeoutRevokeKeepsFreshApproved(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "approve-fresh", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	// 时限内(刚审批): 任务必须一笔都不撤。
	if _, err := WithdrawTimeoutRevoke(context.Background()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var fresh model.Withdrawal
	if err := db.GetDB().First(&fresh, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.WithdrawStatusApproved {
		t.Fatalf("status=%s, want approved (inside the window)", fresh.Status)
	}
	var user model.User
	if err := db.GetDB().First(&user, reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.WithdrawFrozen != 100 {
		t.Fatalf("frozen=%v, want 100 untouched", user.WithdrawFrozen)
	}
	// 管理员仍可手动打款: 兜底撤销绝不能把正常路径堵掉。
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX900", "", "admin"); err != nil {
		t.Fatalf("manual mark paid must still work: %v", err)
	}
}

// TestWithdrawTimeoutRevokeSkipsPaid 已打款的单不撤: 钱已出池, 再撤就是凭空多退一笔。
func TestWithdrawTimeoutRevokeSkipsPaid(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "approve-paid", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX901", "", "admin"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	// 故意把审批时间推到时限之外: 唯一拦住撤销的是 status 已是 paid, 不是时间。
	stale := time.Now().Add(-WithdrawApproveTimeout - time.Hour)
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).Update("reviewed_at", stale).Error; err != nil {
		t.Fatal(err)
	}
	revoked, err := WithdrawTimeoutRevoke(context.Background())
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 0 {
		t.Fatalf("revoked=%d, want 0 (paid orders are out of scope)", revoked)
	}
	var user model.User
	if err := db.GetDB().First(&user, reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	// 打款后冻结已转累计已提; 撤销若能改到它, 这两个数会同时被动。
	if user.WithdrawFrozen != 0 || user.TotalWithdrawn != 100 {
		t.Fatalf("frozen=%v withdrawn=%v, want 0 and 100", user.WithdrawFrozen, user.TotalWithdrawn)
	}
}

// TestWithdrawTimeoutRevokeConcurrentNoDoubleUnfreeze 并发重复跑任务至多解冻一次:
// 每笔解冻都是真金白银, 双解冻等于把同一笔钱退两遍。
func TestWithdrawTimeoutRevokeConcurrentNoDoubleUnfreeze(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "approve-race", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-WithdrawApproveTimeout - time.Hour)
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).Update("reviewed_at", stale).Error; err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var total int64
	for round := 0; round < 4; round++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 允许报错(SQLite 写锁竞争/并发下 RowsAffected=0): 终态不变量兜底。
			if n, err := WithdrawTimeoutRevoke(context.Background()); err == nil {
				mu.Lock()
				total += n
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	// 四轮合计最多撤一笔: 谁先抢到谁解冻, 其余轮次扫不到 status='approved'。
	if total != 1 {
		t.Fatalf("total revoked across rounds=%d, want exactly 1", total)
	}
	var user model.User
	if err := db.GetDB().First(&user, reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.WithdrawFrozen != 0 {
		t.Fatalf("frozen=%v, want 0 (single unfreeze)", user.WithdrawFrozen)
	}
	// 双解冻的直接证据: 冻结额被减成负数, 或者流水出现两条。
	var unfreeze int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND kind = ?", reseller.ID, model.LedgerKindWithdrawUnfreeze).Count(&unfreeze).Error; err != nil {
		t.Fatal(err)
	}
	if unfreeze != 1 {
		t.Fatalf("withdraw_unfreeze rows=%d, want exactly 1", unfreeze)
	}
	// 审计也只能有一条: 只有"真撤成功"的那一轮才许写, 没抢到的轮次写假记录会让事后
	// 对账变成"多撤销了一笔我找不到"的死胡同。
	var audits int64
	if err := db.GetDB().Model(&model.AuditLog{}).
		Where("action = ?", "withdrawal.timeout_revoke").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("timeout_revoke audit rows=%d, want exactly 1", audits)
	}
}

// TestWithdrawTimeoutRevokeAuditsEachOrder 撤销必须逐笔留审计:
// 这是无人值守的资金迁移, 事后只能靠审计回答"哪笔、为什么、谁撤的"。
func TestWithdrawTimeoutRevokeAuditsEachOrder(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "approve-audit", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-WithdrawApproveTimeout - time.Hour)
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).Update("reviewed_at", stale).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := WithdrawTimeoutRevoke(context.Background()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var audits []model.AuditLog
	if err := db.GetDB().Where("action = ?", "withdrawal.timeout_revoke").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit rows=%d, want 1", len(audits))
	}
	// actorID=0 表示系统发起(不是某个管理员手动操作的)。
	if audits[0].UserID != 0 || audits[0].Target != fmt.Sprintf("withdrawal#%d", order.ID) {
		t.Fatalf("audit actor=%d target=%q", audits[0].UserID, audits[0].Target)
	}
}

// TestWithdrawApplyAndFreeze 申请即冻结: 可提减少, 冻结增加, 打款后转累计已提。
func TestWithdrawApplyAndFreeze(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "freeze", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if order.Fee != 1 || order.Actual != 99 {
		t.Fatalf("fee=%v actual=%v, want 1 and 99", order.Fee, order.Actual)
	}
	if order.Rate != 7 || order.ActualCents != 69300 {
		t.Fatalf("rate=%v cents=%d, want 7 and 69300", order.Rate, order.ActualCents)
	}
	if order.Currency != "CNY" || order.RateDay != "" || order.RateSource != "static" || order.ActualCurrency != "" {
		t.Fatalf("currency=%q day=%q source=%q actual=%q, want CNY/empty/static/empty", order.Currency, order.RateDay, order.RateSource, order.ActualCurrency)
	}
	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Available != 0 || summary.Frozen != 100 {
		t.Fatalf("available=%v frozen=%v, want 0 and 100", summary.Available, summary.Frozen)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatalf("review: %v", err)
	}
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX001", "", "admin"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	summary, _ = WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Available != 0 || summary.Frozen != 0 || summary.Withdrawn != 100 {
		t.Fatalf("available=%v frozen=%v withdrawn=%v", summary.Available, summary.Frozen, summary.Withdrawn)
	}
	balance, _ := redemptionTestBalance(t, reseller.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0 (提现不动 Balance)", balance)
	}
}

// TestWithdrawInsufficientRejects 可提不足时整单拒绝, 不留冻结。
func TestWithdrawInsufficientRejects(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "insufficient", 60)
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100)); err == nil {
		t.Fatal("insufficient withdrawable must be rejected")
	}
	summary, _ := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Frozen != 0 || summary.Available != 60 {
		t.Fatalf("frozen=%v available=%v", summary.Frozen, summary.Available)
	}
}

// TestWithdrawNormalUserRejected 普通用户不可提现(D4)。
func TestWithdrawNormalUserRejected(t *testing.T) {
	reseller, normal := withdrawalTestSetup(t, "role-gate", 100)
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(normal.ID, model.RoleUser, 60)); err == nil {
		t.Fatal("normal user must be rejected")
	}
	// 渠道商同金额可申请: 同一接口同一规则, 只差角色门。
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60)); err != nil {
		t.Fatalf("reseller must be allowed: %v", err)
	}
}

// TestWithdrawCancelUnfreezes 取消解冻, 可提回补; 重复取消拒绝。
func TestWithdrawCancelUnfreezes(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "cancel", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawCancel(context.Background(), reseller.ID, order.ID, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := WithdrawCancel(context.Background(), reseller.ID, order.ID, ""); err == nil {
		t.Fatal("double cancel must be rejected")
	}
	summary, _ := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Available != 100 || summary.Frozen != 0 {
		t.Fatalf("available=%v frozen=%v, want 100 and 0", summary.Available, summary.Frozen)
	}
}

// TestWithdrawRejectUnfreezes 管理员拒绝解冻; 已终态的单不能再审。
func TestWithdrawRejectUnfreezes(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "reject", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, false, "账户不符", 1, "admin"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "", 1, "admin"); err == nil {
		t.Fatal("rejected order must not be approvable")
	}
	summary, _ := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Available != 100 || summary.Frozen != 0 {
		t.Fatalf("available=%v frozen=%v", summary.Available, summary.Frozen)
	}
}

// TestWithdrawRollbackRestoresPool 打款回滚: 已提转回冻结, 可提不变。
func TestWithdrawRollbackRestoresPool(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "rollback", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX001", "", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := WithdrawRollback(context.Background(), order.ID, "admin"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := WithdrawRollback(context.Background(), order.ID, "admin"); err == nil {
		t.Fatal("double rollback must be rejected")
	}
	summary, _ := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Available != 40 || summary.Frozen != 60 || summary.Withdrawn != 0 {
		t.Fatalf("available=%v frozen=%v withdrawn=%v, want 40/60/0", summary.Available, summary.Frozen, summary.Withdrawn)
	}
}

// TestWithdrawRulesMinAndFee 低于最低额拒绝; 手续费取 max(最低, 金额×费率)。
func TestWithdrawRulesMinAndFee(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "rules", 200)
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 30)); err == nil {
		t.Fatal("below minimum must be rejected")
	}
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// 100×1% = 1 与最低费 1 相等 → fee 1。
	if order.Fee != 1 {
		t.Fatalf("fee=%v, want 1", order.Fee)
	}
	if err := WithdrawReview(context.Background(), order.ID, false, "", 1, "admin"); err != nil {
		t.Fatal(err)
	}
	// 大额按费率: 200×1% = 2 > 最低费 1。
	big, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 200))
	if err != nil {
		t.Fatalf("apply big: %v", err)
	}
	if big.Fee != 2 {
		t.Fatalf("fee=%v, want 2", big.Fee)
	}
}

// TestWithdrawDailyCountLimit 单日笔数上限。
func TestWithdrawDailyCountLimit(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "daily", 200)
	if err := SettingUpsert(model.SettingKeyWithdrawalDailyCountLimit, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60)); err == nil {
		t.Fatal("second apply must hit the daily count limit")
	}
}

// TestWithdrawAccountLockDays 锁定期内换收款账户拒绝, 同账户不受限。
func TestWithdrawAccountLockDays(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "account-lock", 200)
	if err := SettingUpsert(model.SettingKeyWithdrawalAccountLockDays, "3"); err != nil {
		t.Fatal(err)
	}
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	other := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	other.AccountNo = "6222999988887777"
	if _, err := WithdrawApply(context.Background(), other); err == nil {
		t.Fatal("changed account must be rejected inside the lock window")
	}
	same := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	if _, err := WithdrawApply(context.Background(), same); err != nil {
		t.Fatalf("same account must be allowed: %v", err)
	}
	_ = order
}

// TestWithdrawCooldownBlocksImmature 冷却期内的新收益不可提。
func TestWithdrawCooldownBlocksImmature(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "cooldown", 0)
	// 直接写一条"刚产生"的收益记录, 收益池同步走结算口径。
	if err := db.GetDB().Create(&model.BillingRecord{
		OwnerID: reseller.ID, OwnerRevenue: 100, UserCost: 100,
		CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE users SET total_revenue = 100 WHERE id = ?", reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyWithdrawalCooldownDays, "7"); err != nil {
		t.Fatal(err)
	}
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60)); err == nil {
		t.Fatal("immature revenue must not be withdrawable")
	}
	summary, _ := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if summary.Available != 0 {
		t.Fatalf("available=%v, want 0 inside cooldown", summary.Available)
	}
	// 收益结算时间改成 8 天前 → 成熟可提。
	past := time.Now().AddDate(0, 0, -8)
	if err := db.GetDB().Model(&model.BillingRecord{}).Where("owner_id = ?", reseller.ID).Update("created_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60)); err != nil {
		t.Fatalf("matured revenue must be withdrawable: %v", err)
	}
}

// TestWithdrawCooldownExcludesRefundedRevenue 冷却期口径排除已退款账单:
// 退款批准时 total_revenue 已冲回, 若退款行仍计进 matured, 它会把成熟收益顶到收益池上沿,
// 冷却中的新收入就跟着被提前放出来(提现先于成熟)。
func TestWithdrawCooldownExcludesRefundedRevenue(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "cooldown-refund", 0)
	if err := SettingUpsert(model.SettingKeyWithdrawalCooldownDays, "7"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -10)
	rows := []model.BillingRecord{
		// 10 天前的两笔: 一笔仍是 settled(100), 一笔已退款(200, 收入已冲回)。
		{OwnerID: reseller.ID, OwnerRevenue: 100, UserCost: 120, PlatformRevenue: 20, Status: model.BillingStatusSettled, CreatedAt: old},
		{OwnerID: reseller.ID, OwnerRevenue: 200, UserCost: 250, PlatformRevenue: 50, Status: model.BillingStatusRefunded, CreatedAt: old},
		// 今天新产生的 60 还在冷却期。
		{OwnerID: reseller.ID, OwnerRevenue: 60, UserCost: 75, PlatformRevenue: 15, Status: model.BillingStatusSettled, CreatedAt: time.Now()},
	}
	for i := range rows {
		if err := db.GetDB().Create(&rows[i]).Error; err != nil {
			t.Fatalf("create billing record: %v", err)
		}
	}
	// 收益池同步退款冲回后的口径: 100(成熟) + 60(冷却中)。
	if err := db.GetDB().Exec("UPDATE users SET total_revenue = 160 WHERE id = ?", reseller.ID).Error; err != nil {
		t.Fatal(err)
	}

	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Revenue != 160 || summary.Available != 100 {
		t.Fatalf("revenue=%v available=%v, want 160/100 (成熟收益只算未退款的冷却期外账单)", summary.Revenue, summary.Available)
	}
	// 110 > 成熟 100: 冷却中的 60 不能被"退款行虚高的 matured"提前放出。
	if _, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 110)); err == nil {
		t.Fatal("withdrawal beyond matured revenue must be rejected")
	}
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("matured revenue must be withdrawable: %v", err)
	}
	if order.Amount != 100 {
		t.Fatalf("amount=%v, want 100", order.Amount)
	}
}

// TestWithdrawMaskAccount 账号脱敏只露尾 4 位。
func TestWithdrawMaskAccount(t *testing.T) {
	if got := model.MaskAccount("6222021234567890"); got != "************7890" {
		t.Fatalf("masked = %s", got)
	}
	if got := model.MaskAccount("1234"); got != "1234" {
		t.Fatalf("short masked = %s", got)
	}
}

// TestWithdrawApplyPersistsRateProvenance 提现单把汇率依据一并落库, 旧行为(CNY 静态价)不变。
func TestWithdrawApplyPersistsRateProvenance(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "provenance", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 60))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if order.Currency != "CNY" || order.Rate != 7 || order.RateDay != "" || order.RateSource != "static" {
		t.Fatalf("currency=%q rate=%v day=%q source=%q, want CNY/7/empty/static", order.Currency, order.Rate, order.RateDay, order.RateSource)
	}
}

// TestWithdrawApplyPayoutCurrencySnapshot 非默认到账币种按 FX 快照定格; 非法币种拒绝。
func TestWithdrawApplyPayoutCurrencySnapshot(t *testing.T) {
	// 与 withdrawalTestSetup 共用同一个测试库: fxRateTestDB 会另开库把用户行隔离丢掉。
	reseller, _ := withdrawalTestSetup(t, "payout", 100)
	FxRateCacheReset() // 清进程内快照, 让牌价读取走本测试库
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: "USD", To: "JPY", Day: day, Rate: 150, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = " jpy "
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply jpy: %v", err)
	}
	if order.Currency != "JPY" || order.Rate != 150 || order.ActualCents != int64(math.Round(order.Actual*150*100)) {
		t.Fatalf("currency=%q rate=%v cents=%v actual=%v, want JPY snapshot", order.Currency, order.Rate, order.ActualCents, order.Actual)
	}
	if order.RateDay != day || order.RateSource == "" || order.RateID == nil {
		t.Fatalf("day=%q source=%q id=%v, want snapshot provenance pinned", order.RateDay, order.RateSource, order.RateID)
	}
	// RateID 必须精确指回当日 JPY 牌价行; source 是"最近一次成功刷新源", 不必等于行 source。
	var seededRow model.FxRate
	if err := db.GetDB().First(&seededRow, *order.RateID).Error; err != nil || seededRow.To != "JPY" || seededRow.Day != day {
		t.Fatalf("rate id %v -> row(to=%s day=%s) err=%v, want seeded JPY row", *order.RateID, seededRow.To, seededRow.Day, err)
	}
	bad := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	bad.PayoutCurrency = "BRL"
	if _, err := WithdrawApply(context.Background(), bad); err == nil {
		t.Fatal("unsupported payout currency must be rejected")
	}
}

// TestWithdrawSummaryCarriesPoolCurrency 提现口径必须自报「这些数字是什么币种」。
//
// 为什么单独立一条用例: WithdrawSummary 里的 Available/Frozen/Withdrawn/Revenue 全是收益池
// 口径的裸数字, 而收益池的唯一记账币种是 model.LedgerCurrency = USD(internal/model/currency.go)。
// 同结构体里另有一个 currency 字段, 但它是 **rate 的目标币种**(1 USD 折多少 CNY), 语义完全不同 ——
// 前端拿它标注金额就会把美元读成人民币, 差约 7 倍。所以这里同时钉死两件事:
//  1. pool_currency 必须是 USD(金额单位);
//  2. currency 必须仍是 CNY(汇率快照的目标币种), 不许被"顺手改成 USD"。
//
// 断言走 JSON 而不是结构体字段: 这条契约是给前端消费的, 只有序列化后的键名才算数
// (字段名一改前端就读不到, 结构体断言看不出来)。
func TestWithdrawSummaryCarriesPoolCurrency(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "pool-currency", 100)

	summary, err := WithdrawSummaryGet(context.Background(), reseller.ID, model.RoleReseller)
	if err != nil {
		t.Fatalf("WithdrawSummaryGet: %v", err)
	}

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}

	// 金额单位: 必须是收益池记账币种。
	poolCurrency, ok := payload["pool_currency"]
	if !ok {
		t.Fatalf("JSON 缺 pool_currency 键; 前端无法标注可提金额单位, 用户会把 USD 读成 CNY; payload=%s", raw)
	}
	if poolCurrency != model.LedgerCurrency {
		t.Fatalf("pool_currency = %v, want %q (收益池记账币种)", poolCurrency, model.LedgerCurrency)
	}

	// 汇率快照目标币种: 存量口径不变, 不能被改成 USD(否则 rate 的单位失去意义)。
	if payload["currency"] != "CNY" {
		t.Fatalf("currency = %v, want CNY (rate 的目标币种, 不是金额币种; 改成 USD 会把汇率快照口径改坏)", payload["currency"])
	}
	rate, ok := payload["rate"].(float64)
	if !ok || rate != 7 {
		t.Fatalf("rate = %v, want 7 (1 USD = 7 CNY 的静态设置价)", payload["rate"])
	}

	// 数值语义一条不动: 只有币种标注是新增的。
	if summary.Available != 100 || summary.Revenue != 100 || summary.Frozen != 0 || summary.Withdrawn != 0 {
		t.Fatalf("金额语义被改动: %+v", summary)
	}

	// 可选到账币种白名单不含 USD: 收益池是 USD 口径, 打款换币由 actual_currency 承担。
	for _, code := range summary.SupportedCurrencies {
		if code == model.LedgerCurrency {
			t.Fatalf("supported_currencies 不该含账面币种 %s(打款换币由 actual_currency 承担): %v", model.LedgerCurrency, summary.SupportedCurrencies)
		}
	}
}

// TestWithdrawApplyDefaultsPayoutCurrencyToCNY 省略 payout_currency 必须落到 CNY(存量默认)。
//
// 修前行为: PayoutCurrency 为空时 model.NormalizeCurrency 返回的是账面币种 USD(它自己的语义是
// "空=USD", 见 internal/model/currency.go), 于是下面那行 `if payout == "" { payout = "CNY" }`
// 成了死代码 —— 单据被记成 Currency="USD"、rate=1 的快照, 而 handlers/withdrawal.go 的
// JSON tag 注释与函数注释都宣称"缺省 CNY"。快照口径一旦错, 下游按 currency 判
// 「是否 CNY 到账」「旧行/新行」的地方全部误判, mark-paid 的流水文案也会印成 "USD 59"。
//
// 这里钉死四件事: currency=CNY、rate 取 CNY 静态价(不是恒等 1)、actual_cents 按 CNY 折算、
// rate_source 是 static。只断言结构体不够 —— 关键在 rate 不是 1(1 恰好是"USD 恒等"的特征值)。
func TestWithdrawApplyDefaultsPayoutCurrencyToCNY(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "default-payout", 100)
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = "" // 省略到账币种: 老调用方/第三方脚本不发这个字段
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply with empty payout currency: %v", err)
	}
	if order.Currency != "CNY" {
		t.Fatalf("currency = %q, want CNY (省略 payout_currency 的存量默认)", order.Currency)
	}
	if order.Rate != 7 {
		t.Fatalf("rate = %v, want 7 (CNY 静态牌价, 不是 USD 恒等 1)", order.Rate)
	}
	if want := int64(math.Round(order.Actual * 7 * 100)); order.ActualCents != want {
		t.Fatalf("actual_cents = %d, want %d (按 CNY 牌价折算的分)", order.ActualCents, want)
	}
	if order.RateSource != "static" {
		t.Fatalf("rate_source = %q, want static", order.RateSource)
	}
}

// TestWithdrawApplyExplicitEURKeepsSnapshot 显式传非 CNY 币种时快照必须原样(防回归)。
// 与上一条互为对照: 默认回落只对"空串"生效, 不能顺手把显式币种也改成 CNY。
func TestWithdrawApplyExplicitEURKeepsSnapshot(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "explicit-eur", 100)
	FxRateCacheReset() // 清进程内快照, 让牌价读取走本测试库
	t.Cleanup(FxRateCacheReset)
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "EUR", Day: day, Rate: 0.9, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = "eur"
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply eur: %v", err)
	}
	if order.Currency != "EUR" {
		t.Fatalf("currency = %q, want EUR (显式币种不许被默认回落覆盖)", order.Currency)
	}
	if order.Rate != 0.9 {
		t.Fatalf("rate = %v, want 0.9 (EUR 牌价)", order.Rate)
	}
	if want := int64(math.Round(order.Actual * 0.9 * 100)); order.ActualCents != want {
		t.Fatalf("actual_cents = %d, want %d", order.ActualCents, want)
	}
}

// withdrawNotifContent 等异步通知落库后返回指定 related_id 的通知正文。
// 通知走 notifyDispatchGo 的独立协程: 不 drain 就查库会读到空, 且迟到的写可能落到下一个用例的库。
func withdrawNotifContent(t *testing.T, userID uint, relatedID uint64) string {
	t.Helper()
	NotificationDispatchDrain()
	var notif model.Notification
	err := db.GetDB().Where("user_id = ? AND type = ? AND related_id = ?", userID, model.NotificationTypeWithdrawal, relatedID).
		Order("id DESC").First(&notif).Error
	if err != nil {
		t.Fatalf("expected withdrawal notification for #%d: %v", relatedID, err)
	}
	return notif.Content
}

// TestWithdrawReviewNotifiesAmountInLedgerCurrency 审核通过的通知不许把 USD 金额写成「元」。
//
// order.Amount 是收益池口径, 唯一记账币种是 model.LedgerCurrency = USD。旧文案
// 「金额 %.2f 元」让用户按人民币理解: 100 USD 的提现看起来像"100 元", 实际能换约 700 元。
// 断言落在通知正文(用户真正看到的那一处), 不是结构体字段。
func TestWithdrawReviewNotifiesAmountInLedgerCurrency(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "notify-currency", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatalf("review: %v", err)
	}
	content := withdrawNotifContent(t, reseller.ID, uint64(order.ID))
	if !strings.Contains(content, model.LedgerCurrency) {
		t.Fatalf("通知正文没有标注金额币种 %s, 用户会把 USD 读成人民币: %q", model.LedgerCurrency, content)
	}
	if strings.Contains(content, "元") {
		t.Fatalf("通知正文仍在用人民币单位「元」标注 USD 金额: %q", content)
	}
	// 金额数值本身不许被顺手改掉(只是加币种标注)。
	if !strings.Contains(content, fmt.Sprintf("%.2f", order.Amount)) {
		t.Fatalf("通知正文丢了金额 %v: %q", order.Amount, content)
	}
}

// TestWithdrawTimeoutRevokeNotifiesAmountInLedgerCurrency 超时撤销的通知同口径修正。
func TestWithdrawTimeoutRevokeNotifiesAmountInLedgerCurrency(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "notify-revoke-currency", 100)
	order, err := WithdrawApply(context.Background(), withdrawalSpec(reseller.ID, model.RoleReseller, 100))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := WithdrawReview(context.Background(), order.ID, true, "ok", 1, "admin"); err != nil {
		t.Fatalf("review: %v", err)
	}
	// 通知按「类型 + 未读 + 1 小时窗口」去重: 先把审核通过那条标成已读, 否则超时撤销那条会被
	// 静默吞掉(去重是既有语义, 与这里要验的"文案币种"无关), 查到的就永远是审核那条。
	NotificationDispatchDrain()
	if err := db.GetDB().Model(&model.Notification{}).
		Where("user_id = ? AND type = ?", reseller.ID, model.NotificationTypeWithdrawal).
		Update("status", model.NotificationStatusRead).Error; err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-WithdrawApproveTimeout - time.Hour)
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).Update("reviewed_at", stale).Error; err != nil {
		t.Fatal(err)
	}
	revoked, err := WithdrawTimeoutRevoke(context.Background())
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked=%d, want 1", revoked)
	}
	content := withdrawNotifContent(t, reseller.ID, uint64(order.ID))
	if !strings.Contains(content, model.LedgerCurrency) {
		t.Fatalf("超时撤销通知没有标注金额币种 %s: %q", model.LedgerCurrency, content)
	}
	if strings.Contains(content, "元") {
		t.Fatalf("超时撤销通知仍在用人民币单位「元」标注 USD 金额: %q", content)
	}
	if !strings.Contains(content, fmt.Sprintf("%.2f", order.Amount)) {
		t.Fatalf("超时撤销通知丢了金额 %v: %q", order.Amount, content)
	}
}
