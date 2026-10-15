package op

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/alert"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/pay"
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
	gatewayURL, _ := SettingGetString(model.SettingKeyEpayGatewayURL)
	pid, _ := SettingGetString(model.SettingKeyEpayPID)
	key, _ := SettingGetString(model.SettingKeyEpayKey)
	payTypes, _ := SettingGetString(model.SettingKeyEpayPayTypes)
	return pay.EpayConfig{GatewayURL: gatewayURL, PID: pid, Key: key, PayTypes: payTypes}
}

// RechargeView 充值配置视图: 弹窗据是否已配置决定展示充值入口还是"支付未配置"。
type RechargeView struct {
	Configured   bool      `json:"configured"`
	Rate         float64   `json:"rate"`
	MinCNY       float64   `json:"min_cny"`
	AmountTiers  []float64 `json:"amount_tiers"`
	BonusEnabled bool      `json:"bonus_enabled"`
	PayTypes     []string  `json:"pay_types"` // 支付方式白名单; 空表示收银台自选全开放
}

// RechargeView 返回充值配置视图。
func RechargeViewGet(ctx context.Context) RechargeView {
	rate, _ := StaticRateProvider{}.Rate(ctx, "CNY")
	minCNY := SettingGetFloat(model.SettingKeyRechargeMinCNY)
	tiers, _ := model.ParseAmountTiers(mustSettingString(model.SettingKeyRechargeAmountTiers))
	bonusOn, _ := strconv.ParseBool(mustSettingString(model.SettingKeyRechargeBonusEnabled))
	var payTypes []string
	for _, item := range strings.Split(mustSettingString(model.SettingKeyEpayPayTypes), ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			payTypes = append(payTypes, item)
		}
	}
	return RechargeView{
		Configured:   epayConfig().Configured(),
		Rate:         rate,
		MinCNY:       minCNY,
		AmountTiers:  tiers,
		BonusEnabled: bonusOn,
		PayTypes:     payTypes,
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

// RechargeCreate 创建充值订单并返回收银台表单; 未配置支付或金额不合规直接拒绝。
func RechargeCreate(ctx context.Context, userID uint, amountCents int64, method, notifyURL, returnURL string) (*model.RechargeOrder, *pay.Form, error) {
	config := epayConfig()
	if !config.Configured() {
		return nil, nil, fmt.Errorf("payment gateway is not configured")
	}
	minCNY := SettingGetFloat(model.SettingKeyRechargeMinCNY)
	if amountCents < int64(minCNY*100) {
		return nil, nil, fmt.Errorf("amount is below the minimum recharge amount")
	}
	credits, bonus, _, err := RechargeQuote(ctx, amountCents)
	if err != nil {
		return nil, nil, err
	}
	order := &model.RechargeOrder{
		OrderNo:     rechargeOrderNo(),
		UserID:      userID,
		Provider:    model.PayProviderEpay,
		Method:      strings.ToLower(strings.TrimSpace(method)),
		AmountCents: amountCents,
		Currency:    "CNY",
		Credits:     credits,
		Bonus:       bonus,
		Subject:     "余额充值",
		Status:      model.RechargeStatusPending,
		ExpiredAt:   time.Now().Add(rechargeOrderTTL),
	}
	if err := db.GetDB().WithContext(ctx).Create(order).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to create recharge order: %w", err)
	}
	form, err := pay.NewEpay(config).CreatePayment(order, notifyURL, returnURL)
	if err != nil {
		return nil, nil, err
	}
	return order, form, nil
}

// RechargeNotify 支付回调: 验签、核金额、条件更新抢占入账, 重复投递天然幂等。
func RechargeNotify(ctx context.Context, query url.Values) error {
	config := epayConfig()
	tradeNo, orderNo, amountCents, err := pay.NewEpay(config).VerifyCallback(query)
	if err != nil {
		return err
	}
	if strings.ToUpper(query.Get("trade_status")) != "TRADE_SUCCESS" {
		return fmt.Errorf("trade status is not success")
	}
	raw := query.Encode()
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
		if amountCents != order.AmountCents {
			return fmt.Errorf("amount mismatch: order %d, callback %d", order.AmountCents, amountCents)
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
	return nil
}

// RechargeVerifyReturn 校验浏览器回跳签名; 只用于决定提示语, 入账一律以 notify 为准。
func RechargeVerifyReturn(query url.Values) (string, string, int64, error) {
	return pay.NewEpay(epayConfig()).VerifyCallback(query)
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
