package pay

import (
	"net/http"
	"net/url"

	"github.com/bestruirui/octopus/internal/model"
)

// 支付渠道(对齐 New API 的 payment_provider 口径): 每个渠道一个网关实现, 订单与入账层不感知差异。
const (
	ProviderEpay   = "epay"
	ProviderAlipay = "alipay"
	ProviderStripe = "stripe"
	ProviderCreem  = "creem"
)

// Providers 全部已实现渠道的稳定顺序。
func Providers() []string {
	return []string{ProviderEpay, ProviderAlipay, ProviderStripe, ProviderCreem}
}

// Form 收银台跳转: Fields 非空走表单自动提交(易支付/支付宝), 否则直接跳 RedirectURL(Stripe/Creem)。
type Form struct {
	Action      string            `json:"action"`
	Fields      map[string]string `json:"fields"`
	RedirectURL string            `json:"redirect_url"`
}

// CallbackRequest 支付回调统一视图: 表单渠道(epay/alipay)填 Form,
// JSON Webhook 渠道(stripe/creem)填 Body 与签名 Header。
type CallbackRequest struct {
	Form   url.Values
	Body   []byte
	Header http.Header
}

// PaymentGateway 支付网关抽象(D1 多币种预留): 金额一律"最小货币单位 + 币种",
// ChargeCurrency 声明该渠道的收款币种, 与 CNY 不同由业务层按汇率快照换算。
type PaymentGateway interface {
	// CreatePayment 创建支付单, 返回收银台跳转。
	CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error)
	// VerifyCallback 验签并解析回调: 返回第三方流水号、业务单号与实付金额(渠道币种分)。
	// 验签不过或非成功态一律返回错误, 回调字段不可信。
	VerifyCallback(req *CallbackRequest) (tradeNo, orderNo string, amountCents int64, err error)
	// ChargeCurrency 渠道收款币种(大写), CNY 表示按面值收, 其他按汇率换算。
	ChargeCurrency() string
}
