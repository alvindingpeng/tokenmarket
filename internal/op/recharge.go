package op

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/pay"
	"gorm.io/gorm"
)

// rechargeOrderTTL 待支付订单的存活时间, 超时自动关闭。
const rechargeOrderTTL = 30 * time.Minute

// RateProvider 汇率来源(D2 预留远程牌价采集): 1 余额币种(USD) 折多少目标币种。
// v1 只有静态实现, 后续接央行/支付渠道牌价时换实现即可, 订单与入账不动。
type RateProvider interface {
	Rate(ctx context.Context, target string) (float64, error)
}

// StaticRateProvider 读设置 recharge_rate 的静态汇率。
type StaticRateProvider struct{}

// Rate 返回 1 USD 折多少 target 币种; 目前只支持 CNY。
func (StaticRateProvider) Rate(_ context.Context, target string) (float64, error) {
	if !strings.EqualFold(target, "CNY") {
		return 0, fmt.Errorf("unsupported currency: %s", target)
	}
	rate := SettingGetFloat(model.SettingKeyRechargeRate)
	if rate <= 0 {
		rate = 7.0
	}
	return rate, nil
}

// epayConfig 从后台设置读取易支付参数; 商户信息后配即可用。
func epayConfig() pay.EpayConfig {
	return pay.EpayConfig{
		GatewayURL: mustSettingString(model.SettingKeyEpayGatewayURL),
		PID:        mustSettingString(model.SettingKeyEpayPID),
		Key:        mustSettingString(model.SettingKeyEpayKey),
		PayTypes:   mustSettingString(model.SettingKeyEpayPayTypes),
	}
}

// alipayConfig 从后台设置读取支付宝参数。
func alipayConfig() pay.AlipayConfig {
	return pay.AlipayConfig{
		GatewayURL: mustSettingString(model.SettingKeyAlipayGatewayURL),
		AppID:      mustSettingString(model.SettingKeyAlipayAppID),
		PrivateKey: mustSettingString(model.SettingKeyAlipayPrivateKey),
		PublicKey:  mustSettingString(model.SettingKeyAlipayPublicKey),
	}
}

// stripeConfig 从后台设置读取 Stripe 参数。
func stripeConfig() pay.StripeConfig {
	return pay.StripeConfig{
		SecretKey:     mustSettingString(model.SettingKeyStripeSecretKey),
		WebhookSecret: mustSettingString(model.SettingKeyStripeWebhook),
		APIBase:       mustSettingString(model.SettingKeyStripeAPIBase),
		Currency:      mustSettingString(model.SettingKeyStripeCurrency),
	}
}

// creemConfig 从后台设置读取 Creem 参数(含充值档位)。
func creemConfig() pay.CreemConfig {
	products, _ := model.ParseCreemProducts(mustSettingString(model.SettingKeyCreemProducts))
	return pay.CreemConfig{
		APIKey:        mustSettingString(model.SettingKeyCreemAPIKey),
		WebhookSecret: mustSettingString(model.SettingKeyCreemWebhook),
		APIBase:       mustSettingString(model.SettingKeyCreemAPIBase),
		Products:      products,
	}
}

// gatewayFor 按渠道取网关; 空渠道默认易支付, 未配置或未知渠道直接拒绝。
func gatewayFor(provider string) (pay.PaymentGateway, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case pay.ProviderEpay, "":
		config := epayConfig()
		if !config.Configured() {
			return nil, fmt.Errorf("payment gateway is not configured")
		}
		return pay.NewEpay(config), nil
	case pay.ProviderAlipay:
		config := alipayConfig()
		if !config.Configured() {
			return nil, fmt.Errorf("alipay is not configured")
		}
		gateway, err := pay.NewAlipay(config)
		if err != nil {
			return nil, err
		}
		return gateway, nil
	case pay.ProviderStripe:
		config := stripeConfig()
		if !config.Configured() {
			return nil, fmt.Errorf("stripe is not configured")
		}
		return pay.NewStripe(config), nil
	case pay.ProviderCreem:
		config := creemConfig()
		if !config.Configured() {
			return nil, fmt.Errorf("creem is not configured")
		}
		return pay.NewCreem(config), nil
	default:
		return nil, fmt.Errorf("unsupported payment provider: %s", provider)
	}
}

// PayChannel 渠道视图(对齐 New API 的 pay_methods): 前端按配置状态决定可选渠道。
type PayChannel struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Configured bool     `json:"configured"`
	Currency   string   `json:"currency"`
	PayTypes   []string `json:"pay_types,omitempty"`
}

// RechargeView 充值配置视图: 弹窗据是否已配置决定展示充值入口还是"支付未配置"。
type RechargeView struct {
	Configured   bool         `json:"configured"`
	Rate         float64      `json:"rate"`
	MinCNY       float64      `json:"min_cny"`
	AmountTiers  []float64    `json:"amount_tiers"`
	BonusEnabled bool         `json:"bonus_enabled"`
	PayTypes     []string     `json:"pay_types"` // 易支付方式白名单; 空表示收银台自选全开放
	Channels     []PayChannel `json:"channels"`
}

// channelViews 汇总各渠道的配置状态; 名称即渠道展示名。
func channelViews() []PayChannel {
	epay := epayConfig()
	channels := []PayChannel{{ID: pay.ProviderEpay, Name: "易支付", Configured: epay.Configured(), Currency: "CNY"}}
	var payTypes []string
	for _, item := range strings.Split(epay.PayTypes, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			payTypes = append(payTypes, item)
		}
	}
	channels[0].PayTypes = payTypes
	alipay := alipayConfig()
	channels = append(channels, PayChannel{ID: pay.ProviderAlipay, Name: "支付宝", Configured: alipay.Configured(), Currency: "CNY"})
	stripe := stripeConfig()
	channels = append(channels, PayChannel{ID: pay.ProviderStripe, Name: "Stripe", Configured: stripe.Configured(), Currency: strings.ToUpper(strings.TrimSpace(stripe.Currency))})
	creem := creemConfig()
	channels = append(channels, PayChannel{ID: pay.ProviderCreem, Name: "Creem", Configured: creem.Configured(), Currency: "CNY"})
	return channels
}

// RechargeView 返回充值配置视图。
func RechargeViewGet(ctx context.Context) RechargeView {
	rate, _ := StaticRateProvider{}.Rate(ctx, "CNY")
	minCNY := SettingGetFloat(model.SettingKeyRechargeMinCNY)
	tiers, _ := model.ParseAmountTiers(mustSettingString(model.SettingKeyRechargeAmountTiers))
	bonusOn, _ := strconv.ParseBool(mustSettingString(model.SettingKeyRechargeBonusEnabled))
	channels := channelViews()
	configured := false
	for _, channel := range channels {
		if channel.Configured {
			configured = true
		}
	}
	return RechargeView{
		Configured:   configured,
		Rate:         rate,
		MinCNY:       minCNY,
		AmountTiers:  tiers,
		BonusEnabled: bonusOn,
		PayTypes:     channels[0].PayTypes,
		Channels:     channels,
	}
}

// mustSettingString 读设置, 读不到按空串处理。
func mustSettingString(key model.SettingKey) string {
	value, _ := SettingGetString(key)
	return value
}

// RechargeQuote 充值报价: 实付金额(分)换算入账余额与满赠(D6 开关+规则)。
func RechargeQuote(ctx context.Context, amountCents int64) (credits, bonus, rate float64, err error) {
	if amountCents <= 0 {
		return 0, 0, 0, fmt.Errorf("amount must be positive")
	}
	rate, err = StaticRateProvider{}.Rate(ctx, "CNY")
	if err != nil {
		return 0, 0, 0, err
	}
	amountCNY := float64(amountCents) / 100
	credits = amountCNY / rate
	if bonusOn, _ := strconv.ParseBool(mustSettingString(model.SettingKeyRechargeBonusEnabled)); bonusOn {
		rules, parseErr := model.ParseBonusRules(mustSettingString(model.SettingKeyRechargeBonusRules))
		if parseErr == nil {
			bonus = credits * model.MatchBonusRule(rules, amountCNY) / 100
		}
	}
	return credits, bonus, rate, nil
}

// rechargeOrderNo 生成业务单号: yyyymmddHHMMSS + 6 位随机, 唯一索引兜底。
func rechargeOrderNo() string {
	now := time.Now()
	suffix, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return now.Format("20060102150405") + fmt.Sprintf("%06d", suffix.Int64())
}

// channelPayAmount 计算渠道实付额(渠道币种分): CNY 渠道按面值,
// 其他币种按汇率快照换算(目前支持 USD, 与余额币种同源)。
func channelPayAmount(ctx context.Context, gateway pay.PaymentGateway, amountCents int64) (int64, string, error) {
	currency := strings.ToUpper(strings.TrimSpace(gateway.ChargeCurrency()))
	if currency == "" || currency == "CNY" {
		return amountCents, "CNY", nil
	}
	if currency != "USD" {
		return 0, "", fmt.Errorf("unsupported charge currency: %s", currency)
	}
	rate, err := StaticRateProvider{}.Rate(ctx, "CNY")
	if err != nil || rate <= 0 {
		return 0, "", fmt.Errorf("rate is unavailable")
	}
	return int64(math.Round(float64(amountCents) / 100 / rate * 100)), currency, nil
}

// RechargeCreate 创建充值订单并返回收银台跳转; 未配置支付或金额不合规直接拒绝。
// amountCents 是 CNY 分基准(展示与入账口径), 渠道实付额按渠道币种另记。
func RechargeCreate(ctx context.Context, userID uint, amountCents int64, provider, method, notifyURL, returnURL string) (*model.RechargeOrder, *pay.Form, error) {
	gateway, err := gatewayFor(provider)
	if err != nil {
		return nil, nil, err
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = pay.ProviderEpay
	}
	minCNY := SettingGetFloat(model.SettingKeyRechargeMinCNY)
	if amountCents < int64(minCNY*100) {
		return nil, nil, fmt.Errorf("amount is below the minimum recharge amount")
	}
	payCents, payCurrency, err := channelPayAmount(ctx, gateway, amountCents)
	if err != nil {
		return nil, nil, err
	}
	credits, bonus, _, err := RechargeQuote(ctx, amountCents)
	if err != nil {
		return nil, nil, err
	}
	// Creem 是商品化档位: 入账以档位配置为准, 金额只作档位匹配。
	if provider == pay.ProviderCreem {
		creem := pay.NewCreem(creemConfig())
		product, ok := creem.ProductFor(amountCents)
		if !ok {
			return nil, nil, fmt.Errorf("no creem product matches this amount")
		}
		credits, bonus = product.Credits, 0
		payCents = 0 // 商品定价, 回调不核金额只核商品
	}
	order := &model.RechargeOrder{
		OrderNo:     rechargeOrderNo(),
		UserID:      userID,
		Provider:    provider,
		Method:      strings.ToLower(strings.TrimSpace(method)),
		AmountCents: amountCents,
		Currency:    "CNY",
		PayCents:    payCents,
		PayCurrency: payCurrency,
		Credits:     credits,
		Bonus:       bonus,
		Subject:     "余额充值",
		Status:      model.RechargeStatusPending,
		ExpiredAt:   time.Now().Add(rechargeOrderTTL),
	}
	if err := db.GetDB().WithContext(ctx).Create(order).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to create recharge order: %w", err)
	}
	// 回跳带上业务单号, 无签名回跳渠道(Stripe/Creem)据单号查状态给提示。
	separator := "?"
	if strings.Contains(returnURL, "?") {
		separator = "&"
	}
	form, err := gateway.CreatePayment(order, notifyURL, returnURL+separator+"order_no="+order.OrderNo)
	if err != nil {
		return nil, nil, err
	}
	return order, form, nil
}

// RechargeNotify 支付回调: 按渠道验签、核金额、条件更新抢占入账, 重复投递天然幂等。
func RechargeNotify(ctx context.Context, provider string, req *pay.CallbackRequest) error {
	gateway, err := gatewayFor(provider)
	if err != nil {
		return err
	}
	tradeNo, orderNo, amountCents, err := gateway.VerifyCallback(req)
	if err != nil {
		return err
	}
	raw := ""
	if req != nil {
		if len(req.Body) > 0 {
			raw = string(req.Body)
		} else if req.Form != nil {
			raw = req.Form.Encode()
		}
	}
	var order model.RechargeOrder
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("order_no = ?", orderNo).Take(&order).Error; err != nil {
			return fmt.Errorf("order not found")
		}
		if order.Status == model.RechargeStatusPaid {
			// 幂等: 已入账的订单重复投递直接算成功, 绝不二次入账。
			return nil
		}
		if order.Status != model.RechargeStatusPending {
			return fmt.Errorf("order is %s", order.Status)
		}
		// 金额核对按渠道实付额; 商品定价渠道(PayCents=0)只核商品不核金额。
		if order.PayCents > 0 && amountCents != order.PayCents {
			return fmt.Errorf("amount mismatch: order %d, callback %d", order.PayCents, amountCents)
		}
		// 抢占: 状态条件更新, 并发重复回调至多一个事务改到行。
		result := tx.Model(&model.RechargeOrder{}).
			Where("order_no = ? AND status = ?", orderNo, model.RechargeStatusPending).
			Updates(map[string]any{
				"status":     model.RechargeStatusPaid,
				"trade_no":   tradeNo,
				"paid_at":    time.Now(),
				"notify_raw": raw,
			})
		if result.Error != nil {
			return fmt.Errorf("failed to mark order paid: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return nil
		}
		// 入账拆两笔: 充值额进累计充值口径, 满赠只进余额, 口径不会混。
		if order.Credits > 0 {
			if err := walletCreditTx(tx, order.UserID, order.Credits, model.LedgerKindTopup, "充值 "+order.OrderNo, 0, ""); err != nil {
				return err
			}
		}
		if order.Bonus > 0 {
			if err := walletCreditTx(tx, order.UserID, order.Bonus, model.LedgerKindBonus, "充值满赠 "+order.OrderNo, 0, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// 金额不符是资损风险信号: 拒绝入账并告警, 通知原文已在回调参数里。
		if strings.HasPrefix(err.Error(), "amount mismatch") {
			RaiseAlertFacts(ctx, AlertFacts{
				Kind:   "payment_amount_mismatch",
				Reason: "callback amount does not match order",
				Fields: []alert.Field{
					{Label: "订单号", Value: orderNo},
					{Label: "第三方流水", Value: tradeNo},
					{Label: "错误", Value: err.Error()},
					{Label: "影响", Value: "该笔不入账; 若第三方确已扣款需人工补单核对"},
				},
			})
		}
		return err
	}
	UserTouchBalance(order.UserID)
	// 充值到账通知: 在入账提交之后发, 失败只记日志, 绝不影响已入账的资金事实。
	notifyAsync(ctx, order.UserID, model.NotificationTypeRecharge, "充值成功",
		fmt.Sprintf("您的账户已成功充值 %.2f 元，订单号：%s。", order.Credits+order.Bonus, order.OrderNo),
		"", 0)
	return nil
}

// RechargeVerifyReturn 校验浏览器回跳签名(表单类渠道); 只用于决定提示语, 入账一律以 notify 为准。
func RechargeVerifyReturn(provider string, req *pay.CallbackRequest) (string, string, int64, error) {
	gateway, err := gatewayFor(provider)
	if err != nil {
		return "", "", 0, err
	}
	return gateway.VerifyCallback(req)
}

// RechargeOrderStatus 查订单状态(无签名回跳渠道据单号给结果提示)。
func RechargeOrderStatus(ctx context.Context, orderNo string) string {
	var order model.RechargeOrder
	if err := db.GetDB().WithContext(ctx).Where("order_no = ?", orderNo).Take(&order).Error; err != nil {
		return ""
	}
	return order.Status
}

// RechargeCloseExpired 关闭超时未支付订单; 下单与列表时懒执行, 不额外起定时器。
func RechargeCloseExpired(ctx context.Context) error {
	result := db.GetDB().WithContext(ctx).Model(&model.RechargeOrder{}).
		Where("status = ? AND expired_at < ?", model.RechargeStatusPending, time.Now()).
		Update("status", model.RechargeStatusExpired)
	if result.Error != nil {
		return fmt.Errorf("failed to close expired orders: %w", result.Error)
	}
	return nil
}

// RechargeList 查询充值订单; userID 非零查指定用户, 为零查全部(管理员)。
func RechargeList(ctx context.Context, userID uint, limit int) ([]model.RechargeOrder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if err := RechargeCloseExpired(ctx); err != nil {
		return nil, err
	}
	statement := db.GetDB().WithContext(ctx).Model(&model.RechargeOrder{})
	if userID != 0 {
		statement = statement.Where("user_id = ?", userID)
	}
	orders := []model.RechargeOrder{}
	if err := statement.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("failed to list recharge orders: %w", err)
	}
	return orders, nil
}

// RechargeManualComplete 人工补单: 第三方已扣款但回调丢失时由管理员补入账。
// 与回调同一条抢占+入账路径, 不会因为手工操作出现双入账。
func RechargeManualComplete(ctx context.Context, orderNo string, actorID uint, actorName string) error {
	var order model.RechargeOrder
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("order_no = ?", orderNo).Take(&order).Error; err != nil {
			return fmt.Errorf("order not found")
		}
		if order.Status == model.RechargeStatusPaid {
			return fmt.Errorf("order is already paid")
		}
		if order.Status != model.RechargeStatusPending {
			return fmt.Errorf("order is %s", order.Status)
		}
		result := tx.Model(&model.RechargeOrder{}).
			Where("order_no = ? AND status = ?", orderNo, model.RechargeStatusPending).
			Updates(map[string]any{"status": model.RechargeStatusPaid, "paid_at": time.Now()})
		if result.Error != nil {
			return fmt.Errorf("failed to mark order paid: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("order is already paid")
		}
		if order.Credits > 0 {
			if err := walletCreditTx(tx, order.UserID, order.Credits, model.LedgerKindTopup, "人工补单 "+order.OrderNo, actorID, actorName); err != nil {
				return err
			}
		}
		if order.Bonus > 0 {
			if err := walletCreditTx(tx, order.UserID, order.Bonus, model.LedgerKindBonus, "人工补单满赠 "+order.OrderNo, actorID, actorName); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	UserTouchBalance(order.UserID)
	return nil
}
