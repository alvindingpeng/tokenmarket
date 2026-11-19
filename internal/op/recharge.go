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

// 充值/提现汇率的来源标识: fx_source_static 是管理员设置的固定牌价(CNY 存量口径),
// fx_source_identity 是账面币种恒等(rate=1, 无折算); FX 快照路径直接记牌价源名。
const (
	fxSourceStatic   = "static"
	fxSourceIdentity = "identity"
)

// RateProvider 汇率来源(D2 预留远程牌价采集): 1 余额币种(USD) 折多少目标币种。
// v1 只有静态实现, 后续接央行/支付渠道牌价时换实现即可, 订单与入账不动。
type RateProvider interface {
	Rate(ctx context.Context, target string) (float64, error)
}

// StaticRateProvider 充值/提现的汇率提供者: CNY 读设置 recharge_rate(存量口径),
// 其它 FX 币种读 FX 牌价快照(FxRateLookup, 与结算折算同一份牌价)。
type StaticRateProvider struct{}

// Rate 返回 1 USD 折多少 target 币种(兼容 RateProvider 接口的旧签名)。
func (StaticRateProvider) Rate(ctx context.Context, target string) (float64, error) {
	rate, _, _, _, err := StaticRateProvider{}.RateDetail(ctx, target)
	return rate, err
}

// RateDetail 返回汇率及依据: (rate, day, source, id)。
// CNY 先试存量静态设置 recharge_rate(表单类渠道按面值收 CNY, 打款口径换了会直接改变
// 管理员要打的人民币分, 保守不动; source 记 "static", day/id 为空表示无牌价行),
// 设置缺失或非法(<=0)时回退 FX 快照的 CNY 牌价; FX 快照也没有才报错 —— 保证下单永远有价可依。
// 其它 FX 币种直接复用 FxRateLookup 的牌价快照: 与结算折算同源同日, 汇总展示只认一份牌价。
// 牌价缺失时返回错误(可用性优先), 由调用方决定降级 —— 绝不静默按 0 或 1 折算。
func (StaticRateProvider) RateDetail(ctx context.Context, target string) (rate float64, day string, source string, id *uint64, err error) {
	switch model.NormalizeCurrency(target) {
	case "CNY":
		if static := SettingGetFloat(model.SettingKeyRechargeRate); static > 0 {
			return static, "", fxSourceStatic, nil, nil
		}
	case model.LedgerCurrency:
		return 1, "", fxSourceIdentity, nil, nil
	}
	rate, day, err = FxRateLookup(ctx, model.LedgerCurrency, target)
	if err != nil {
		return 0, "", "", nil, err
	}
	// 牌价行 ID: 快照可能来自内存缓存, 按唯一键回查库拿行 ID 供单据钉住来源;
	// 查不到(刚刷新尚未落库的极端并发)按 NULL 处理, 不因审计字段失败拒绝下单。
	var row model.FxRate
	if lookErr := db.GetDB().WithContext(ctx).
		Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, model.NormalizeCurrency(target), day).
		Take(&row).Error; lookErr == nil {
		rowID := row.ID
		id = &rowID
	}
	return rate, day, FxRateSourceName(), id, nil
}

// RateDetailWithID 同 RateDetail, 另返回所依据的 fx_rates 行 ID(静态价/恒等为 nil):
// 充值/提现下单把它钉进单据, 复核时能精确还原当时用的哪一行牌价。
func (StaticRateProvider) RateDetailWithID(ctx context.Context, target string) (rate float64, day, source string, id *uint64, err error) {
	rate, day, source, id, err = StaticRateProvider{}.RateDetail(ctx, target)
	return rate, day, source, id, err
}

// RateDetailCompat 兼容旧四元组调用方(不带 rate id)。
func (StaticRateProvider) RateDetailCompat(ctx context.Context, target string) (rate float64, day, source string, err error) {
	rate, day, source, _, err = StaticRateProvider{}.RateDetail(ctx, target)
	return rate, day, source, err
}

// Convert 把 amount 从 from 币种折算成 to 币种, 返回折算结果与所依据的牌价快照
// (rate=1 from 折多少 to, day=采集日, source=来源, id=fx_rates 行 ID)。asOf 保留参数位:
// 当前一律按最新快照折算, 历史单据以自身持久化的 rate/rate_day/rate_id 为准, 不重算。
// from=to 恒等折算(rate=1, 无牌价行)。牌价缺失返回错误, 绝不静默按 0 或 1 折算。
func Convert(ctx context.Context, from, to string, amount float64, asOf ...time.Time) (converted, rate float64, day, source string, id *uint64, err error) {
	fromN, toN := model.NormalizeCurrency(from), model.NormalizeCurrency(to)
	if fromN == toN {
		return amount, 1, "", fxSourceIdentity, nil, nil
	}
	// FxRateLookup 给出的是 1 USD 折 quote; 任意两币按倒数交叉:
	// 1 from = usdToTo/usdToFrom 单位 to, 与结算折算完全同一口径。
	usdToFrom, dayF, srcF, idF, err := StaticRateProvider{}.RateDetailWithID(ctx, fromN)
	if err != nil {
		return 0, 0, "", "", nil, fmt.Errorf("fx rate %s unavailable: %w", fromN, err)
	}
	usdToTo, dayT, _, idT, err := StaticRateProvider{}.RateDetailWithID(ctx, toN)
	if err != nil {
		return 0, 0, "", "", nil, fmt.Errorf("fx rate %s unavailable: %w", toN, err)
	}
	if usdToFrom <= 0 || usdToTo <= 0 {
		return 0, 0, "", "", nil, fmt.Errorf("fx rate must be positive: %s=%v %s=%v", fromN, usdToFrom, toN, usdToTo)
	}
	rate = usdToTo / usdToFrom
	day, source, id = dayF, srcF, idF
	if toN == model.LedgerCurrency {
		id = idF
	} else if dayT > dayF || (dayT != "" && dayF == "") {
		day, id = dayT, idT
	}
	return amount * rate, rate, day, source, id, nil
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
	RateDay      string       `json:"rate_day,omitempty"`    // 汇率牌价日(YYYYMMDD); CNY 静态价为空
	RateSource   string       `json:"rate_source,omitempty"` // static=设置价, identity=恒等, 其余=牌价源名
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
	rate, day, source, _, _ := StaticRateProvider{}.RateDetail(ctx, "CNY")
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
		RateDay:      day,
		RateSource:   source,
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

// RechargeQuoteDetail 充值报价(带汇率依据): 实付金额(分)换算入账余额与满赠(D6 开关+规则)。
// 返回所用汇率、牌价日、来源与 fx_rates 行 ID: 前端展示"按哪天的牌价折算",
// 下单原样落库, 回调只核金额不重算 credits(Creem 固定档位同样以单上 credits 为准)。
// quoteCurrency 为空按 CNY(存量口径): 报价币种只影响换算与单据快照, 不改变入账 USD 语义。
func RechargeQuoteDetail(ctx context.Context, amountCents int64, quoteCurrency string) (credits, bonus, rate float64, day, source string, id *uint64, err error) {
	if amountCents <= 0 {
		return 0, 0, 0, "", "", nil, fmt.Errorf("amount must be positive")
	}
	quote := model.NormalizeCurrency(quoteCurrency)
	if quote != "CNY" && !model.ValidPriceCurrency(quote) {
		return 0, 0, 0, "", "", nil, fmt.Errorf("unsupported quote currency: %s", quoteCurrency)
	}
	rate, day, source, id, err = StaticRateProvider{}.RateDetail(ctx, quote)
	if err != nil {
		return 0, 0, 0, "", "", nil, err
	}
	amountQuote := float64(amountCents) / 100
	credits = amountQuote / rate
	if bonusOn, _ := strconv.ParseBool(mustSettingString(model.SettingKeyRechargeBonusEnabled)); bonusOn {
		rules, parseErr := model.ParseBonusRules(mustSettingString(model.SettingKeyRechargeBonusRules))
		if parseErr == nil {
			bonus = credits * model.MatchBonusRule(rules, amountQuote) / 100
		}
	}
	return credits, bonus, rate, day, source, id, nil
}

// RechargeQuote 充值报价(旧签名, CNY 口径): 实付金额(分)换算入账余额与满赠。
func RechargeQuote(ctx context.Context, amountCents int64) (credits, bonus, rate float64, err error) {
	credits, bonus, rate, _, _, _, err = RechargeQuoteDetail(ctx, amountCents, "CNY")
	return credits, bonus, rate, err
}

// rechargeOrderNo 生成业务单号: yyyymmddHHMMSS + 6 位随机, 唯一索引兜底。
func rechargeOrderNo() string {
	now := time.Now()
	suffix, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return now.Format("20060102150405") + fmt.Sprintf("%06d", suffix.Int64())
}

// channelPayAmount 计算渠道实付额(渠道币种分): CNY 渠道按面值,
// 其他币种按订单币种汇率换算(与下单快照同一份牌价, 面额永远与快照同源)。
func channelPayAmount(ctx context.Context, gateway pay.PaymentGateway, orderCurrency string, amountCents int64) (int64, string, error) {
	currency := strings.ToUpper(strings.TrimSpace(gateway.ChargeCurrency()))
	if currency == "" || currency == "CNY" {
		return amountCents, "CNY", nil
	}
	if !model.ValidPriceCurrency(currency) {
		return 0, "", fmt.Errorf("unsupported charge currency: %s", currency)
	}
	converted, _, _, _, _, err := Convert(ctx, orderCurrency, currency, float64(amountCents)/100)
	if err != nil {
		return 0, "", fmt.Errorf("rate is unavailable: %w", err)
	}
	return int64(math.Round(converted * 100)), currency, nil
}

// RechargeCreate 创建充值订单并返回收银台跳转; 未配置支付或金额不合规直接拒绝。
// amountCents 是 CNY 分基准(存量兼容口径): 报价按 CNY 静态价折算入账,
// 渠道实付额按渠道币种另记; 下单即锁定汇率快照(rate/rate_day/rate_source/rate_id), 回调不重算。
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
	payCents, payCurrency, err := channelPayAmount(ctx, gateway, "CNY", amountCents)
	if err != nil {
		return nil, nil, err
	}
	credits, bonus, rate, rateDay, rateSource, rateID, err := RechargeQuoteDetail(ctx, amountCents, "CNY")
	if err != nil {
		return nil, nil, err
	}
	// Creem 是商品化档位: 入账以档位配置为准, 金额只作档位匹配; 快照仍按下单时的 CNY 价落库。
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
		Rate:        rate,
		RateDay:     rateDay,
		RateSource:  rateSource,
		RateID:      rateID,
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

// RechargeExportQuery 管理端导出筛选: 全部字段可选, 零值表示不过滤。
type RechargeExportQuery struct {
	UserID uint
	Status string
	From   time.Time // created_at >= from
	To     time.Time // created_at < to
}

// RechargeExport 导出查询: 与列表同口径但无 200 上限(调用方负责上限), created_at 倒序。
func RechargeExport(ctx context.Context, query RechargeExportQuery, limit int) ([]model.RechargeOrder, error) {
	if err := RechargeCloseExpired(ctx); err != nil {
		return nil, err
	}
	statement := db.GetDB().WithContext(ctx).Model(&model.RechargeOrder{})
	if query.UserID != 0 {
		statement = statement.Where("user_id = ?", query.UserID)
	}
	if status := strings.ToLower(strings.TrimSpace(query.Status)); status != "" {
		statement = statement.Where("status = ?", status)
	}
	if !query.From.IsZero() {
		statement = statement.Where("created_at >= ?", query.From)
	}
	if !query.To.IsZero() {
		statement = statement.Where("created_at < ?", query.To)
	}
	orders := []model.RechargeOrder{}
	if err := statement.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("failed to export recharge orders: %w", err)
	}
	return orders, nil
}

// RechargeExportCount 同条件的命中总数(导出截断提示用)。
func RechargeExportCount(ctx context.Context, query RechargeExportQuery, total *int64) error {
	statement := db.GetDB().WithContext(ctx).Model(&model.RechargeOrder{})
	if query.UserID != 0 {
		statement = statement.Where("user_id = ?", query.UserID)
	}
	if status := strings.ToLower(strings.TrimSpace(query.Status)); status != "" {
		statement = statement.Where("status = ?", status)
	}
	if !query.From.IsZero() {
		statement = statement.Where("created_at >= ?", query.From)
	}
	if !query.To.IsZero() {
		statement = statement.Where("created_at < ?", query.To)
	}
	if err := statement.Count(total).Error; err != nil {
		return fmt.Errorf("failed to count recharge export: %w", err)
	}
	return nil
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
