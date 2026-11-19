package pay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// CreemConfig Creem(MoR 全球收单)参数; 商品档位在 Creem 后台建好后配置映射。
type CreemConfig struct {
	APIKey        string               // x-api-key(密钥类设置)
	WebhookSecret string               // Webhook 签名密钥
	APIBase       string               // 默认 https://api.creem.io, 测试环境 https://test-api.creem.io
	Products      []model.CreemProduct // 充值档位映射
}

// Configured 参数是否齐备。
func (c CreemConfig) Configured() bool {
	return strings.TrimSpace(c.APIKey) != "" && strings.TrimSpace(c.WebhookSecret) != "" && len(c.Products) > 0
}

// Creem Creem 网关: 商品化充值档位, 结账链接跳转 + Webhook 验签。
type Creem struct{ Config CreemConfig }

// NewCreem 构造 Creem 网关。
func NewCreem(config CreemConfig) *Creem { return &Creem{Config: config} }

// ChargeCurrency Creem 按档位商品币种结算; 展示与入账基准仍为 CNY 分。
func (c *Creem) ChargeCurrency() string { return "CNY" }

// ProductFor 找到金额匹配的档位。
func (c *Creem) ProductFor(amountCents int64) (model.CreemProduct, bool) {
	for _, product := range c.Config.Products {
		if product.AmountCents == amountCents {
			return product, true
		}
	}
	return model.CreemProduct{}, false
}

// CreatePayment 创建 Creem 结账会话, 返回跳转链接。
func (c *Creem) CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error) {
	if !c.Config.Configured() {
		return nil, fmt.Errorf("payment gateway is not configured")
	}
	product, ok := c.ProductFor(order.AmountCents)
	if !ok {
		return nil, fmt.Errorf("no creem product matches this amount")
	}
	requestBody, _ := json.Marshal(map[string]any{
		"product_id":  product.ProductID,
		"request_id":  order.OrderNo,
		"success_url": returnURL,
		"metadata": map[string]string{
			"order_no": order.OrderNo,
			"credits":  fmt.Sprintf("%g", product.Credits),
		},
	})
	base := strings.TrimSpace(c.Config.APIBase)
	if base == "" {
		base = "https://api.creem.io"
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/v1/checkouts", strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to build creem request: %w", err)
	}
	request.Header.Set("x-api-key", c.Config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("creem request failed: %w", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("creem checkout failed: %s", strings.TrimSpace(string(payload))[:min(len(payload), 200)])
	}
	var checkout struct {
		CheckoutURL string `json:"checkout_url"`
	}
	if err := json.Unmarshal(payload, &checkout); err != nil || checkout.CheckoutURL == "" {
		return nil, fmt.Errorf("creem checkout response is invalid")
	}
	return &Form{RedirectURL: checkout.CheckoutURL}, nil
}

// VerifyCallback 验证 creem-signature(明文 HMAC-SHA256 hex)并解析 checkout.completed 事件。
func (c *Creem) VerifyCallback(req *CallbackRequest) (string, string, int64, error) {
	if !c.Config.Configured() {
		return "", "", 0, fmt.Errorf("payment gateway is not configured")
	}
	if req == nil || len(req.Body) == 0 {
		return "", "", 0, fmt.Errorf("callback body is missing")
	}
	signature := req.Header.Get("creem-signature")
	mac := hmac.New(sha256.New, []byte(c.Config.WebhookSecret))
	mac.Write(req.Body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if signature == "" || !hmac.Equal([]byte(expected), []byte(signature)) {
		return "", "", 0, fmt.Errorf("invalid creem callback signature")
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Checkout struct {
				ID        string `json:"id"`
				RequestID string `json:"request_id"`
				Amount    int64  `json:"amount"`
				Status    string `json:"status"`
				Product   struct {
					ID string `json:"id"`
				} `json:"product"`
			} `json:"checkout"`
		} `json:"data"`
	}
	if err := json.Unmarshal(req.Body, &event); err != nil {
		return "", "", 0, fmt.Errorf("creem callback body is invalid")
	}
	if event.Type != "checkout.completed" && event.Type != "checkout.paid" {
		return "", "", 0, fmt.Errorf("unsupported creem event: %s", event.Type)
	}
	checkout := event.Data.Checkout
	if checkout.RequestID == "" {
		return "", "", 0, fmt.Errorf("creem callback missing trade number")
	}
	// 金额以订单创建时的档位为准; 回调金额仅校验商品 ID 属于配置档位。
	if checkout.Product.ID != "" {
		matched := false
		for _, product := range c.Config.Products {
			if product.ProductID == checkout.Product.ID {
				matched = true
			}
		}
		if !matched {
			return "", "", 0, fmt.Errorf("creem product is not configured")
		}
	}
	tradeNo := checkout.ID
	if tradeNo == "" {
		tradeNo = checkout.RequestID
	}
	amount := checkout.Amount
	if amount <= 0 {
		amount = 1
	}
	return tradeNo, checkout.RequestID, amount, nil
}
