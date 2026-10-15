package pay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// stripeTestSign 生成 Stripe-Signature 头(与官方口径一致: v1 = HMAC(whsec, "t.body"))。
func stripeTestSign(secret string, body []byte, timestamp time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp.Unix(), body)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

// TestStripeCreatePaymentSession 建单请求带价格与单号, 返回收银台链接。
func TestStripeCreatePaymentSession(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		payload := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(payload)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cs_test_1","url":"https://checkout.stripe.com/pay/cs_test_1"}`)
	}))
	defer server.Close()
	gateway := NewStripe(StripeConfig{SecretKey: "sk_test", WebhookSecret: "whsec_test", APIBase: server.URL, Currency: "usd"})
	order := &model.RechargeOrder{OrderNo: "O1", AmountCents: 1000, Currency: "CNY", Subject: "余额充值"}
	form, err := gateway.CreatePayment(order, "https://api/pay/notify/stripe", "https://api/pay/return/stripe?order_no=O1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.RedirectURL != "https://checkout.stripe.com/pay/cs_test_1" {
		t.Fatalf("redirect = %s", form.RedirectURL)
	}
	if gotAuth != "Bearer sk_test" {
		t.Fatalf("auth = %s", gotAuth)
	}
	values, _ := url.ParseQuery(gotBody)
	if values.Get("client_reference_id") != "O1" || values.Get("mode") != "payment" {
		t.Fatalf("body = %s", gotBody)
	}
	if values.Get("line_items[0][price_data][unit_amount]") != "1000" || values.Get("line_items[0][price_data][currency]") != "usd" {
		t.Fatalf("price data = %s", gotBody)
	}
	if values.Get("metadata[order_no]") != "O1" || !strings.Contains(values.Get("success_url"), "order_no=O1") {
		t.Fatalf("urls = %s", gotBody)
	}
	if gateway.ChargeCurrency() != "USD" {
		t.Fatalf("currency = %s", gateway.ChargeCurrency())
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
	tampered = []byte(strings.Replace(string(tampered), "\"amount_total\":1000", "\"amount_total\":999999", 1))
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
