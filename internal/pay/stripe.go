package pay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// StripeConfig Stripe 参数; 全部来自后台设置, 多币种按 ChargeCurrency 声明。
type StripeConfig struct {
	SecretKey     string // sk_... 密钥(密钥类设置)
	WebhookSecret string // whsec_... Webhook 签名密钥
	APIBase       string // 默认 https://api.stripe.com, 测试可指向 mock
	Currency      string // 收款币种, 默认 usd
}

// Configured 参数是否齐备。
func (c StripeConfig) Configured() bool {
	return strings.TrimSpace(c.SecretKey) != "" && strings.TrimSpace(c.WebhookSecret) != ""
}

// Stripe 支付宝之外的国际卡收款: Checkout Session + Webhook 验签。
type Stripe struct{ Config StripeConfig }

// NewStripe 构造 Stripe 网关。
func NewStripe(config StripeConfig) *Stripe { return &Stripe{Config: config} }

// ChargeCurrency Stripe 收款币种(大写)。
func (s *Stripe) ChargeCurrency() string {
	currency := strings.ToUpper(strings.TrimSpace(s.Config.Currency))
	if currency == "" {
		return "USD"
	}
	return currency
}

// stripeAPIBase API 根地址; 默认官方, 便于测试指向 mock。
func (s *Stripe) stripeAPIBase() string {
	base := strings.TrimSpace(s.Config.APIBase)
	if base == "" {
		return "https://api.stripe.com"
	}
	return strings.TrimRight(base, "/")
}

// CreatePayment 创建 Checkout Session, 返回跳转链接。
func (s *Stripe) CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error) {
	if !s.Config.Configured() {
		return nil, fmt.Errorf("payment gateway is not configured")
	}
	currency := strings.ToLower(s.ChargeCurrency())
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("client_reference_id", order.OrderNo)
	form.Set("metadata[order_no]", order.OrderNo)
	form.Set("success_url", returnURL)
	form.Set("cancel_url", returnURL)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", currency)
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(order.AmountCents, 10))
	form.Set("line_items[0][price_data][product_data][name]", order.Subject)
	request, err := http.NewRequest(http.MethodPost, s.stripeAPIBase()+"/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to build stripe request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.Config.SecretKey)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("stripe request failed: %w", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stripe checkout failed: %s", strings.TrimSpace(string(payload))[:min(len(payload), 200)])
	}
	var session struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload, &session); err != nil || session.URL == "" {
		return nil, fmt.Errorf("stripe checkout response is invalid")
	}
	return &Form{RedirectURL: session.URL}, nil
}

// stripeSignature 验证 Stripe-Signature: v1 = HMAC-SHA256(whsec, "t.原始报文")。
func stripeSignatureValid(header string, body []byte, secret string, now time.Time) bool {
	var timestamp int64
	matched := false
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "t="):
			timestamp, _ = strconv.ParseInt(strings.TrimPrefix(part, "t="), 10, 64)
		case strings.HasPrefix(part, "v1="):
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
			expected := hex.EncodeToString(mac.Sum(nil))
			if hmac.Equal([]byte(expected), []byte(strings.TrimPrefix(part, "v1="))) {
				matched = true
			}
		}
	}
	if !matched {
		return false
	}
	// 重放窗口 5 分钟: 时间戳离现在太远的一律拒绝。
	delta := now.Unix() - timestamp
	if delta < 0 {
		delta = -delta
	}
	return delta <= 300
}

// VerifyCallback 验证 Webhook 签名并解析 checkout.session.completed 事件。
func (s *Stripe) VerifyCallback(req *CallbackRequest) (string, string, int64, error) {
	if !s.Config.Configured() {
		return "", "", 0, fmt.Errorf("payment gateway is not configured")
	}
	if req == nil || len(req.Body) == 0 {
		return "", "", 0, fmt.Errorf("callback body is missing")
	}
	if !stripeSignatureValid(req.Header.Get("Stripe-Signature"), req.Body, s.Config.WebhookSecret, time.Now()) {
		return "", "", 0, fmt.Errorf("invalid stripe callback signature")
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID                string `json:"id"`
				ClientReferenceID string `json:"client_reference_id"`
				AmountTotal       int64  `json:"amount_total"`
				Currency          string `json:"currency"`
				PaymentStatus     string `json:"payment_status"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(req.Body, &event); err != nil {
		return "", "", 0, fmt.Errorf("stripe callback body is invalid")
	}
	if event.Type != "checkout.session.completed" {
		return "", "", 0, fmt.Errorf("unsupported stripe event: %s", event.Type)
	}
	session := event.Data.Object
	if session.PaymentStatus != "paid" {
		return "", "", 0, fmt.Errorf("stripe payment status is not paid")
	}
	if session.ClientReferenceID == "" || session.AmountTotal <= 0 {
		return "", "", 0, fmt.Errorf("stripe callback missing trade number")
	}
	return session.ID, session.ClientReferenceID, session.AmountTotal, nil
}
