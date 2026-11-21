package op

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/pay"
)

// rechargeTestSetup 初始化隔离测试库并配置一套测试用支付参数。
func rechargeTestSetup(t *testing.T, username string) model.User {
	t.Helper()
	// 本 setup 原先完全不关库: 通知协程会与下一个用例的新库打架, 现在统一走 opTestDBSetup。
	opTestDBSetup(t, "recharge.db")
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
	// 报价必须带汇率依据: CNY 走静态设置价, source=static 且无牌价日。
	day, source, err2 := func() (string, string, error) {
		_, _, _, d, src, _, err := RechargeQuoteDetail(context.Background(), 7000, "CNY")
		return d, src, err
	}()
	if err2 != nil {
		t.Fatalf("quote detail: %v", err2)
	}
	if day != "" || source != "static" {
		t.Fatalf("rate_day=%q rate_source=%q, want empty day and static source", day, source)
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

// TestRechargeCloseExpired 超时订单自动关闭; 关闭后"没有回调"就不该有任何入账。
// 注意关闭本身不等于放弃这笔钱: 第三方事后确认付款仍会补入账(见
// TestRechargeNotifyCreditsExpiredOrder), 这里断言的是"没人确认付款就不动余额"。
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
	if _, err := RechargeCloseExpired(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	var closed model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", order.OrderNo).First(&closed).Error; err != nil {
		t.Fatal(err)
	}
	if closed.Status != model.RechargeStatusExpired {
		t.Fatalf("status=%s, want expired", closed.Status)
	}
	// 没有回调 = 没人付款确认: 余额必须一分不动。
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0", balance)
	}
}

// TestRechargeQuoteUsesFxSnapshotForOtherCurrencies 非法币种在 Rate 层拒绝,
// FX 快照币种返回带 day/source 的牌价, 缺牌价时报错而不是按 1 折算。
func TestStaticRateProviderSupportsFxSnapshotCurrencies(t *testing.T) {
	fxRateTestDB(t)
	seedFxRate(t, "USD", "JPY", 150)
	rate, day, source, _, err := StaticRateProvider{}.RateDetail(context.Background(), " jpy ")
	if err != nil {
		t.Fatalf("rate detail: %v", err)
	}
	if rate != 150 || day == "" {
		t.Fatalf("rate=%v day=%q, want 150 with snapshot day", rate, day)
	}
	// 落库快照的 source 必须原样保留; Rate 层返回的是"最近一次成功刷新源", 不必与单行一致。
	var seeded model.FxRate
	if err := db.GetDB().Where("`to` = ?", "JPY").Order("day DESC").First(&seeded).Error; err != nil || seeded.Source != "test" {
		t.Fatalf("seeded fx source=%q err=%v, want test", seeded.Source, err)
	}
	provider := StaticRateProvider{}
	if _, _, _, _, brlErr := provider.RateDetail(context.Background(), "BRL"); brlErr == nil {
		t.Fatal("unsupported currency must be rejected")
	}
	FxRateCacheReset()
	// USD 恒等: rate=1, 不需要任何牌价。
	rate, _, source, _, err = provider.RateDetail(context.Background(), "USD")
	if err != nil || rate != 1 || source != "identity" {
		t.Fatalf("usd identity: rate=%v source=%q err=%v", rate, source, err)
	}
}

// TestRechargeOrderPersistsRateProvenance 下单时把汇率与依据一并定格, 回调不再重算。
func TestRechargeOrderPersistsRateProvenance(t *testing.T) {
	user := rechargeTestSetup(t, "provenance")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if order.Rate != 7 || order.Currency != "CNY" || order.RateDay != "" || order.RateSource != "static" {
		t.Fatalf("order rate=%v currency=%q day=%q source=%q, want 7/CNY/empty/static", order.Rate, order.Currency, order.RateDay, order.RateSource)
	}
}

// TestRechargeNotifyCreditsExpiredOrder 钱优先: 订单已超时关闭但第三方确认收款, 必须入账。
//
// 这是"钱付了入不了账"的回归。"expired"只表示我们等回调等过了 TTL, 不代表用户没付;
// 若死守 pending, 用户的钱就卡在第三方, 只能走人工补单。
func TestRechargeNotifyCreditsExpiredOrder(t *testing.T) {
	user := rechargeTestSetup(t, "expired-credit")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 把单子真正跑成 expired: 收紧 expired_at 再跑关闭, 与线上到期路径完全一致。
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", order.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	closed, err := RechargeExpireRun(context.Background())
	if err != nil {
		t.Fatalf("expire run: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed=%d, want 1", closed)
	}
	var expired model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", order.OrderNo).First(&expired).Error; err != nil {
		t.Fatal(err)
	}
	if expired.Status != model.RechargeStatusExpired {
		t.Fatalf("status=%s, want expired before the callback", expired.Status)
	}
	// 第三方确认付款(金额正确): 必须入账。
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T500", "70.00")}); err != nil {
		t.Fatalf("notify on expired order must credit: %v", err)
	}
	var fresh model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", order.OrderNo).First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.RechargeStatusPaid || fresh.TradeNo == nil || *fresh.TradeNo != "T500" {
		t.Fatalf("status=%s trade=%v, want paid/T500", fresh.Status, fresh.TradeNo)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 10 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 10 and 10", balance, recharged)
	}
}

// TestRechargeNotifyExpiredStillChecksAmount 放宽状态不等于放宽金额:
// 过期单照样按渠道实付额核对, 金额不符仍然拒绝入账(资损防线不能因为"过期"被绕过)。
func TestRechargeNotifyExpiredStillChecksAmount(t *testing.T) {
	user := rechargeTestSetup(t, "expired-mismatch")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", order.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := RechargeExpireRun(context.Background()); err != nil {
		t.Fatalf("expire run: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T501", "0.01")}); err == nil {
		t.Fatal("amount mismatch on an expired order must still be rejected")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 0 {
		t.Fatalf("balance=%v, want 0", balance)
	}
	var fresh model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", order.OrderNo).First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.RechargeStatusExpired {
		t.Fatalf("status=%s, want expired (not credited)", fresh.Status)
	}
}

// TestRechargeNotifyExpiredIdempotent 过期补入账也只能入一次:
// 重复回调走既有 paid 幂等分支, 余额不再增加 —— 绝不允许同一单二次入账。
func TestRechargeNotifyExpiredIdempotent(t *testing.T) {
	user := rechargeTestSetup(t, "expired-idempotent")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", order.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := RechargeExpireRun(context.Background()); err != nil {
		t.Fatalf("expire run: %v", err)
	}
	query := rechargeTestQuery(order.OrderNo, "T502", "70.00")
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: query}); err != nil {
		t.Fatalf("notify #1: %v", err)
	}
	// 第二次: 订单已是 paid, 命中幂等分支(返回 nil) 且不再入账。
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: query}); err != nil {
		t.Fatalf("notify #2 must be idempotent: %v", err)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 10 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 10 and 10 (credited once)", balance, recharged)
	}
	var topupRows int64
	if err := db.GetDB().Model(&model.BillingLedger{}).
		Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindTopup).Count(&topupRows).Error; err != nil {
		t.Fatal(err)
	}
	if topupRows != 1 {
		t.Fatalf("topup ledger rows=%d, want exactly 1", topupRows)
	}
}

// TestRechargeManualCompleteAcceptsExpired 人工补单必须接受 expired:
// 管理员核对第三方账单确认收款后, 不该因为"单子已关闭"被迫改库。
func TestRechargeManualCompleteAcceptsExpired(t *testing.T) {
	user := rechargeTestSetup(t, "expired-manual")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", order.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := RechargeExpireRun(context.Background()); err != nil {
		t.Fatalf("expire run: %v", err)
	}
	if err := RechargeManualComplete(context.Background(), order.OrderNo, 1, "admin"); err != nil {
		t.Fatalf("manual complete on expired order: %v", err)
	}
	// 二次补单仍然拒绝(条件更新 + paid 短路), 余额只加一次。
	if err := RechargeManualComplete(context.Background(), order.OrderNo, 1, "admin"); err == nil {
		t.Fatal("double manual complete must be rejected")
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 10 {
		t.Fatalf("balance=%v, want 10", balance)
	}
}

// TestRechargeExpireRunClosesOnlyDuePending 周期任务只关到期的 pending, 不碰其它状态。
func TestRechargeExpireRunClosesOnlyDuePending(t *testing.T) {
	user := rechargeTestSetup(t, "expire-run")
	due, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	fresh, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.GetDB().Model(&model.RechargeOrder{}).Where("order_no = ?", due.OrderNo).Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	closed, err := RechargeExpireRun(context.Background())
	if err != nil {
		t.Fatalf("expire run: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed=%d, want 1 (only the due one)", closed)
	}
	var freshRow model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", fresh.OrderNo).First(&freshRow).Error; err != nil {
		t.Fatal(err)
	}
	// 未到期的单必须仍是 pending: 提前关闭会白白吃掉用户还在付款中的订单。
	if freshRow.Status != model.RechargeStatusPending {
		t.Fatalf("fresh order status=%s, want pending", freshRow.Status)
	}
	var dueRow model.RechargeOrder
	if err := db.GetDB().Where("order_no = ?", due.OrderNo).First(&dueRow).Error; err != nil {
		t.Fatal(err)
	}
	if dueRow.Status != model.RechargeStatusExpired {
		t.Fatalf("due order status=%s, want expired", dueRow.Status)
	}
}

// TestRechargeCloseExpiredReturnsCount 关闭函数返回本轮关闭笔数: 周期任务靠它判断"有没有事发生",
// 返回固定 nil 会让任务永远无法发现"我其实一笔都没关"(阈值写错/TTL 配反都表现成静默)。
func TestRechargeCloseExpiredReturnsCount(t *testing.T) {
	_ = rechargeTestSetup(t, "close-count")
	// 没有到期的单时必须是 0 而不是报错。
	closed, err := RechargeCloseExpired(context.Background())
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed != 0 {
		t.Fatalf("closed=%d, want 0 on an empty table", closed)
	}
}
