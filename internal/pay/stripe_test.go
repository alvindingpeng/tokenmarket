package pay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// stripeTestSign 生成 Stripe-Signature 头(与官方口径一致: v1 = HMAC(whsec, "t.body"))。
func stripeTestSign(secret string, body []byte, timestamp time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp.Unix(), body)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

// stripeCapture mock 收银台捕获到的建单请求。
type stripeCapture struct {
	Auth string
	Body string
}

// stripeMockServer 起一个 mock 收银台, 返回构造好的网关与它捕获的请求; 收尾自动关服务。
// 抽出来是因为"到底报给 Stripe 多少钱"要在多个用例里反复断言, 每处各抄一遍 httptest 样板
// 只会让断言互相漂移 —— 而金额单位正是本次缺陷所在, 断言必须集中可读。
func stripeMockServer(t *testing.T, currency string) (*Stripe, *stripeCapture) {
	t.Helper()
	captured := &stripeCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Auth = r.Header.Get("Authorization")
		payload, _ := io.ReadAll(r.Body)
		captured.Body = string(payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cs_test_1","url":"https://checkout.stripe.com/pay/cs_test_1"}`)
	}))
	t.Cleanup(server.Close)
	gateway := NewStripe(StripeConfig{
		SecretKey: "sk_test", WebhookSecret: "whsec_test", APIBase: server.URL, Currency: currency,
	})
	return gateway, captured
}

// stripeForm 把捕获到的表单解析成可断言的键值; 断言失败时把原始报文一并打出来便于定位。
func stripeForm(t *testing.T, captured *stripeCapture) url.Values {
	t.Helper()
	values, err := url.ParseQuery(captured.Body)
	if err != nil {
		t.Fatalf("parse form %q: %v", captured.Body, err)
	}
	return values
}

// TestStripeCreatePaymentSession 建单请求带价格与单号, 返回收银台链接。
func TestStripeCreatePaymentSession(t *testing.T) {
	gateway, captured := stripeMockServer(t, "usd")
	// PayCents 与 AmountCents 相等(CNY 收款渠道按面值换算), 两种口径此时一致;
	// 真正的分歧在 TestStripeUnitAmountUsesPayCents。
	order := &model.RechargeOrder{
		OrderNo: "O1", AmountCents: 1000, Currency: "CNY",
		PayCents: 1000, PayCurrency: "USD", Subject: "余额充值",
	}
	form, err := gateway.CreatePayment(order, "https://api/pay/notify/stripe", "https://api/pay/return/stripe?order_no=O1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.RedirectURL != "https://checkout.stripe.com/pay/cs_test_1" {
		t.Fatalf("redirect = %s", form.RedirectURL)
	}
	if captured.Auth != "Bearer sk_test" {
		t.Fatalf("auth = %s", captured.Auth)
	}
	values := stripeForm(t, captured)
	if values.Get("client_reference_id") != "O1" || values.Get("mode") != "payment" {
		t.Fatalf("body = %s", captured.Body)
	}
	// 金额单位是**渠道币种分**, 故按 PayCents 断言而不是 AmountCents。
	if values.Get("line_items[0][price_data][unit_amount]") != strconv.FormatInt(order.PayCents, 10) {
		t.Fatalf("unit_amount = %q, want PayCents %d; body = %s",
			values.Get("line_items[0][price_data][unit_amount]"), order.PayCents, captured.Body)
	}
	if values.Get("line_items[0][price_data][currency]") != "usd" {
		t.Fatalf("currency = %q, want lowercase of ChargeCurrency", values.Get("line_items[0][price_data][currency]"))
	}
	if values.Get("metadata[order_no]") != "O1" || !strings.Contains(values.Get("success_url"), "order_no=O1") {
		t.Fatalf("urls = %s", captured.Body)
	}
	if gateway.ChargeCurrency() != "USD" {
		t.Fatalf("currency = %s", gateway.ChargeCurrency())
	}
}

// TestStripeUnitAmountUsesPayCents 防回归核心: unit_amount 必须是渠道实付额 PayCents,
// 绝不能是订单的 CNY 分 AmountCents —— 否则 ¥70 的单会被开成 $70.00 收银台。
func TestStripeUnitAmountUsesPayCents(t *testing.T) {
	gateway, captured := stripeMockServer(t, "usd")
	// 真实场景: ¥70.00 的订单, 按牌价折成 $10.00(1000 美分)。
	order := &model.RechargeOrder{
		OrderNo: "O2", AmountCents: 7000, Currency: "CNY",
		PayCents: 1000, PayCurrency: "USD", Subject: "余额充值",
	}
	if _, err := gateway.CreatePayment(order, "https://api/pay/notify/stripe", "https://api/pay/return/stripe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	values := stripeForm(t, captured)
	unitAmount := values.Get("line_items[0][price_data][unit_amount]")
	if unitAmount != "1000" {
		t.Fatalf("unit_amount = %q, want \"1000\" (PayCents), 报 AmountCents 会把 ¥70 收成 $70", unitAmount)
	}
	if unitAmount == strconv.FormatInt(order.AmountCents, 10) {
		t.Fatalf("unit_amount 等于 AmountCents=%d: 收银台把 CNY 分当成了 %s 分",
			order.AmountCents, gateway.ChargeCurrency())
	}
	// 币种必须是收款币种的小写形式, 与 unit_amount 同一币种, 两者不能错配。
	if values.Get("line_items[0][price_data][currency]") != strings.ToLower(gateway.ChargeCurrency()) {
		t.Fatalf("currency = %q, want %q",
			values.Get("line_items[0][price_data][currency]"), strings.ToLower(gateway.ChargeCurrency()))
	}
}

// TestStripeUnitAmountFallsBackWhenNoPayCents 商品定价渠道(PayCents=0)不核金额,
// 没有渠道实付额可报, 回退 AmountCents, 免得开出一张 0 元收银台。
func TestStripeUnitAmountFallsBackWhenNoPayCents(t *testing.T) {
	gateway, captured := stripeMockServer(t, "usd")
	order := &model.RechargeOrder{
		OrderNo: "O3", AmountCents: 7000, Currency: "CNY",
		PayCents: 0, PayCurrency: "", Subject: "余额充值",
	}
	if _, err := gateway.CreatePayment(order, "https://api/pay/notify/stripe", "https://api/pay/return/stripe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	values := stripeForm(t, captured)
	if values.Get("line_items[0][price_data][unit_amount]") != "7000" {
		t.Fatalf("unit_amount = %q, want fallback to AmountCents 7000",
			values.Get("line_items[0][price_data][unit_amount]"))
	}
}

// TestStripeCNYOrderUSDChargeEndToEnd 端到端: CNY 订单 + USD 收款。
//
// 这条用例锁死整条链路的口径一致性 —— 收银台报的金额、Stripe 回调的 amount_total、
// op 层核对用的 order.PayCents 必须是同一个数。只要收银台报 AmountCents,
// 用户付了 AmountCents 后回调核对必然不等, 订单永远核不过金额, 钱扣了却不入账。
func TestStripeCNYOrderUSDChargeEndToEnd(t *testing.T) {
	secret := "whsec_test"
	gateway, captured := stripeMockServer(t, "usd")
	gateway.Config.WebhookSecret = secret
	order := &model.RechargeOrder{
		OrderNo: "O4", AmountCents: 7000, Currency: "CNY",
		PayCents: 1000, PayCurrency: "USD", Subject: "余额充值",
	}
	if _, err := gateway.CreatePayment(order, "https://api/pay/notify/stripe", "https://api/pay/return/stripe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	values := stripeForm(t, captured)
	unitAmount, err := strconv.ParseInt(values.Get("line_items[0][price_data][unit_amount]"), 10, 64)
	if err != nil {
		t.Fatalf("unit_amount not an integer: %v", err)
	}
	if unitAmount != order.PayCents {
		t.Fatalf("收银台金额 %d != 渠道实付额 %d", unitAmount, order.PayCents)
	}

	// Stripe 按收银台金额收款, 回调带回的就是这个数(单位同收银台: 渠道币种分)。
	// 这里用真实签名走一遍 VerifyCallback, 确认拿到的金额等于 op 层核对的 PayCents。
	body := []byte(fmt.Sprintf(
		`{"type":"checkout.session.completed","data":{"object":{"id":"cs_e2e","client_reference_id":%q,"amount_total":%d,"currency":%q,"payment_status":"paid"}}}`,
		order.OrderNo, unitAmount, strings.ToLower(gateway.ChargeCurrency())))
	header := http.Header{}
	header.Set("Stripe-Signature", stripeTestSign(secret, body, time.Now()))
	_, orderNo, amountCents, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: header})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if orderNo != order.OrderNo {
		t.Fatalf("order no = %s, want %s", orderNo, order.OrderNo)
	}
	// 这是 op 层 RechargeNotify 的核对式(order.PayCents > 0 && amountCents != order.PayCents):
	// 相等即入账, 不等即 mismatch 且永不入账。
	if amountCents != order.PayCents {
		t.Fatalf("回调金额 %d 与 order.PayCents %d 不等: op 层会判 amount mismatch, 余额永不入账",
			amountCents, order.PayCents)
	}
	// 反面证据: 旧的"报 AmountCents"写法会让用户付 7000, 回调核对必然失败。
	if amountCents == order.AmountCents {
		t.Fatalf("回调金额等于 CNY 分 %d: 这正是不折算就开单会出现的金额", order.AmountCents)
	}
}

// TestStripeVerifyCallback Webhook 验签解析; 伪造签名/过期时间戳/未付款状态一律拒绝。
func TestStripeVerifyCallback(t *testing.T) {
	secret := "whsec_test"
	gateway := NewStripe(StripeConfig{SecretKey: "sk_test", WebhookSecret: secret, Currency: "usd"})
	body := []byte(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","client_reference_id":"O9","amount_total":1000,"currency":"usd","payment_status":"paid"}}}`)
	header := http.Header{}
	header.Set("Stripe-Signature", stripeTestSign(secret, body, time.Now()))
	tradeNo, orderNo, amountCents, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: header})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tradeNo != "cs_1" || orderNo != "O9" || amountCents != 1000 {
		t.Fatalf("tradeNo=%s orderNo=%s cents=%d", tradeNo, orderNo, amountCents)
	}
	// 伪造签名。
	bad := http.Header{}
	bad.Set("Stripe-Signature", stripeTestSign("whsec_wrong", body, time.Now()))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: bad}); err == nil {
		t.Fatal("bad signature must be rejected")
	}
	// 篡改报文。
	tampered := append([]byte{}, body...)
	tampered = []byte(strings.Replace(string(tampered), `"amount_total":1000`, `"amount_total":999999`, 1))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: tampered, Header: header}); err == nil {
		t.Fatal("tampered body must be rejected")
	}
	// 过期时间戳(重放)。
	stale := http.Header{}
	stale.Set("Stripe-Signature", stripeTestSign(secret, body, time.Now().Add(-time.Hour)))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: stale}); err == nil {
		t.Fatal("stale signature must be rejected")
	}
	// 未付款状态。
	unpaid := []byte(`{"type":"checkout.session.completed","data":{"object":{"id":"cs_2","client_reference_id":"O9","amount_total":1000,"currency":"usd","payment_status":"unpaid"}}}`)
	unpaidHeader := http.Header{}
	unpaidHeader.Set("Stripe-Signature", stripeTestSign(secret, unpaid, time.Now()))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: unpaid, Header: unpaidHeader}); err == nil {
		t.Fatal("unpaid session must be rejected")
	}
	// 非目标事件。
	other := []byte(`{"type":"payment_intent.created","data":{"object":{"id":"pi_1","client_reference_id":"O9","amount_total":1000,"payment_status":"paid"}}}`)
	otherHeader := http.Header{}
	otherHeader.Set("Stripe-Signature", stripeTestSign(secret, other, time.Now()))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: other, Header: otherHeader}); err == nil {
		t.Fatal("unexpected event type must be rejected")
	}
}
