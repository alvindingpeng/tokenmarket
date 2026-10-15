package pay

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// AlipayConfig 支付宝官方直连参数(电脑网站支付); 全部来自后台设置, 后配即可用。
type AlipayConfig struct {
	GatewayURL string // 网关地址, 默认 https://openapi.alipay.com/gateway.do
	AppID      string // 应用 ID
	PrivateKey string // 应用私钥(PKCS8, 可粘贴不带 PEM 头尾的 base64)
	PublicKey  string // 支付宝公钥(验签用, 同上)
}

// Configured 参数是否齐备。
func (c AlipayConfig) Configured() bool {
	return strings.TrimSpace(c.GatewayURL) != "" && strings.TrimSpace(c.AppID) != "" &&
		strings.TrimSpace(c.PrivateKey) != "" && strings.TrimSpace(c.PublicKey) != ""
}

// Alipay 支付宝网关: RSA2(SHA256withRSA) 签名, 电脑网站支付表单跳转。
type Alipay struct {
	Config     AlipayConfig
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// NewAlipay 构造支付宝网关并解析密钥; 密钥非法直接报错, 不带病运行。
func NewAlipay(config AlipayConfig) (*Alipay, error) {
	gateway := &Alipay{Config: config}
	if !config.Configured() {
		return gateway, nil
	}
	privateKey, err := parseRSAPrivateKey(config.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("alipay private key is invalid: %w", err)
	}
	publicKey, err := parseRSAPublicKey(config.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("alipay public key is invalid: %w", err)
	}
	gateway.privateKey, gateway.publicKey = privateKey, publicKey
	return gateway, nil
}

// wrapPEM 粘贴的密钥形态各异(带/不带头尾、丢换行、丢等号);
// 统一剥掉头尾取 base64 主体, 补齐填充与折行后重建规范 PEM。
func wrapPEM(value, blockType string) []byte {
	var body strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}
		body.WriteString(strings.ReplaceAll(line, " ", ""))
	}
	content := body.String()
	// 有些渠道拷出来的密钥丢了 base64 填充等号, 补齐才能解析。
	if remainder := len(content) % 4; remainder != 0 {
		content += strings.Repeat("=", 4-remainder)
	}
	var lines []string
	for len(content) > 64 {
		lines = append(lines, content[:64])
		content = content[64:]
	}
	if content != "" {
		lines = append(lines, content)
	}
	return []byte("-----BEGIN " + blockType + "-----\n" + strings.Join(lines, "\n") + "\n-----END " + blockType + "-----")
}

// parseRSAPrivateKey 解析 PKCS8 私钥(兼容 PKCS1)。
func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(wrapPEM(value, "PRIVATE KEY"))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("not an RSA private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// parseRSAPublicKey 解析 PKIX 公钥(兼容证书或裸 PKCS1)。
func parseRSAPublicKey(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(wrapPEM(value, "PUBLIC KEY"))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("not an RSA public key")
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	return x509.ParsePKCS1PublicKey(block.Bytes)
}

// AlipaySignPayload 生成待签名串(验签复用同一函数, 保证两边口径一致)。
func AlipaySignPayload(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for name, value := range params {
		if name == "sign" || name == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+params[name])
	}
	return strings.Join(parts, "&")
}

// AlipaySignParams 参数签名: SHA256withRSA 后 base64。
func AlipaySignParams(params map[string]string, privateKey *rsa.PrivateKey) (string, error) {
	digest := sha256.Sum256([]byte(AlipaySignPayload(params)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign alipay params: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// AlipayVerifyParams 验签: 支付宝公钥 + SHA256withRSA。
func AlipayVerifyParams(params map[string]string, signature string, publicKey *rsa.PublicKey) bool {
	if signature == "" || publicKey == nil {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(AlipaySignPayload(params)))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], raw) == nil
}

// ChargeCurrency 支付宝收人民币。
func (a *Alipay) ChargeCurrency() string { return "CNY" }

// CreatePayment 组装电脑网站支付表单(alipay.trade.page.pay), 浏览器自动提交跳收银台。
func (a *Alipay) CreatePayment(order *model.RechargeOrder, notifyURL, returnURL string) (*Form, error) {
	if !a.Config.Configured() {
		return nil, fmt.Errorf("payment gateway is not configured")
	}
	bizContent := fmt.Sprintf(
		"{\"out_trade_no\":\"%s\",\"total_amount\":\"%s\",\"subject\":\"%s\",\"product_code\":\"FAST_INSTANT_TRADE_PAY\"}",
		order.OrderNo, strconv.FormatFloat(float64(order.AmountCents)/100, 'f', 2, 64), order.Subject,
	)
	params := map[string]string{
		"app_id":      a.Config.AppID,
		"method":      "alipay.trade.page.pay",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  notifyURL,
		"return_url":  returnURL,
		"biz_content": bizContent,
	}
	signature, err := AlipaySignParams(params, a.privateKey)
	if err != nil {
		return nil, err
	}
	params["sign"] = signature
	gatewayURL := a.Config.GatewayURL
	if !strings.Contains(gatewayURL, "://") {
		gatewayURL = "https://" + gatewayURL
	}
	return &Form{
		Action: strings.TrimRight(gatewayURL, "/") + "/gateway.do",
		Fields: params,
	}, nil
}

// VerifyCallback 验签并解析异步通知; 非 TRADE_SUCCESS 视为失败态返回错误。
func (a *Alipay) VerifyCallback(req *CallbackRequest) (string, string, int64, error) {
	if !a.Config.Configured() {
		return "", "", 0, fmt.Errorf("payment gateway is not configured")
	}
	if req == nil || req.Form == nil {
		return "", "", 0, fmt.Errorf("callback form is missing")
	}
	params := map[string]string{}
	for name, values := range req.Form {
		if len(values) > 0 {
			params[name] = values[0]
		}
	}
	if !AlipayVerifyParams(params, params["sign"], a.publicKey) {
		return "", "", 0, fmt.Errorf("invalid alipay callback signature")
	}
	if params["app_id"] != "" && params["app_id"] != a.Config.AppID {
		return "", "", 0, fmt.Errorf("alipay app_id mismatch")
	}
	if strings.ToUpper(params["trade_status"]) != "TRADE_SUCCESS" {
		return "", "", 0, fmt.Errorf("alipay trade status is not success")
	}
	tradeNo, orderNo := params["trade_no"], params["out_trade_no"]
	if tradeNo == "" || orderNo == "" {
		return "", "", 0, fmt.Errorf("callback missing trade number")
	}
	money, err := strconv.ParseFloat(strings.TrimSpace(params["total_amount"]), 64)
	if err != nil || money <= 0 {
		return "", "", 0, fmt.Errorf("callback amount is invalid")
	}
	return tradeNo, orderNo, int64(math.Round(money * 100)), nil
}
