package pay

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
)

// PaymentGateway 支付网关抽象(D1 外币支付预留): 金额一律"最小货币单位 + 币种",
// 后续 Stripe/支付宝直连按同一接口即插即用, 订单与入账层不动。
type PaymentGateway interface {
	// CreatePayment 创建支付单, 返回要提交给收银台的表单。
	CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error)
	// VerifyCallback 验签并解析回调, 返回第三方流水号、业务单号与实付金额(分)。
	// 验签不过一律拒绝, 回调里的任何字段都不可信。
	VerifyCallback(query url.Values) (tradeNo, orderNo string, amountCents int64, err error)
}

// Form 收银台提交表单: 浏览器自动 POST 到 Action 完成跳转。
type Form struct {
	Action string            `json:"action"`
	Fields map[string]string `json:"fields"`
}

// EpayConfig 易支付参数; 全部来自后台设置, 商户信息后配即可用。
type EpayConfig struct {
	GatewayURL string // 网关地址, 如 https://pay.example.com
	PID        string // 商户 ID
	Key        string // 商户密钥
	PayTypes   string // 限定支付方式(逗号分隔: alipay,wechat,qq), 空表示不限定
}

// Configured 商户参数是否齐备: 网关地址、商户 ID、密钥三者缺一即视为未配置。
func (c EpayConfig) Configured() bool {
	return strings.TrimSpace(c.GatewayURL) != "" && strings.TrimSpace(c.PID) != "" && strings.TrimSpace(c.Key) != ""
}

// MethodAllowed 支付方式是否在允许清单内; 空方式表示"收银台自选", 交给网关决定不受清单限制。
func (c EpayConfig) MethodAllowed(method string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" || strings.TrimSpace(c.PayTypes) == "" {
		return true
	}
	for _, allowed := range strings.Split(c.PayTypes, ",") {
		if strings.ToLower(strings.TrimSpace(allowed)) == method {
			return true
		}
	}
	return false
}

// Epay 易支付网关实现: 参数化配置, 适配各易支付风格服务商。
type Epay struct{ Config EpayConfig }

// NewEpay 构造易支付网关。
func NewEpay(config EpayConfig) *Epay { return &Epay{Config: config} }

// EpaySign 易支付标准签名: 非空参数(去掉 sign/sign_type)按键名 ASCII 升序拼 k=v&...,
// 末尾直接拼商户密钥, 取 MD5 十六进制小写。
func EpaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for name, value := range params {
		if name == "sign" || name == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for index, name := range keys {
		if index > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(name)
		sb.WriteByte('=')
		sb.WriteString(params[name])
	}
	sb.WriteString(key)
	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// formatCents 分转元, 固定两位小数。
func formatCents(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64)
}

// CreatePayment 组装收银台表单并签名。
func (e *Epay) CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error) {
	if !e.Config.Configured() {
		return nil, fmt.Errorf("payment gateway is not configured")
	}
	if !e.Config.MethodAllowed(order.Method) {
		return nil, fmt.Errorf("payment method is not allowed")
	}
	params := map[string]string{
		"pid":          e.Config.PID,
		"type":         order.Method,
		"out_trade_no": order.OrderNo,
		"notify_url":   notifyURL,
		"return_url":   returnURL,
		"name":         order.Subject,
		"money":        formatCents(order.AmountCents),
	}
	params["sign"] = EpaySign(params, e.Config.Key)
	params["sign_type"] = "MD5"
	return &Form{
		Action: strings.TrimRight(strings.TrimSpace(e.Config.GatewayURL), "/") + "/submit.php",
		Fields: params,
	}, nil
}

// VerifyCallback 验签并解析回调; 签名不符或字段缺失一律拒绝。
func (e *Epay) VerifyCallback(query url.Values) (string, string, int64, error) {
	if !e.Config.Configured() {
		return "", "", 0, fmt.Errorf("payment gateway is not configured")
	}
	params := map[string]string{}
	for name, values := range query {
		if len(values) > 0 {
			params[name] = values[0]
		}
	}
	sign := params["sign"]
	if sign == "" || !strings.EqualFold(sign, EpaySign(params, e.Config.Key)) {
		return "", "", 0, fmt.Errorf("invalid callback signature")
	}
	tradeNo, orderNo := params["trade_no"], params["out_trade_no"]
	if tradeNo == "" || orderNo == "" {
		return "", "", 0, fmt.Errorf("callback missing trade number")
	}
	money, err := strconv.ParseFloat(strings.TrimSpace(params["money"]), 64)
	if err != nil || money <= 0 {
		return "", "", 0, fmt.Errorf("callback amount is invalid")
	}
	return tradeNo, orderNo, int64(math.Round(money * 100)), nil
}
