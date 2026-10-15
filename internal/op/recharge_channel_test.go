package op

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/pay"
)

// channelTestSetup 初始化隔离库 + 一套渠道配置; 返回用户与测试 RSA 私钥(签支付宝回调用)。
func channelTestSetup(t *testing.T, username string) (model.User, *rsa.PrivateKey) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, _ := x509.MarshalPKCS8PrivateKey(key)
	publicDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	privateBody := strings.ReplaceAll(base64.StdEncoding.EncodeToString(privateDER), "=", "")
	publicBody := strings.ReplaceAll(base64.StdEncoding.EncodeToString(publicDER), "=", "")
	for settingKey, value := range map[model.SettingKey]string{
		model.SettingKeyRechargeRate:         "7.0",
		model.SettingKeyRechargeMinCNY:       "1",
		model.SettingKeyRechargeBonusEnabled: "false",
		model.SettingKeyAlipayGatewayURL:     "https://openapi.example.com",
		model.SettingKeyAlipayAppID:          "2026",
		model.SettingKeyAlipayPrivateKey:     privateBody,
		model.SettingKeyAlipayPublicKey:      publicBody,
	} {
		if err := SettingUpsert(settingKey, value); err != nil {
			t.Fatalf("seed setting %s: %v", settingKey, err)
		}
	}
	user := model.User{Username: username, Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user, key
}

// channelTestViews 返回渠道视图按 ID 索引。
func channelTestViews(t *testing.T) map[string]PayChannel {
	t.Helper()
	views := map[string]PayChannel{}
	for _, channel := range RechargeViewGet(context.Background()).Channels {
		views[channel.ID] = channel
	}
	return views
}

// TestRechargeChannelViews 渠道配置状态与展示名对齐 New API pay_methods 口径。
func TestRechargeChannelViews(t *testing.T) {
	channelTestSetup(t, "channels")
	views := channelTestViews(t)
	if !views[pay.ProviderAlipay].Configured {
		t.Fatal("alipay must show configured after seeding keys")
	}
	if views[pay.ProviderStripe].Configured || views[pay.ProviderCreem].Configured {
		t.Fatal("stripe/creem must show unconfigured by default")
	}
	if views[pay.ProviderEpay].Name != "易支付" || views[pay.ProviderStripe].Name != "Stripe" {
		t.Fatalf("channel names = %v", views)
	}
	if !RechargeViewGet(context.Background()).Configured {
		t.Fatal("view must report configured when any channel is ready")
	}
}

// TestRechargeCreateUnknownProvider 未知渠道直接拒绝。
func TestRechargeCreateUnknownProvider(t *testing.T) {
	user, _ := channelTestSetup(t, "unknown")
	if _, _, err := RechargeCreate(context.Background(), user.ID, 7000, "paypal", "", "n", "r"); err == nil {
		t.Fatal("unknown provider must be rejected")
	}
	if _, _, err := RechargeCreate(context.Background(), user.ID, 7000, "stripe", "", "n", "r"); err == nil {
		t.Fatal("unconfigured stripe must be rejected")
	}
}

// TestRechargeAlipaySettle 支付宝下单出表单, 签名回调入账, 篡改金额拒绝。
func TestRechargeAlipaySettle(t *testing.T) {
	user, key := channelTestSetup(t, "alipay-settle")
	order, form, err := RechargeCreate(context.Background(), user.ID, 7000, "alipay", "", "https://api/pay/notify/alipay", "https://api/pay/return/alipay")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.Action != "https://openapi.example.com/gateway.do" || order.PayCents != 7000 || order.PayCurrency != "CNY" {
		t.Fatalf("form=%s pay=%d%s", form.Action, order.PayCents, order.PayCurrency)
	}
	params := map[string]string{
		"app_id":       "2026",
		"trade_no":     "ALI100",
		"out_trade_no": order.OrderNo,
		"trade_status": "TRADE_SUCCESS",
		"total_amount": "70.00",
	}
	signature, err := pay.AlipaySignParams(params, key)
	if err != nil {
		t.Fatal(err)
	}
	params["sign"] = signature
	params["sign_type"] = "RSA2"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	if err := RechargeNotify(context.Background(), "alipay", &pay.CallbackRequest{Form: query}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 10 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 10 and 10", balance, recharged)
	}
	// 篡改金额: 只付 1 分却要 70 元入账, 必须拒绝。
	order2, _, err := RechargeCreate(context.Background(), user.ID, 7000, "alipay", "", "n", "r")
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	tampered := map[string]string{
		"app_id": "2026", "trade_no": "ALI200", "out_trade_no": order2.OrderNo,
		"trade_status": "TRADE_SUCCESS", "total_amount": "0.01",
	}
	tamperedSign, _ := pay.AlipaySignParams(tampered, key)
	tampered["sign"] = tamperedSign
	tamperedQuery := url.Values{}
	for name, value := range tampered {
		tamperedQuery.Set(name, value)
	}
	if err := RechargeNotify(context.Background(), "alipay", &pay.CallbackRequest{Form: tamperedQuery}); err == nil {
		t.Fatal("amount mismatch must be rejected")
	}
	balance, _ = redemptionTestBalance(t, user.ID)
	if balance != 10 {
		t.Fatalf("balance=%v, want 10 (mismatch 不入账)", balance)
	}
}

// TestRechargeStripeUSDSettle Stripe 非 CNY 收款按汇率快照换算, Webhook 入账 USD 面额。
func TestRechargeStripeUSDSettle(t *testing.T) {
	user, _ := channelTestSetup(t, "stripe-settle")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cs_1","url":"https://checkout.stripe.com/pay/cs_1"}`)
	}))
	defer server.Close()
	for settingKey, value := range map[model.SettingKey]string{
		model.SettingKeyStripeSecretKey:   "sk_test",
		model.SettingKeyStripeWebhook:     "whsec_test",
		model.SettingKeyStripeAPIBase:     server.URL,
		model.SettingKeyStripeCurrency:    "usd",
	} {
		if err := SettingUpsert(settingKey, value); err != nil {
			t.Fatal(err)
		}
	}
	// ¥70 @ 汇率 7 = $10 = 1000 美分。
	order, form, err := RechargeCreate(context.Background(), user.ID, 7000, "stripe", "", "https://api/pay/notify/stripe", "https://api/pay/return/stripe")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.RedirectURL == "" || order.PayCents != 1000 || order.PayCurrency != "USD" {
		t.Fatalf("redirect=%s pay=%d%s", form.RedirectURL, order.PayCents, order.PayCurrency)
	}
	if order.Credits != 10 {
		t.Fatalf("credits=%v, want 10", order.Credits)
	}
	body := []byte(fmt.Sprintf(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","client_reference_id":"%s","amount_total":1000,"currency":"usd","payment_status":"paid"}}}`, order.OrderNo))
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	mac.Write([]byte(fmt.Sprintf("%d.%s", time.Now().Unix(), body)))
	header := http.Header{}
	header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", time.Now().Unix(), hex.EncodeToString(mac.Sum(nil))))
	if err := RechargeNotify(context.Background(), "stripe", &pay.CallbackRequest{Body: body, Header: header}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	balance, recharged := redemptionTestBalance(t, user.ID)
	if balance != 10 || recharged != 10 {
		t.Fatalf("balance=%v recharged=%v, want 10 and 10", balance, recharged)
	}
}

// TestRechargeCreemProductCredit Creem 按档位入账(不走汇率), 金额不匹配档位拒绝下单。
func TestRechargeCreemProductCredit(t *testing.T) {
	user, _ := channelTestSetup(t, "creem-settle")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"checkout_url":"https://test.creem.io/checkout/c_1"}`)
	}))
	defer server.Close()
	for settingKey, value := range map[model.SettingKey]string{
		model.SettingKeyCreemAPIKey:   "ck_test",
		model.SettingKeyCreemWebhook:  "whsec",
		model.SettingKeyCreemAPIBase:  server.URL,
		model.SettingKeyCreemProducts: `[{"product_id":"prod_x","name":"¥70 充值","amount_cents":7000,"credits":42}]`,
	} {
		if err := SettingUpsert(settingKey, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := RechargeCreate(context.Background(), user.ID, 1234, "creem", "", "n", "r"); err == nil {
		t.Fatal("amount without matching product must be rejected")
	}
	order, form, err := RechargeCreate(context.Background(), user.ID, 7000, "creem", "", "https://api/pay/notify/creem", "https://api/pay/return/creem")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.RedirectURL == "" || order.Credits != 42 {
		t.Fatalf("redirect=%s credits=%v", form.RedirectURL, order.Credits)
	}
	body := []byte(fmt.Sprintf(`{"type":"checkout.completed","data":{"checkout":{"id":"ch_1","request_id":"%s","amount":1000,"product":{"id":"prod_x"}}}}`, order.OrderNo))
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write(body)
	header := http.Header{}
	header.Set("creem-signature", hex.EncodeToString(mac.Sum(nil)))
	if err := RechargeNotify(context.Background(), "creem", &pay.CallbackRequest{Body: body, Header: header}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	balance, _ := redemptionTestBalance(t, user.ID)
	if balance != 42 {
		t.Fatalf("balance=%v, want 42 (档位入账)", balance)
	}
}
