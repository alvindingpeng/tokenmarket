package pay

import (
	"net/url"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestEpaySignRoundtrip 签名与验签闭环: 正确参数通过。
func TestEpaySignRoundtrip(t *testing.T) {
	config := EpayConfig{GatewayURL: "https://pay.example.com", PID: "1001", Key: "testkey"}
	gateway := NewEpay(config)
	params := map[string]string{
		"pid":          "1001",
		"trade_no":     "T100",
		"out_trade_no": "20261014000000000001",
		"trade_status": "TRADE_SUCCESS",
		"money":        "10.00",
	}
	params["sign"] = EpaySign(params, config.Key)
	params["sign_type"] = "MD5"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	tradeNo, orderNo, amountCents, err := gateway.VerifyCallback(query)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tradeNo != "T100" || orderNo != "20261014000000000001" || amountCents != 1000 {
		t.Fatalf("tradeNo=%s orderNo=%s cents=%d", tradeNo, orderNo, amountCents)
	}
}

// TestEpayVerifyRejectsTampered 篡改金额后验签必须失败。
func TestEpayVerifyRejectsTampered(t *testing.T) {
	config := EpayConfig{GatewayURL: "https://pay.example.com", PID: "1001", Key: "testkey"}
	gateway := NewEpay(config)
	params := map[string]string{
		"pid": "1001", "trade_no": "T100", "out_trade_no": "O1", "trade_status": "TRADE_SUCCESS", "money": "10.00",
	}
	params["sign"] = EpaySign(params, config.Key)
	params["sign_type"] = "MD5"
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	query.Set("money", "0.01") // 篡改: 只付 1 分钱却要 10 元的入账
	if _, _, _, err := gateway.VerifyCallback(query); err == nil {
		t.Fatal("tampered callback must be rejected")
	}
}

// TestEpayVerifyRejectsMissingSign 无签名一律拒绝。
func TestEpayVerifyRejectsMissingSign(t *testing.T) {
	gateway := NewEpay(EpayConfig{GatewayURL: "https://pay.example.com", PID: "1001", Key: "testkey"})
	query := url.Values{}
	query.Set("trade_no", "T100")
	query.Set("out_trade_no", "O1")
	query.Set("money", "10.00")
	if _, _, _, err := gateway.VerifyCallback(query); err == nil {
		t.Fatal("unsigned callback must be rejected")
	}
}

// TestEpayCreatePaymentForm 下单表单含必需参数且带签名, 空参数不进签名串。
func TestEpayCreatePaymentForm(t *testing.T) {
	config := EpayConfig{GatewayURL: "https://pay.example.com/", PID: "1001", Key: "testkey", PayTypes: "alipay,wechat"}
	gateway := NewEpay(config)
	order := &model.RechargeOrder{OrderNo: "O1", Method: "alipay", AmountCents: 2500, Currency: "CNY", Subject: "余额充值"}
	form, err := gateway.CreatePayment(order, "https://api.example.com/notify", "https://api.example.com/return")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if form.Action != "https://pay.example.com/submit.php" {
		t.Fatalf("action = %s", form.Action)
	}
	for _, name := range []string{"pid", "type", "out_trade_no", "notify_url", "return_url", "name", "money", "sign", "sign_type"} {
		if strings.TrimSpace(form.Fields[name]) == "" {
			t.Fatalf("missing field %s", name)
		}
	}
	if form.Fields["money"] != "25.00" {
		t.Fatalf("money = %s", form.Fields["money"])
	}
	if form.Fields["sign"] != EpaySign(map[string]string{
		"pid": "1001", "type": "alipay", "out_trade_no": "O1",
		"notify_url": "https://api.example.com/notify", "return_url": "https://api.example.com/return",
		"name": "余额充值", "money": "25.00",
	}, config.Key) {
		t.Fatal("form signature mismatch")
	}
}

// TestEpayMethodNotAllowed 限定支付方式时越界拒绝; 空方式(收银台自选)不受清单限制。
func TestEpayMethodNotAllowed(t *testing.T) {
	gateway := NewEpay(EpayConfig{GatewayURL: "https://pay.example.com", PID: "1001", Key: "testkey", PayTypes: "alipay"})
	order := &model.RechargeOrder{OrderNo: "O1", Method: "wechat", AmountCents: 100}
	if _, err := gateway.CreatePayment(order, "n", "r"); err == nil {
		t.Fatal("disallowed method must be rejected")
	}
	auto := &model.RechargeOrder{OrderNo: "O2", Method: "", AmountCents: 100}
	if _, err := gateway.CreatePayment(auto, "n", "r"); err != nil {
		t.Fatalf("auto method must be allowed: %v", err)
	}
}
