package pay

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// alipayTestKeys 生成测试密钥, 返回私钥与公钥的裸 base64(不带 PEM 头尾)。
func alipayTestKeys(t *testing.T) (string, string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, _ := x509.MarshalPKCS8PrivateKey(key)
	publicDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	privateBody := strings.ReplaceAll(base64.StdEncoding.EncodeToString(privateDER), "=", "")
	publicBody := strings.ReplaceAll(base64.StdEncoding.EncodeToString(publicDER), "=", "")
	return privateBody, publicBody, key
}

// TestAlipayCreatePaymentForm 电脑网站支付表单含必需参数与 RSA2 签名, 验签通过。
func TestAlipayCreatePaymentForm(t *testing.T) {
	privateBody, publicBody, key := alipayTestKeys(t)
	config := AlipayConfig{GatewayURL: "https://openapi.example.com", AppID: "2026", PrivateKey: privateBody, PublicKey: publicBody}
	gateway, err := NewAlipay(config)
	if err != nil {
		t.Fatal(err)
	}
	order := &model.RechargeOrder{OrderNo: "O1", AmountCents: 2500, Currency: "CNY", Subject: "余额充值"}
	form, err := gateway.CreatePayment(order, "https://api.example.com/pay/notify/alipay", "https://api.example.com/pay/return/alipay?order_no=O1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.Action != "https://openapi.example.com/gateway.do" {
		t.Fatalf("action = %s", form.Action)
	}
	for _, name := range []string{"app_id", "method", "sign", "sign_type", "biz_content", "notify_url"} {
		if strings.TrimSpace(form.Fields[name]) == "" {
			t.Fatalf("missing field %s", name)
		}
	}
	if form.Fields["method"] != "alipay.trade.page.pay" || form.Fields["sign_type"] != "RSA2" {
		t.Fatalf("method=%s sign_type=%s", form.Fields["method"], form.Fields["sign_type"])
	}
	if !strings.Contains(form.Fields["biz_content"], "\"total_amount\":\"25.00\"") {
		t.Fatalf("biz_content = %s", form.Fields["biz_content"])
	}
	params := map[string]string{}
	for name, value := range form.Fields {
		params[name] = value
	}
	if !AlipayVerifyParams(params, params["sign"], &key.PublicKey) {
		t.Fatal("form signature must verify")
	}
}

// TestAlipayVerifyCallbackSigned 签名回调解析金额与单号; 篡改与非成功态必须拒绝。
func TestAlipayVerifyCallbackSigned(t *testing.T) {
	privateBody, publicBody, key := alipayTestKeys(t)
	gateway, err := NewAlipay(AlipayConfig{GatewayURL: "https://openapi.example.com", AppID: "2026", PrivateKey: privateBody, PublicKey: publicBody})
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]string{
		"app_id":       "2026",
		"trade_no":     "2026101512345678",
		"out_trade_no": "O9",
		"trade_status": "TRADE_SUCCESS",
		"total_amount": "25.00",
	}
	signature, err := AlipaySignParams(params, key)
	if err != nil {
		t.Fatal(err)
	}
	params["sign"] = signature
	params["sign_type"] = "RSA2"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	tradeNo, orderNo, amountCents, err := gateway.VerifyCallback(&CallbackRequest{Form: query})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tradeNo != "2026101512345678" || orderNo != "O9" || amountCents != 2500 {
		t.Fatalf("tradeNo=%s orderNo=%s cents=%d", tradeNo, orderNo, amountCents)
	}
	// 篡改金额必须拒绝(签名覆盖金额字段)。
	tampered := url.Values{}
	for name, value := range query {
		tampered[name] = value
	}
	tampered.Set("total_amount", "0.01")
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Form: tampered}); err == nil {
		t.Fatal("tampered callback must be rejected")
	}
	// 非成功态拒绝。
	failed := map[string]string{
		"app_id": "2026", "trade_no": "T1", "out_trade_no": "O9", "trade_status": "WAIT_BUYER_PAY", "total_amount": "25.00",
	}
	failedSign, _ := AlipaySignParams(failed, key)
	failed["sign"] = failedSign
	failedQuery := url.Values{}
	for name, value := range failed {
		failedQuery.Set(name, value)
	}
	if _, _, _, err := gateway.VerifyCallback(&CallbackRequest{Form: failedQuery}); err == nil {
		t.Fatal("non-success trade status must be rejected")
	}
	// 未配置拒绝。
	emptyGateway, _ := NewAlipay(AlipayConfig{})
	if _, _, _, err := emptyGateway.VerifyCallback(&CallbackRequest{Form: query}); err == nil {
		t.Fatal("unconfigured gateway must reject")
	}
}

// TestAlipayKeyParsing 裸 base64 与带 PEM 头尾的密钥都能解析; 垃圾输入构造报错。
func TestAlipayKeyParsing(t *testing.T) {
	privateBody, publicBody, _ := alipayTestKeys(t)
	if _, err := parseRSAPrivateKey("-----BEGIN PRIVATE KEY-----\n" + privateBody + "\n-----END PRIVATE KEY-----"); err != nil {
		t.Fatalf("PEM wrapped private key must parse: %v", err)
	}
	if _, err := parseRSAPrivateKey("garbage-not-a-key"); err == nil {
		t.Fatal("garbage private key must fail")
	}
	if _, err := NewAlipay(AlipayConfig{GatewayURL: "g", AppID: "a", PrivateKey: "bad", PublicKey: publicBody}); err == nil {
		t.Fatal("invalid private key must fail construction")
	}
}
