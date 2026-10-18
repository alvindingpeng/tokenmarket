package op

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/pay"
)

// rechargeTestSetup 初始化隔离测试库并配置一套测试用支付参数。
func rechargeTestSetup(t *testing.T, username string) model.User {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "recharge.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	for key, value := range map[model.SettingKey]string{
		model.SettingKeyEpayGatewayURL:       "https://pay.example.com",
		model.SettingKeyEpayPID:              "1001",
		model.SettingKeyEpayKey:              "testkey",
		model.SettingKeyRechargeRate:         "7.0",
		model.SettingKeyRechargeMinCNY:       "1",
		model.SettingKeyRechargeBonusEnabled: "false",
		model.SettingKeyRechargeBonusRules:   "",
	} {
		if err := SettingUpsert(key, value); err != nil {
			t.Fatalf("seed setting %s: %v", key, err)
		}
	}
	user := model.User{Username: username, Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

// rechargeTestQuery 按易支付格式构造签名回调参数。
func rechargeTestQuery(orderNo, tradeNo, money string) url.Values {
	params := map[string]string{
		"pid":          "1001",
		"trade_no":     tradeNo,
		"out_trade_no": orderNo,
		"trade_status": "TRADE_SUCCESS",
		"money":        money,
	}
	params["sign"] = pay.EpaySign(params, "testkey")
	params["sign_type"] = "MD5"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	return query
}

// TestRechargeQuoteAndBonus 汇率换算与满赠规则命中最高档。
func TestRechargeQuoteAndBonus(t *testing.T) {
	rechargeTestSetup(t, "quote")
	credits, bonus, rate, err := RechargeQuote(context.Background(), 7000) // 70 元
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if rate != 7.0 || credits != 10 {
		t.Fatalf("rate=%v credits=%v, want 7 and 10", rate, credits)
	}
	if bonus != 0 {
		t.Fatalf("bonus=%v, want 0 with bonus disabled", bonus)
	}
	if err := SettingUpsert(model.SettingKeyRechargeBonusEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyRechargeBonusRules, "50:5,100:10"); err != nil {
		t.Fatal(err)
	}
	credits, bonus, _, err = RechargeQuote(context.Background(), 7000) // 70 元命中 50:5
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if credits != 10 || bonus != 0.5 {
		t.Fatalf("credits=%v bonus=%v, want 10 and 0.5", credits, bonus)
	}
}

// TestRechargeCreateNotConfigured 未配置商户参数时拒绝下单, 优雅降级。
func TestRechargeCreateNotConfigured(t *testing.T) {
	rechargeTestSetup(t, "unconfigured")
	if err := SettingUpsert(model.SettingKeyEpayGatewayURL, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RechargeCreate(context.Background(), 1, 1000, "epay", "alipay", "n", "r"); err == nil {
		t.Fatal("unconfigured gateway must reject")
	}
}

// TestRechargeNotifyIdempotent 回调重复投递只入账一次。
func TestRechargeNotifyIdempotent(t *testing.T) {
	user := rechargeTestSetup(t, "idempotent")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	query := rechargeTestQuery(order.OrderNo, "T100", "70.00")
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: query}); err != nil {
		t.Fatalf("notify #1: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: query}); err != nil {
		t.Fatalf("notify #2 (重复投递应幂等成功): %v", err)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 10 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 10 and 10", balance, recharged)
	}
	var fresh model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", order.OrderNo).First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.RechargeStatusPaid || fresh.TradeNo == nil || *fresh.TradeNo != "T100" {
		t.Fatalf("order status=%s trade=%v", fresh.Status, fresh.TradeNo)
	}
}

// TestRechargeNotifyBonusLedger 满赠入账单独记流水且不进累计充值口径。
func TestRechargeNotifyBonusLedger(t *testing.T) {
	user := rechargeTestSetup(t, "bonus")
	if err := SettingUpsert(model.SettingKeyRechargeBonusEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyRechargeBonusRules, "50:10"); err != nil {
		t.Fatal(err)
	}
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T200", "70.00")}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 11 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 11 and 10 (赠送 1 不计累计充值)", balance, recharged)
	}
	var bonusRows int64
	if err := db.GetDB().Model(&model.BillingLedger{}).Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindBonus).Count(&bonusRows).Error; err != nil {
		t.Fatal(err)
	}
	if bonusRows != 1 {
		t.Fatalf("bonus ledger rows=%d, want 1", bonusRows)
	}
}

// TestRechargeNotifyAmountMismatch 金额不符拒绝入账。
func TestRechargeNotifyAmountMismatch(t *testing.T) {
	user := rechargeTestSetup(t, "mismatch")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T300", "0.01")}); err == nil {
		t.Fatal("amount mismatch must be rejected")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0", balance)
	}
}

// TestRechargeManualComplete 人工补单入账, 二次补单拒绝。
func TestRechargeManualComplete(t *testing.T) {
	user := rechargeTestSetup(t, "manual")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := RechargeManualComplete(context.Background(), order.OrderNo, 1, "admin"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := RechargeManualComplete(context.Background(), order.OrderNo, 1, "admin"); err == nil {
		t.Fatal("double complete must be rejected")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 10 {
		t.Fatalf("balance=%v, want 10", balance)
	}
}

// TestRechargeCloseExpired 超时订单自动关闭, 关闭后不能再入账。
func TestRechargeCloseExpired(t *testing.T) {
	user := rechargeTestSetup(t, "expired")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", order.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := RechargeCloseExpired(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T400", "70.00")}); err == nil {
		t.Fatal("expired order must not credit")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0", balance)
	}
}
