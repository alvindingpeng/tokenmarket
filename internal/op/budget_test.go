package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// budgetTestDB 临时库。CheckUserBudget 读 db.GetDB(), 必须走 InitDB。
func budgetTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "budget.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// block 档核心语义: 周期内已花费达到上限即拦截, 未达到放行; 周期窗随时间自然滚动。
// 账目口径与 max_cost 兜底一致: billing_records.user_cost, 只计 settled。
func TestCheckUserBudgetBlocksOverSpend(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()
	today := time.Now()
	if _, err := BudgetCreate(ctx, model.SpendBudget{
		UserID: 12, Period: model.BudgetPeriodDay, Action: model.BudgetActionBlock,
		LimitAmount: 50, WarnRatio: 80,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	seedBilling(t, 12, 0, 30, model.BillingStatusSettled, today)
	if err := CheckUserBudget(ctx, 12, 0); err != nil {
		t.Fatalf("under limit must pass: %v", err)
	}
	seedBilling(t, 12, 0, 25, model.BillingStatusSettled, today)
	err := CheckUserBudget(ctx, 12, 0)
	if _, ok := err.(model.BudgetExceeded); !ok {
		t.Fatalf("over limit must be blocked: got %v", err)
	}
}

// warn 档: 超限也不拦截, 只发通知(去重窗口挡住后续重复)。
func TestCheckUserBudgetWarnDoesNotBlock(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := BudgetCreate(ctx, model.SpendBudget{
		UserID: 21, Period: model.BudgetPeriodDay, Action: model.BudgetActionWarn,
		LimitAmount: 10, WarnRatio: 80,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	seedBilling(t, 21, 0, 99, model.BillingStatusSettled, now)
	if err := CheckUserBudget(ctx, 21, 0); err != nil {
		t.Fatalf("warn action must never block: %v", err)
	}
}

// 周期窗口: 昨天的账不计入今天的日预算; 月预算包含跨日的整个自然月。
func TestCheckUserBudgetPeriodWindow(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := BudgetCreate(ctx, model.SpendBudget{
		UserID: 30, Period: model.BudgetPeriodDay, Action: model.BudgetActionBlock,
		LimitAmount: 10, WarnRatio: 80,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	seedBilling(t, 30, 0, 100, model.BillingStatusSettled, now.AddDate(0, 0, -1))
	if err := CheckUserBudget(ctx, 30, 0); err != nil {
		t.Fatalf("yesterday's spend must not count into today's budget: %v", err)
	}
}

// Key 级预算: 只按 api_key_id 聚合, 不受同一用户其他 Key 的花费影响。
func TestCheckUserBudgetAPIKeyScope(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := BudgetCreate(ctx, model.SpendBudget{
		APIKeyID: 7, Period: model.BudgetPeriodMonth, Action: model.BudgetActionBlock,
		LimitAmount: 20, WarnRatio: 80,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	seedBilling(t, 0, 7, 25, model.BillingStatusSettled, now)
	if err := CheckUserBudget(ctx, 0, 7); err == nil {
		t.Fatal("key budget must block when key overspends")
	}
	if err := CheckUserBudget(ctx, 0, 8); err != nil {
		t.Fatalf("other keys must not be affected: %v", err)
	}
}

// 创建校验: 重复预算、非法周期/行为、非法比例都拦下。
func TestBudgetCreateValidation(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()
	if _, err := BudgetCreate(ctx, model.SpendBudget{Period: "week", Action: "warn", LimitAmount: 1, WarnRatio: 80}); err == nil {
		t.Fatal("invalid period must be rejected")
	}
	if _, err := BudgetCreate(ctx, model.SpendBudget{UserID: 1, Period: "day", Action: "nope", LimitAmount: 1, WarnRatio: 80}); err == nil {
		t.Fatal("invalid action must be rejected")
	}
	if _, err := BudgetCreate(ctx, model.SpendBudget{UserID: 1, Period: "day", Action: "warn", LimitAmount: 0, WarnRatio: 80}); err == nil {
		t.Fatal("zero limit must be rejected")
	}
	if _, err := BudgetCreate(ctx, model.SpendBudget{UserID: 1, Period: "day", Action: "warn", LimitAmount: 1, WarnRatio: 300}); err == nil {
		t.Fatal("warn_ratio out of range must be rejected")
	}
	if _, err := BudgetCreate(ctx, model.SpendBudget{Period: "day", Action: "warn", LimitAmount: 1, WarnRatio: 80}); err == nil {
		t.Fatal("scope-less budget must be rejected")
	}
	// 第一条合法; 同 scope 同周期重复被拒。
	if _, err := BudgetCreate(ctx, model.SpendBudget{UserID: 55, Period: "day", Action: "warn", LimitAmount: 5, WarnRatio: 80}); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
	if _, err := BudgetCreate(ctx, model.SpendBudget{UserID: 55, Period: "day", Action: "block", LimitAmount: 9, WarnRatio: 50}); err == nil {
		t.Fatal("duplicate scope+period must be rejected")
	}
}

// seedBilling 直接插 billing_records: 判定只依赖这张表, 不必跑完整结算链路。
func seedBilling(t *testing.T, userID uint, apiKeyID int, cost float64, status string, at time.Time) {
	t.Helper()
	if err := db.GetDB().Create(&model.BillingRecord{
		UserID: userID, APIKeyID: apiKeyID, UserCost: cost, Status: status, CreatedAt: at,
	}).Error; err != nil {
		t.Fatalf("seed billing: %v", err)
	}
}

// 用户侧预算: scope 锁定本人; 不能给自己建 key 级; 删除/更新碰不到别人的。
func TestMyBudgetScopeIsolation(t *testing.T) {
	budgetTestDB(t)
	ctx := context.Background()

	if _, err := MyBudgetCreate(ctx, 7, model.SpendBudget{
		Period: model.BudgetPeriodMonth, Action: model.BudgetActionWarn, LimitAmount: 10, WarnRatio: 50,
	}); err != nil {
		t.Fatalf("create my budget: %v", err)
	}
	// key 级预算是管理员分配 Key 池的手段, 用户自建必须被拒。
	if _, err := MyBudgetCreate(ctx, 7, model.SpendBudget{
		APIKeyID: 3, Period: model.BudgetPeriodDay, Action: model.BudgetActionWarn, LimitAmount: 10, WarnRatio: 50,
	}); err == nil {
		t.Fatal("api_key budget must be rejected for self-service")
	}

	items, err := MyBudgetList(ctx, 7)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v items=%d", err, len(items))
	}
	// 用量视图: 无消费时 spent=0, window_end 是周期窗口上界。
	if items[0].Spent != 0 || items[0].WindowEnd == "" {
		t.Fatalf("view: spent=%v window_end=%q", items[0].Spent, items[0].WindowEnd)
	}

	seedBilling(t, 7, 0, 4, model.BillingStatusSettled, time.Now())
	items, err = MyBudgetList(ctx, 7)
	if err != nil {
		t.Fatalf("list after spend: %v", err)
	}
	if items[0].Spent < 3.99 || items[0].Spent > 4.01 || items[0].RatioPct != 40 {
		t.Fatalf("view after spend: spent=%v ratio=%d", items[0].Spent, items[0].RatioPct)
	}

	// 隔离: 用户 8 建一条, 用户 7 改不动删不掉。
	if _, err := MyBudgetCreate(ctx, 8, model.SpendBudget{
		Period: model.BudgetPeriodDay, Action: model.BudgetActionWarn, LimitAmount: 5, WarnRatio: 50,
	}); err != nil {
		t.Fatalf("create user8: %v", err)
	}
	if _, err := MyBudgetUpdate(ctx, 7, items[0].ID+1, model.BudgetPeriodDay, model.BudgetActionBlock, 99, 50, true); err == nil {
		t.Fatal("user7 must not update user8 budget")
	}
	if err := MyBudgetDelete(ctx, 7, items[0].ID+1); err == nil {
		t.Fatal("user7 must not delete user8 budget")
	}
	// 自己的正常路径。
	if _, err := MyBudgetUpdate(ctx, 7, items[0].ID, model.BudgetPeriodMonth, model.BudgetActionWarn, 20, 60, false); err != nil {
		t.Fatalf("self update: %v", err)
	}
	if err := MyBudgetDelete(ctx, 7, items[0].ID); err != nil {
		t.Fatalf("self delete: %v", err)
	}
}
