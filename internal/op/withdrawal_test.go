package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// withdrawalTestSetup 初始化隔离库: 建一个带收益池的渠道商与一个普通用户。
func withdrawalTestSetup(t *testing.T, username string, revenue float64) (model.User, model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "withdraw.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	for key, value := range map[model.SettingKey]string{
		model.SettingKeyRechargeRate:                 "7.0",
		model.SettingKeyWithdrawalMinAmount:          "50",
		model.SettingKeyWithdrawalFeeRate:            "0.01",
		model.SettingKeyWithdrawalFeeMin:             "1",
		model.SettingKeyWithdrawalCooldownDays:       "0",
		model.SettingKeyWithdrawalDailyCountLimit:    "0",
		model.SettingKeyWithdrawalDailyAmountLimit:   "0",
		model.SettingKeyWithdrawalAccountLockDays:    "0",
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
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX001", "admin"); err != nil {
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
	if err := WithdrawMarkPaid(context.Background(), order.ID, "TX001", "admin"); err != nil {
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

// TestWithdrawMaskAccount 账号脱敏只露尾 4 位。
func TestWithdrawMaskAccount(t *testing.T) {
	if got := model.MaskAccount("6222021234567890"); got != "************7890" {
		t.Fatalf("masked = %s", got)
	}
	if got := model.MaskAccount("1234"); got != "1234" {
		t.Fatalf("short masked = %s", got)
	}
}
