package op

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// redemptionTestSetup 初始化隔离测试库并建一个待兑换用户。
func redemptionTestSetup(t *testing.T, username string) model.User {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "redemption.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 防爆破计数是进程级内存态, 测试间清零避免用户 id 复用造成相互污染。
	redeemGuard.Lock()
	redeemGuard.fails = map[string]int{}
	redeemGuard.locks = map[string]time.Time{}
	redeemGuard.Unlock()
	user := model.User{Username: username, Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

// redemptionTestBalance 读取库内余额口径。
func redemptionTestBalance(t *testing.T, userID uint) (float64, float64) {
	t.Helper()
	var fresh model.User
	if err := db.GetDB().First(&fresh, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return fresh.Balance, fresh.TotalRecharged
}

// TestRedemptionConcurrentRedeemOnce 并发重复兑换同一张码:至多一次成功, 余额/累计/流水三者恒等。
func TestRedemptionConcurrentRedeemOnce(t *testing.T) {
	user := redemptionTestSetup(t, "concurrent")
	_, codes, err := RedemptionBatchCreate(context.Background(), RedemptionBatchSpec{
		Name: "concurrent", Credits: 5, Count: 1, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	code := codes[0].Code

	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := RedemptionRedeem(context.Background(), user.ID, code, fmt.Sprintf("10.0.0.%d", index))
			results[index] = err
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1 (errors: %v)", successes, results)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 5 || recharged != 5 {
		t.Fatalf("balance=%v recharged=%v, want 5 and 5", balance, recharged)
	}
	var ledgerCount int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindRedeem).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("redemption ledger rows = %d, want 1", ledgerCount)
	}
}

// TestRedemptionUnlimitedPerUser 默认口径: 用户兑换张数不限, 同一人可连续兑换多张。
func TestRedemptionUnlimitedPerUser(t *testing.T) {
	user := redemptionTestSetup(t, "unlimited")
	_, codes, err := RedemptionBatchCreate(context.Background(), RedemptionBatchSpec{
		Name: "unlimited", Credits: 2, Count: 3, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	for index, entry := range codes {
		if _, err := RedemptionRedeem(context.Background(), user.ID, entry.Code, "10.1.0.1"); err != nil {
			t.Fatalf("redeem #%d: %v", index+1, err)
		}
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 6 || recharged != 6 {
		t.Fatalf("balance=%v recharged=%v, want 6 and 6", balance, recharged)
	}
}

// TestRedemptionPerUserLimit 配置了单用户上限时按上限拦截; 未用张数可兑。
func TestRedemptionPerUserLimit(t *testing.T) {
	user := redemptionTestSetup(t, "limited")
	_, codes, err := RedemptionBatchCreate(context.Background(), RedemptionBatchSpec{
		Name: "limited", Credits: 3, Count: 2, PerUserLimit: 1, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := RedemptionRedeem(context.Background(), user.ID, codes[0].Code, "10.2.0.1"); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := RedemptionRedeem(context.Background(), user.ID, codes[1].Code, "10.2.0.1"); err == nil {
		t.Fatal("second redeem should hit per user limit")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 3 {
		t.Fatalf("balance=%v, want 3", balance)
	}
}

// TestRedeemGuardLocksAfterFailures 连续错码达到上限后锁定, 之后连正确码也拒绝。
func TestRedeemGuardLocksAfterFailures(t *testing.T) {
	user := redemptionTestSetup(t, "guard")
	_, codes, err := RedemptionBatchCreate(context.Background(), RedemptionBatchSpec{
		Name: "guard", Credits: 1, Count: 1, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	const clientIP = "10.3.0.1"
	for i := 0; i < redeemMaxFails; i++ {
		if _, err := RedemptionRedeem(context.Background(), user.ID, fmt.Sprintf("WRONG%02d", i), clientIP); err == nil {
			t.Fatal("wrong code should fail")
		}
	}
	if _, err := RedemptionRedeem(context.Background(), user.ID, codes[0].Code, clientIP); err != ErrRedeemLocked {
		t.Fatalf("expected ErrRedeemLocked, got %v", err)
	}
	// 同一用户换来源 IP 仍被锁(锁的是账户不是来源)。
	if _, err := RedemptionRedeem(context.Background(), user.ID, codes[0].Code, "10.3.0.2"); err != ErrRedeemLocked {
		t.Fatalf("same user should stay locked, got %v", err)
	}
	// 其他用户不受牵连。
	other := model.User{Username: "guard-other", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&other).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if _, err := RedemptionRedeem(context.Background(), other.ID, codes[0].Code, "10.3.0.2"); err != nil {
		t.Fatalf("other user should redeem: %v", err)
	}
}

// TestRedemptionCodeDisableOnce 禁用后不可再兑, 已兑的不允许翻改。
func TestRedemptionCodeDisableOnce(t *testing.T) {
	user := redemptionTestSetup(t, "disable")
	_, codes, err := RedemptionBatchCreate(context.Background(), RedemptionBatchSpec{
		Name: "disable", Credits: 4, Count: 1, CreatedBy: 1,
	})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if err := RedemptionCodeDisable(context.Background(), codes[0].ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := RedemptionRedeem(context.Background(), user.ID, codes[0].Code, "10.4.0.1"); err == nil {
		t.Fatal("disabled code should not redeem")
	}
	if err := RedemptionCodeDisable(context.Background(), codes[0].ID); err == nil {
		t.Fatal("double disable should fail")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0", balance)
	}
}
