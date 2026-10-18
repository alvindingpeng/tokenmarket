package pay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestCreemProductParsing 档位配置解析; 坏格式给出可读错误。
func TestCreemProductParsing(t *testing.T) {
	products, err := model.ParseCreemProducts(`[{"product_id":"prod_x","name":"¥70 充值","amount_cents":7000,"credits":10}]`)
	if err != nil || len(products) != 1 || products[0].Credits != 10 {
		t.Fatalf("products=%v err=%v", products, err)
	}
	if _, err := model.ParseCreemProducts(`[{"product_id":"","amount_cents":7000,"credits":10}]`); err == nil {
		t.Fatal("empty product_id must fail")
	}
	if _, err := model.ParseCreemProducts(`not-json`); err == nil {
		t.Fatal("bad json must fail")
	}
	empty, err := model.ParseCreemProducts("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty config must parse to empty list: %v %v", empty, err)
	}
}

// TestCreemCreatePaymentCheckout 建结账会话带商品与业务单号, 返回结账链接。
func TestCreemCreatePaymentCheckout(t *testing.T) {
	var gotKey, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		payload := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(payload)
		gotBody = string(payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"checkout_url":"https://test.creem.io/checkout/c_1"}`)
	}))
	defer server.Close()
	products := []model.CreemProduct{{ProductID: "prod_x", Name: "¥70 充值", AmountCents: 7000, Credits: 10}}
	gateway := NewCreem(CreemConfig{APIKey: "ck_test", WebhookSecret: "whsec", APIBase: server.URL, Products: products})
	order := &model.RechargeOrder{OrderNo: "O1", AmountCents: 7000, Currency: "CNY", Subject: "余额充值"}
	form, err := gateway.CreatePayment(order, "https://api/pay/notify/creem", "https://api/pay/return/creem?order_no=O1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.RedirectURL != "https://test.creem.io/checkout/c_1" {
		t.Fatalf("redirect = %s", form.RedirectURL)
	}
	if gotKey != "ck_test" || !strings.Contains(gotBody, "\"product_id\":\"prod_x\"") || !strings.Contains(gotBody, "\"request_id\":\"O1\"") {
		t.Fatalf("key=%s body=%s", gotKey, gotBody)
	}
	// 金额不匹配任何档位必须拒绝。
	bad := &model.RechargeOrder{OrderNo: "O2", AmountCents: 1234, Currency: "CNY"}
	if _, err := gateway.CreatePayment(bad, "n", "r"); err == nil {
		t.Fatal("amount without matching product must be rejected")
	}
}

// TestCreemVerifyCallback Webhook 明文 HMAC 验签; 坏签名与未配置商品必须拒绝。
func TestCreemVerifyCallback(t *testing.T) {
	products := []model.CreemProduct{{ProductID: "prod_x", Name: "¥70 充值", AmountCents: 7000, Credits: 10}}
	gateway := NewCreem(CreemConfig{APIKey: "ck_test", WebhookSecret: "whsec", Products: products})
	body := []byte(`{"type":"checkout.completed","data":{"checkout":{"id":"ch_1","request_id":"O9","amount":1000,"status":"completed","product":{"id":"prod_x"}}}}`)
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write(body)
	header := http.Header{}
	header.Set("creem-signature", hex.EncodeToString(mac.Sum(nil)))
	tradeNo, orderNo, _, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: header})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tradeNo != "ch_1" || orderNo != "O9" {
		t.Fatalf("tradeNo=%s orderNo=%s", tradeNo, orderNo)
	}
	// 坏签名。
	bad := http.Header{}
	bad.Set("creem-signature", "deadbeef")
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: body, Header: bad}); err == nil {
		t.Fatal("bad signature must be rejected")
	}
	// 未配置商品。
	alien := []byte(`{"type":"checkout.completed","data":{"checkout":{"id":"ch_2","request_id":"O8","amount":1000,"product":{"id":"prod_alien"}}}}`)
	alienHeader := http.Header{}
	mac2 := hmac.New(sha256.New, []byte("whsec"))
	mac2.Write(alien)
	alienHeader.Set("creem-signature", hex.EncodeToString(mac2.Sum(nil)))
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Body: alien, Header: alienHeader}); err == nil {
		t.Fatal("unconfigured product must be rejected")
	}
}
