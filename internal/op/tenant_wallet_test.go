package op

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// tenantWalletSetup 隔离库 + 两个租户 + 各一个用户, 返回租户与用户。
func tenantWalletSetup(t *testing.T) (model.Tenant, model.User, model.Tenant, model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "tenant-wallet.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()

	tenants := []model.Tenant{
		{Name: "wallet-a", Code: "wallet-a", Status: model.TenantStatusActive},
		{Name: "wallet-b", Code: "wallet-b", Status: model.TenantStatusActive},
	}
	if err := db.GetDB().Create(&tenants).Error; err != nil {
		t.Fatalf("create tenants: %v", err)
	}
	userA, err := UserCreate("wa-user", "", "password123", model.RoleUser, model.StatusActive, tenants[0].ID)
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	userB, err := UserCreate("wb-user", "", "password123", model.RoleUser, model.StatusActive, tenants[1].ID)
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	for _, tenant := range tenants {
		if _, err := TenantWalletEnsure(tenant.ID); err != nil {
			t.Fatalf("ensure wallet: %v", err)
		}
	}
	return tenants[0], userA, tenants[1], userB
}

// setTenantBillingMode 切换租户资金闸门模式并同步缓存。
func setTenantBillingMode(t *testing.T, mode string) {
	t.Helper()
	// 用 Upsert 而非 SetString: 最小化 fixture 里没有种子设置行, SetString 只更新不创建。
	if err := SettingUpsert(model.SettingKeyTenantBillingMode, mode); err != nil {
		t.Fatalf("set billing mode: %v", err)
	}
	// settingCache 是包级缓存, 不随测试结束清空: 忘了复位会让 enforce 泄漏到别的测试,
	// 表现为"别的测试突然预扣失败" —— 距离真正的 bug 太远, 排查成本极高。
	t.Cleanup(func() {
		_ = SettingUpsert(model.SettingKeyTenantBillingMode, model.TenantBillingModeOff)
	})
}

// TestTenantWalletRechargeAccumulates 充值必须同时增加可用余额与累计充值, 并留一条流水。
func TestTenantWalletRechargeAccumulates(t *testing.T) {
	tenantA, _, _, _ := tenantWalletSetup(t)

	wallet, err := TenantWalletRecharge(context.Background(), tenantA.ID, 100, 1, "admin", "initial topup")
	if err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if wallet.Balance != 100 || wallet.TotalRecharged != 100 {
		t.Fatalf("wallet=%+v, want balance=100 total_recharged=100", wallet)
	}
	// 二次充值累加, 不是覆盖。
	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 50, 1, "admin", "second topup"); err != nil {
		t.Fatalf("second recharge: %v", err)
	}
	fresh, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Balance != 150 || fresh.TotalRecharged != 150 {
		t.Fatalf("wallet=%+v, want balance=150 total_recharged=150", fresh)
	}

	rows, total, err := TenantLedgerList(context.Background(), TenantLedgerQuery{TenantID: tenantA.ID})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("ledger rows=%d total=%d, want 2", len(rows), total)
	}
	for _, row := range rows {
		if row.Kind != model.TenantLedgerKindRecharge {
			t.Fatalf("ledger kind=%q, want recharge", row.Kind)
		}
	}
}

// TestTenantWalletRechargeRejectsInvalidAmounts 金额必须是正有限数, 否则账目会被污染。
func TestTenantWalletRechargeRejectsInvalidAmounts(t *testing.T) {
	tenantA, _, _, _ := tenantWalletSetup(t)

	cases := []float64{0, -1, -100}
	for _, amount := range cases {
		if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, amount, 1, "admin", ""); err == nil {
			t.Fatalf("recharge %.2f should be rejected", amount)
		}
	}
	// 拒绝后余额不能被改动。
	wallet, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 0 {
		t.Fatalf("balance=%v after rejected recharges, want 0", wallet.Balance)
	}
}

// TestTenantWalletAdjustCannotGoNegative 扣减调账不得把可用余额打成负数:
// 负余额会让后续预扣的"余额足够"判断永久失败, 客户既充不进也花不出。
func TestTenantWalletAdjustCannotGoNegative(t *testing.T) {
	tenantA, _, _, _ := tenantWalletSetup(t)

	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 10, 1, "admin", ""); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if _, err := TenantWalletAdjust(context.Background(), tenantA.ID, -20, 1, "admin", "over-refund"); err == nil {
		t.Fatal("adjustment below zero must be rejected")
	}
	wallet, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance != 10 {
		t.Fatalf("balance=%v, want 10 (unchanged)", wallet.Balance)
	}
	// 恰好吃到 0 是允许的。
	if _, err := TenantWalletAdjust(context.Background(), tenantA.ID, -10, 1, "admin", "full refund"); err != nil {
		t.Fatalf("adjust to zero: %v", err)
	}
}

// TestTenantBillingOffDoesNotBlockRequests 默认 off 模式必须不拦请求:
// 存量部署的默认租户钱包为 0, 一旦拦截就是整站停摆。
func TestTenantBillingOffDoesNotBlockRequests(t *testing.T) {
	tenantA, userA, _, _ := tenantWalletSetup(t)
	setTenantBillingMode(t, model.TenantBillingModeOff)

	// 用户自身余额充足, 租户余额为 0: off 模式下预扣必须成功。
	if err := UserSetBalance(userA.ID, 100, 1, "", "seed"); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if _, err := BillingReserve(userA.ID, 9001, 1); err != nil {
		t.Fatalf("reserve in off mode must succeed despite empty tenant wallet: %v", err)
	}
	// 账目仍然记录冻结额(记账不拦截)。
	wallet, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Frozen != 1 {
		t.Fatalf("frozen=%v, want 1 (still recorded in off mode)", wallet.Frozen)
	}
}

// TestTenantBillingEnforceBlocksWhenTenantBalanceLow enforce 模式下租户余额不足必须拒绝,
// 即使使用者本人余额充足 —— 平台授信是按租户给的。
func TestTenantBillingEnforceBlocksWhenTenantBalanceLow(t *testing.T) {
	_, userA, _, _ := tenantWalletSetup(t)
	setTenantBillingMode(t, model.TenantBillingModeEnforce)

	if err := UserSetBalance(userA.ID, 100, 1, "", "seed"); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	_, err := BillingReserve(userA.ID, 9002, 5)
	if err == nil {
		t.Fatal("reserve must fail when tenant balance is empty in enforce mode")
	}
	if !errors.Is(err, errTenantInsufficientBalance) {
		t.Fatalf("error=%v, want errTenantInsufficientBalance", err)
	}
}

// TestTenantBillingEnforceSettlesAgainstTenantWallet 结算必须扣减租户余额并累计支出。
func TestTenantBillingEnforceSettlesAgainstTenantWallet(t *testing.T) {
	tenantA, userA, _, _ := tenantWalletSetup(t)
	setTenantBillingMode(t, model.TenantBillingModeEnforce)

	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 50, 1, "admin", ""); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if err := UserSetBalance(userA.ID, 100, 1, "", "seed"); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}

	reservationID, err := BillingReserve(userA.ID, 9003, 2)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	record := &model.BillingRecord{
		RequestID: 9003,
		UserID:    userA.ID,
		ModelName: "test-model",
		UserCost:  1.5,
	}
	if err := billingSettleApply(context.Background(), reservationID, record); err != nil {
		t.Fatalf("settle: %v", err)
	}

	wallet, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 余额 50 - 1.5 = 48.5, 冻结额回到 0, 累计支出 1.5。
	if wallet.Balance != 48.5 || wallet.Frozen != 0 || wallet.TotalSpent != 1.5 {
		t.Fatalf("wallet=%+v, want balance=48.5 frozen=0 total_spent=1.5", wallet)
	}
}

// TestTenantWalletReleaseRestoresFrozen 未计费释放必须把冻结额原样退回。
func TestTenantWalletReleaseRestoresFrozen(t *testing.T) {
	tenantA, userA, _, _ := tenantWalletSetup(t)
	setTenantBillingMode(t, model.TenantBillingModeEnforce)

	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 50, 1, "admin", ""); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if err := UserSetBalance(userA.ID, 100, 1, "", "seed"); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}

	reservationID, err := BillingReserve(userA.ID, 9004, 3)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := BillingRelease(reservationID); err != nil {
		t.Fatalf("release: %v", err)
	}
	wallet, err := tenantWalletOf(db.GetDB(), tenantA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Frozen != 0 || wallet.Balance != 50 {
		t.Fatalf("wallet=%+v, want frozen=0 balance=50 (fully released)", wallet)
	}
}

// TestTenantWalletIsolatedAcrossTenants 一个租户的充值与消费不得影响另一个租户。
func TestTenantWalletIsolatedAcrossTenants(t *testing.T) {
	tenantA, userA, tenantB, _ := tenantWalletSetup(t)

	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 100, 1, "admin", ""); err != nil {
		t.Fatalf("recharge a: %v", err)
	}
	if err := UserSetBalance(userA.ID, 100, 1, "", "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := BillingReserve(userA.ID, 9005, 10); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	walletB, err := tenantWalletOf(db.GetDB(), tenantB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if walletB.Balance != 0 || walletB.Frozen != 0 {
		t.Fatalf("tenant B wallet=%+v, must be untouched", walletB)
	}
}

// TestTenantWalletEnsureIsIdempotent 重复初始化不得重置已有余额。
func TestTenantWalletEnsureIsIdempotent(t *testing.T) {
	tenantA, _, _, _ := tenantWalletSetup(t)

	if _, err := TenantWalletRecharge(context.Background(), tenantA.ID, 25, 1, "admin", ""); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	wallet, err := TenantWalletEnsure(tenantA.ID)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if wallet.Balance != 25 {
		t.Fatalf("balance=%v after ensure, want 25 (not reset)", wallet.Balance)
	}
}
