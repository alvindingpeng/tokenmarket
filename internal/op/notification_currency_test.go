package op

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/pay"
)

// 本文件守住"金额单位不许标错"这一条: 站内通知里的金额如果是账面币种(model.LedgerCurrency = USD),
// 就不能印成人民币单位「元」。
//
// 为什么值得单独一组用例: 标错单位不会报错、不会让任何金额变错、也不会被任何对账发现 ——
// 用户只是把 100 USD 读成 100 元, 于是以为自己还有 700 元的额度, 反过来把"余额不足"告警
// 当成系统误报。这类缺陷只有靠断言正文文本才抓得住。

// notificationContentOf 等异步通知落库后返回指定用户+类型的最新一条正文。
func notificationContentOf(t *testing.T, userID uint, notifType model.NotificationType, relatedID uint64) string {
	t.Helper()
	NotificationDispatchDrain()
	var notif model.Notification
	query := db.GetDB().Where("user_id = ? AND type = ?", userID, notifType)
	if relatedID != 0 {
		query = query.Where("related_id = ?", relatedID)
	}
	if err := query.Order("id DESC").First(&notif).Error; err != nil {
		t.Fatalf("expected %s notification for user %d: %v", notifType, userID, err)
	}
	return notif.Content
}

// assertLedgerCurrencyInNotification 断言通知正文标了账面币种、没有「元」、且金额数值未被改动。
func assertLedgerCurrencyInNotification(t *testing.T, content, subject string, amount float64) {
	t.Helper()
	if !strings.Contains(content, model.LedgerCurrency) {
		t.Fatalf("%s 没有标注金额币种 %s, 用户会把 USD 读成人民币: %q", subject, model.LedgerCurrency, content)
	}
	if strings.Contains(content, "元") {
		t.Fatalf("%s 仍在用人民币单位「元」标注 USD 金额: %q", subject, content)
	}
	if !strings.Contains(content, fmt.Sprintf("%.2f", amount)) {
		t.Fatalf("%s 丢了金额 %v: %q", subject, amount, content)
	}
}

// TestNotifyBalanceLowUsesLedgerCurrency 余额不足通知不许把 USD 写成「元」。
func TestNotifyBalanceLowUsesLedgerCurrency(t *testing.T) {
	opTestDBSetup(t, "notify-balance-low-currency.db")
	const userID = uint(7)
	if err := NotifyBalanceLow(context.Background(), userID, 3.5); err != nil {
		t.Fatalf("notify: %v", err)
	}
	content := notificationContentOf(t, userID, model.NotificationTypeBalanceLow, 0)
	assertLedgerCurrencyInNotification(t, content, "余额不足通知", 3.5)
}

// TestLowBalanceAlertNotifyUsesLedgerCurrency 结算后"接近起扣门槛"的用户侧通知同口径。
//
// 这条走真实触发路径(maybeRaiseLowBalanceAlert), 而不是直接调 NotificationCreate:
// 金额文案是在函数体里现拼的, 只测底层写入函数等于没测。
func TestLowBalanceAlertNotifyUsesLedgerCurrency(t *testing.T) {
	opTestDBSetup(t, "notify-low-balance-alert-currency.db")
	if err := SettingUpsert(model.SettingKeyMinBalance, "1"); err != nil {
		t.Fatal(err)
	}
	// 必须走 UserCreate: 它会把用户写进 userCache, 而 maybeRaiseLowBalanceAlert 走的是
	// UserGetByID(读缓存) —— 直接 db.Create 不刷缓存, 函数会在第一行就静默返回,
	// 用例于是红在"没有通知", 看起来像文案问题, 实际是用例自身漏了一步。
	user, err := UserCreate("low-balance-currency", "", "secret123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", user.ID).Update("balance", 1).Error; err != nil {
		t.Fatal(err)
	}
	user.Balance = 1
	userCache.Set(int(user.ID), user)
	// available = 1 < 门槛 1 + 本笔预估 1 = 2 → 命中预警。
	maybeRaiseLowBalanceAlert(context.Background(), user.ID, 1)
	content := notificationContentOf(t, user.ID, model.NotificationTypeBalanceLow, 0)
	assertLedgerCurrencyInNotification(t, content, "低余额预警通知", 1)
}

// TestRechargeNotifyUsesLedgerCurrency 充值到账通知标的必须是入账余额(USD), 不是下单面额。
//
// 用户拿下单面额(可能是 CNY/JPY 的 amount_cents)对这个数字永远对不上; 标「元」更会把
// "到账 10 USD"读成"到账 10 元"。
func TestRechargeNotifyUsesLedgerCurrency(t *testing.T) {
	user := rechargeTestSetup(t, "recharge-notify-currency")
	order, _, err := RechargeCreate(context.Background(), user.ID, 7000, "epay", "alipay", "https://api/n", "https://api/r")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := RechargeNotify(context.Background(), "epay", &pay.CallbackRequest{Form: rechargeTestQuery(order.OrderNo, "T-CURRENCY", "70.00")}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	content := notificationContentOf(t, user.ID, model.NotificationTypeRecharge, 0)
	assertLedgerCurrencyInNotification(t, content, "充值到账通知", order.Credits+order.Bonus)
	if !strings.Contains(content, order.OrderNo) {
		t.Fatalf("充值通知丢了订单号 %s: %q", order.OrderNo, content)
	}
}

// TestNotifyWithdrawalUsesLedgerCurrency NotifyWithdrawal 四个状态分支全都不许写「元」。
//
// 这个函数当前全仓 0 调用点, 所以它是"将来才会爆"的缺陷: 一旦有人把它接上提现流程,
// 四个分支会同时复现同一个错误。用例逐个分支跑, 避免只修了 default 分支。
func TestNotifyWithdrawalUsesLedgerCurrency(t *testing.T) {
	opTestDBSetup(t, "notify-withdrawal-helper-currency.db")
	const userID = uint(9)
	for _, status := range []string{"approved", "rejected", "paid", "submitted"} {
		t.Run(status, func(t *testing.T) {
			if err := NotifyWithdrawal(context.Background(), userID, 1, 100, status); err != nil {
				t.Fatalf("notify: %v", err)
			}
			NotificationDispatchDrain()
			var notif model.Notification
			if err := db.GetDB().Where("user_id = ? AND type = ?", userID, model.NotificationTypeWithdrawal).
				Order("id DESC").First(&notif).Error; err != nil {
				t.Fatalf("expected withdrawal notification: %v", err)
			}
			assertLedgerCurrencyInNotification(t, notif.Content, "提现状态通知("+status+")", 100)
			// 同一类型 + 未读的通知在窗口内会被去重: 标已读, 让下一个分支能真正写进去。
			if err := db.GetDB().Model(&model.Notification{}).Where("id = ?", notif.ID).
				Update("status", model.NotificationStatusRead).Error; err != nil {
				t.Fatal(err)
			}
		})
	}
}
