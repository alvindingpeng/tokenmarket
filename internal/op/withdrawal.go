package op

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// WithdrawSummary 提现口径: 可提 = 成熟收益 - 累计已提 - 提现中冻结。
// 成熟收益 = 冷却期外且未退款的累计收益; 冷却关闭时等于 TotalRevenue(池子已净扣退款)。
//
// 币种有两个, 别混用:
//   - PoolCurrency 是**金额币种**: Available/Frozen/Withdrawn/Revenue/FeeMin/MinAmount 的单位,
//     恒等于 model.LedgerCurrency(USD)。前端标注金额单位只能用这个字段。
//   - Currency 是**汇率目标币种**: rate(=1 USD 折多少 Currency)的计价对象, 恒为 CNY。
//     它跟上面那几个金额没有关系 —— 拿它标注"可提余额"会把 USD 读成 CNY, 差约 7 倍。
type WithdrawSummary struct {
	Available    float64 `json:"available"`     // 可提余额(收益池口径, 单位见 pool_currency)
	Frozen       float64 `json:"frozen"`        // 提现中冻结(同上)
	Withdrawn    float64 `json:"withdrawn"`     // 累计已提(同上)
	Revenue      float64 `json:"revenue"`       // 累计收益(同上)
	Rate         float64 `json:"rate"`          // 汇率快照: 1 PoolCurrency = ? Currency
	Currency     string  `json:"currency"`      // 仅 rate 的目标币种(恒 CNY); **不要**用它标注金额单位, 用 pool_currency
	PoolCurrency string  `json:"pool_currency"` // 金额币种: 上面四个金额与 fee_min/min_amount 的单位, 恒为 model.LedgerCurrency
	RateDay      string  `json:"rate_day"`      // 牌价采集日 YYYYMMDD; CNY 静态价为空
	RateSource   string  `json:"rate_source"`   // static=设置价, identity=恒等, 其余=牌价源名
	FeeRate      float64 `json:"fee_rate"`
	FeeMin       float64 `json:"fee_min"`
	MinAmount    float64 `json:"min_amount"`
	CanApply     bool    `json:"can_apply"` // 角色是否允许提现(D4: 仅渠道商/管理员)
	// SupportedCurrencies 可选的到账币种白名单(CNY 在首): 前端据此渲染 payout 下拉, 空表示仅 CNY。
	// 字段名与前端 supported_currencies 对齐(复数), 一次到位避免双命名兼容。
	SupportedCurrencies []string `json:"supported_currencies,omitempty"`
}

// WithdrawSpec 提现申请参数。
type WithdrawSpec struct {
	UserID         uint
	Role           model.Role
	Amount         float64
	Method         string
	AccountName    string
	AccountNo      string
	PayoutCurrency string // 到账币种(可选); 空=CNY 存量口径, 非法币种直接拒绝
}

// withdrawRules 提现规则(全部 settings 可配)。
type withdrawRules struct {
	minAmount        float64
	feeRate          float64
	feeMin           float64
	cooldownDays     int
	dailyCountLimit  int
	dailyAmountLimit float64
	accountLockDays  int
}

// withdrawRulesGet 读提现规则; 读不到按默认值处理(设置缺失不阻断)。
func withdrawRulesGet() withdrawRules {
	rules := withdrawRules{
		minAmount:        SettingGetFloat(model.SettingKeyWithdrawalMinAmount),
		feeRate:          SettingGetFloat(model.SettingKeyWithdrawalFeeRate),
		feeMin:           SettingGetFloat(model.SettingKeyWithdrawalFeeMin),
		dailyAmountLimit: SettingGetFloat(model.SettingKeyWithdrawalDailyAmountLimit),
	}
	if rules.minAmount <= 0 {
		rules.minAmount = 50
	}
	if rules.feeRate <= 0 {
		rules.feeRate = 0.01
	}
	if rules.feeMin <= 0 {
		rules.feeMin = 1
	}
	rules.cooldownDays = int(SettingGetFloat(model.SettingKeyWithdrawalCooldownDays))
	rules.dailyCountLimit = int(SettingGetFloat(model.SettingKeyWithdrawalDailyCountLimit))
	rules.accountLockDays = int(SettingGetFloat(model.SettingKeyWithdrawalAccountLockDays))
	return rules
}

// maturedRevenue 冷却期外的累计收益: SUM(owner_revenue) 只统计 cutoff 之前结算且未退款的记录。
// 已退款账单必须排除: 退款批准时 total_revenue 已冲回, 若退款行仍计进 matured, 冷却中的新收入
// 会被"退款行的虚高 matured"顶到 total_revenue 上沿而提前放出(提现先于成熟)。
// cooldownDays<=0 时直接用 TotalRevenue(恒等口径, 不查表; total_revenue 本身已净扣退款)。
// 展示口径(WithdrawSummaryGet)走这里; 冻结判定必须用事务内的 maturedRevenueIn。
func maturedRevenue(ctx context.Context, userID uint, cooldownDays int, totalRevenue float64) (float64, error) {
	return maturedRevenueIn(db.GetDB().WithContext(ctx), userID, cooldownDays, totalRevenue)
}

// maturedRevenueIn 同 maturedRevenue, 但在给定查询句柄上执行 —— 冻结判定要传事务内的 tx,
// 让成熟收益与 total_revenue 看到同一时刻的账(锁内重算, 不用事务外的陈旧快照)。
func maturedRevenueIn(q *gorm.DB, userID uint, cooldownDays int, totalRevenue float64) (float64, error) {
	if cooldownDays <= 0 {
		return totalRevenue, nil
	}
	cutoff := time.Now().AddDate(0, 0, -cooldownDays)
	var matured float64
	if err := q.Model(&model.BillingRecord{}).
		Where("owner_id = ? AND created_at <= ? AND COALESCE(status, '') <> ?", userID, cutoff, model.BillingStatusRefunded).
		Select("COALESCE(SUM(owner_revenue), 0)").Scan(&matured).Error; err != nil {
		return 0, fmt.Errorf("failed to calculate matured revenue: %w", err)
	}
	// 并上已固化的日汇总(只计未退款部分): 明细按留存期清理后, 超期那部分收益只能从汇总表读,
	// 否则"可提现额度"会随清理缩水 —— 用户已经赚到的钱不能因为系统清理旧记录就消失。
	// 汇总表带日期, 所以 created_at <= cutoff 这个"只有成熟的部分才可提"的语义仍然成立。
	//
	// 汇总表的 day 是 YYYYMMDD 定长字符串, 与 cutoff 的同类格式做字典序比较等价于日期比较,
	// 三种数据库通用 —— 不引入 STR_TO_DATE/DATE() 这类只在单一方言里存在的函数。
	var rollupMatured float64
	if err := q.Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day <= ?", model.BillingRollupOwner, userID, cutoff.Format("20060102")).
		Select("COALESCE(SUM(owner_revenue - refunded_owner_revenue), 0)").Scan(&rollupMatured).Error; err != nil {
		return 0, fmt.Errorf("failed to calculate matured rollup revenue: %w", err)
	}
	matured += rollupMatured
	// 兜底上界: 成熟收益不该超过收益池总额(历史数据/人工调账偏差时按池子封顶, 不多放钱)。
	if matured > totalRevenue {
		matured = totalRevenue
	}
	return matured, nil
}

// canWithdraw 角色门(D4): 只有渠道商与管理员可动收益池, 普通用户余额不可提现。
func canWithdraw(role model.Role) bool {
	return role == model.RoleReseller || role == model.RoleAdmin
}

// WithdrawSummaryGet 返回提现口径与规则。
func WithdrawSummaryGet(ctx context.Context, userID uint, role model.Role) (*WithdrawSummary, error) {
	var user model.User
	if err := db.GetDB().WithContext(ctx).First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}
	rules := withdrawRulesGet()
	matured, err := maturedRevenue(ctx, userID, rules.cooldownDays, user.TotalRevenue)
	if err != nil {
		return nil, err
	}
	available := matured - user.TotalWithdrawn - user.WithdrawFrozen
	if available < 0 {
		available = 0
	}
	rate, rateDay, rateSource, _, _ := StaticRateProvider{}.RateDetail(ctx, "CNY")
	return &WithdrawSummary{
		Available: available,
		Frozen:    user.WithdrawFrozen,
		Withdrawn: user.TotalWithdrawn,
		Revenue:   user.TotalRevenue,
		Rate:      rate,
		// 汇率目标币种保持 CNY。**不能**把它改成 USD 图省事: rate/Currency 是一对
		// ("1 USD 折 rate 个 Currency"), 也是提现单 apply 时钉进 Withdrawal.Currency 的快照口径
		// (model/withdrawal.go 里旧行默认 CNY, 读取侧空串回落 CNY 静态价)。把它们改成 USD
		// 会让存量单与 rate=7 的语义互相矛盾(rate 变成 1 USD 折 1 USD), 提现单的
		// actual_cents 换算也跟着错。要标金额单位, 用下面新增的 PoolCurrency。
		Currency:            "CNY",
		PoolCurrency:        model.LedgerCurrency,
		RateDay:             rateDay,
		RateSource:          rateSource,
		FeeRate:             rules.feeRate,
		FeeMin:              rules.feeMin,
		MinAmount:           rules.minAmount,
		CanApply:            canWithdraw(role),
		SupportedCurrencies: model.SupportedPayoutCurrencies(),
	}, nil
}

// WithdrawApply 提现申请: 规则校验 → 收益池条件冻结 → 建单 → 流水。
// 冻结是 SQL 条件更新(含冷却期的不可提部分), 并发重复申请至多一笔成。
// 成熟收益在事务内行写锁之后重算: 结算入账/退款冲回与本申请并发时, 冻结判定用的永远是
// 当前账, 不是事务外读到的陈旧快照(快照会把冷却中的新收入提前放出来, 或误杀成熟收入)。
func WithdrawApply(ctx context.Context, spec WithdrawSpec) (*model.Withdrawal, error) {
	if !canWithdraw(spec.Role) {
		return nil, fmt.Errorf("only reseller revenue is withdrawable")
	}
	rules := withdrawRulesGet()
	if spec.Amount < rules.minAmount {
		return nil, fmt.Errorf("amount is below the withdrawal minimum")
	}
	method := strings.ToLower(strings.TrimSpace(spec.Method))
	if method != model.WithdrawMethodAlipay && method != model.WithdrawMethodWechat && method != model.WithdrawMethodBank {
		return nil, fmt.Errorf("unsupported withdrawal method")
	}
	accountName := strings.TrimSpace(spec.AccountName)
	accountNo := strings.TrimSpace(spec.AccountNo)
	if accountName == "" || accountNo == "" {
		return nil, fmt.Errorf("withdrawal account is required")
	}
	fee := model.WithdrawFee(spec.Amount, rules.feeRate, rules.feeMin)
	actual := spec.Amount - fee
	if actual <= 0 {
		return nil, fmt.Errorf("amount is too small to cover the fee")
	}
	// 到账币种: 空=CNY(存量口径); 非白名单币种直接拒绝, 绝不把未知代码静默折算。
	//
	// 判定顺序必须"先去空白 → 空则 CNY → 再归一/校验", 不能先用 NormalizeCurrency:
	// 它的语义是"空=账面币种 USD"(被余额/充值/牌价多处依赖, 这里不许改它), 于是
	// NormalizeCurrency("") 返回 USD, 后面那句 `if payout == ""` 就成了永不执行的死代码 ——
	// 省略 payout_currency 的调用方会拿到 Currency="USD"/rate=1 的快照, 而接口文档与
	// JSON tag 都宣称缺省 CNY, 下游按 currency 判「旧行/新行」「是否 CNY 到账」全部误判。
	// 无资损(rate=1 时 actual_cents 数值等价), 但快照口径错就是错。
	payout := strings.ToUpper(strings.TrimSpace(spec.PayoutCurrency))
	if payout == "" {
		payout = "CNY"
	}
	if !model.ValidPriceCurrency(payout) {
		return nil, fmt.Errorf("unsupported payout currency: %s", spec.PayoutCurrency)
	}
	rate, rateDay, rateSource, rateID, err := StaticRateProvider{}.RateDetail(ctx, payout)
	if err != nil {
		return nil, err
	}
	// 单日限额: 统计当日仍有效(未取消/未拒绝)的申请。
	if rules.dailyCountLimit > 0 || rules.dailyAmountLimit > 0 {
		today := time.Now().Format("2006-01-02")
		var count int64
		var sum float64
		statement := db.GetDB().WithContext(ctx).Model(&model.Withdrawal{}).
			Where("user_id = ? AND status IN ? AND applied_at >= ?", spec.UserID,
				[]model.WithdrawStatus{model.WithdrawStatusPending, model.WithdrawStatusApproved, model.WithdrawStatusPaid}, today)
		if err := statement.Count(&count).Error; err != nil {
			return nil, fmt.Errorf("failed to count today withdrawals: %w", err)
		}
		if err := statement.Select("COALESCE(SUM(amount), 0)").Scan(&sum).Error; err != nil {
			return nil, fmt.Errorf("failed to sum today withdrawals: %w", err)
		}
		if rules.dailyCountLimit > 0 && int(count) >= rules.dailyCountLimit {
			return nil, fmt.Errorf("daily withdrawal count limit reached")
		}
		if rules.dailyAmountLimit > 0 && sum+spec.Amount > rules.dailyAmountLimit {
			return nil, fmt.Errorf("daily withdrawal amount limit reached")
		}
	}
	// 账户锁定: 锁定期内换收款账户直接拒绝(防盗号改卡)。
	if rules.accountLockDays > 0 {
		var last model.Withdrawal
		err := db.GetDB().WithContext(ctx).Where("user_id = ?", spec.UserID).
			Order("id DESC").Limit(1).Take(&last).Error
		if err == nil && last.ID != 0 && !strings.EqualFold(last.AccountNo, accountNo) {
			if time.Since(last.AppliedAt) < time.Duration(rules.accountLockDays)*24*time.Hour {
				return nil, fmt.Errorf("withdrawal account is locked")
			}
		}
	}
	order := &model.Withdrawal{
		UserID: spec.UserID,
		Amount: spec.Amount,
		Fee:    fee,
		Actual: actual,
		Rate:   rate,
		// 打款指令按到账币种最小单位定格: 审批/打款只读此快照, 不再重算。
		ActualCents: int64(math.Round(actual * rate * 100)),
		Currency:    payout,
		RateDay:     rateDay,
		RateSource:  rateSource,
		RateID:      rateID,
		Method:      method,
		AccountName: accountName,
		AccountNo:   accountNo,
		Status:      model.WithdrawStatusPending,
		AppliedAt:   time.Now(),
	}
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 行写锁: 把该行写成自身值先占住写锁(空更新, 不看 RowsAffected —— MySQL 按值变化计行数)。
		// 拿到写锁再读再算: 结算入账/退款冲回的并发事务要么排在本事务之前(重算看得见),
		// 要么排在之后(冻结判定用不到), 事务外的陈旧成熟收益快照不再可能放行冷却中的收入。
		if err := tx.Exec("UPDATE users SET total_revenue = total_revenue WHERE id = ?", spec.UserID).Error; err != nil {
			return fmt.Errorf("failed to lock user revenue: %w", err)
		}
		var user model.User
		if err := tx.First(&user, spec.UserID).Error; err != nil {
			return fmt.Errorf("user not found: %w", err)
		}
		// 成熟收益与不可提部分在锁内重算, 与冻结条件里的 total_revenue 同一时刻:
		// 不可提额度必须是事务内现算的, 否则并发入账的新收入会被陈旧快照当成可提。
		matured, err := maturedRevenueIn(tx, spec.UserID, rules.cooldownDays, user.TotalRevenue)
		if err != nil {
			return err
		}
		immature := user.TotalRevenue - matured
		if immature < 0 {
			immature = 0
		}
		if err := tx.Create(order).Error; err != nil {
			return fmt.Errorf("failed to create withdrawal: %w", err)
		}
		// 条件冻结: 可提(含冷却不可提部分)足额才成功, 否则整单回滚。
		result := tx.Exec(
			"UPDATE users SET withdraw_frozen = withdraw_frozen + ? WHERE id = ? AND total_revenue - total_withdrawn - withdraw_frozen >= ?",
			spec.Amount, spec.UserID, spec.Amount+immature,
		)
		if result.Error != nil {
			return fmt.Errorf("failed to freeze withdrawable balance: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("withdrawable balance is insufficient")
		}
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       spec.UserID,
			Kind:         model.LedgerKindWithdrawFreeze,
			Amount:       -spec.Amount,
			BalanceAfter: balanceOf(tx, spec.UserID),
			Note:         fmt.Sprintf("提现单 #%d 冻结(申请 %g, 手续费 %g)", order.ID, spec.Amount, fee),
		})
	})
	if err != nil {
		return nil, err
	}
	// 申请接口直接回吐这张单: 也得带上派生到账字段, 否则用户刚提交的那一单在列表刷新前
	// 仍会被前端当人民币(EUR 53.10 显成 ¥53.10)。
	order.DerivePayout()
	return order, nil
}

// WithdrawCancel 用户取消: 仅本人 pending 可取消, 解冻回可提。
func WithdrawCancel(ctx context.Context, userID uint, id uint, actorName string) error {
	return withdrawUnfreeze(ctx, id, model.WithdrawStatusPending, model.WithdrawStatusCancelled,
		"提现单 #%d 取消解冻", func(order *model.Withdrawal) error {
			if order.UserID != userID {
				return fmt.Errorf("withdrawal not found")
			}
			return nil
		}, actorName)
}

// WithdrawReview 管理员审核: 通过进 approved, 拒绝解冻。
func WithdrawReview(ctx context.Context, id uint, approve bool, note string, actorID uint, actorName string) error {
	note = strings.TrimSpace(note)
	if approve {
		var order model.Withdrawal
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 先拿写锁: 短事务的"读后写"升级会撞 SQLITE_BUSY_SNAPSHOT(517, busy_timeout 不覆盖),
			// 取锁后并发方在本事务的写锁上按 busy_timeout 排队。见 db.AcquireWriteLock。
			if err := db.AcquireWriteLock(tx); err != nil {
				return err
			}
			if err := tx.Where("id = ?", id).Take(&order).Error; err != nil {
				return fmt.Errorf("withdrawal not found")
			}
			result := tx.Model(&model.Withdrawal{}).
				Where("id = ? AND status = ?", id, model.WithdrawStatusPending).
				Updates(map[string]any{
					"status":      model.WithdrawStatusApproved,
					"review_note": note,
					"reviewed_by": actorID,
					"reviewed_at": time.Now(),
				})
			if result.Error != nil {
				return fmt.Errorf("failed to approve withdrawal: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("withdrawal is not pending")
			}
			return nil
		})
		if err != nil {
			return err
		}
		// 金额单位必须显式标注: order.Amount 是收益池口径, 唯一记账币种是 model.LedgerCurrency(USD)。
		// 旧文案写「元」会让用户按人民币理解(100 USD 的提现看似"100 元", 实际约 700 元)。
		// Go 侧中文字符串, 不走 web/src/locales 的 i18n 模板。
		notifyAsync(ctx, order.UserID, model.NotificationTypeWithdrawal, "提现审核通过",
			fmt.Sprintf("您的提现申请 #%d（金额 %.2f %s，账面币种）已审核通过，等待打款。", id, order.Amount, model.LedgerCurrency),
			"", uint64(id))
		return nil
	}
	err := withdrawUnfreeze(ctx, id, model.WithdrawStatusPending, model.WithdrawStatusRejected,
		"提现单 #%d 拒绝解冻", nil, actorName)
	if err != nil {
		return err
	}
	// 拒绝时补记审核意见; 失败不影响资金状态(解冻已完成)。
	db.GetDB().WithContext(ctx).Model(&model.Withdrawal{}).Where("id = ?", id).
		Updates(map[string]any{"review_note": note, "reviewed_by": actorID, "reviewed_at": time.Now()})
	return nil
}

// withdrawUnfreeze 通用解冻迁移: 仅在 fromStatus 时生效, 并发重复调用至多一次成。
func withdrawUnfreeze(ctx context.Context, id uint, fromStatus, toStatus model.WithdrawStatus,
	noteFormat string, guard func(*model.Withdrawal) error, actorName string) error {
	var order model.Withdrawal
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先拿写锁: 短事务的"读后写"升级会撞 SQLITE_BUSY_SNAPSHOT(517, busy_timeout 不覆盖),
		// 取锁后并发方在本事务的写锁上按 busy_timeout 排队。见 db.AcquireWriteLock。
		if err := db.AcquireWriteLock(tx); err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Take(&order).Error; err != nil {
			return fmt.Errorf("withdrawal not found")
		}
		if guard != nil {
			if err := guard(&order); err != nil {
				return err
			}
		}
		result := tx.Model(&model.Withdrawal{}).
			Where("id = ? AND status = ?", id, fromStatus).
			Update("status", toStatus)
		if result.Error != nil {
			return fmt.Errorf("failed to update withdrawal: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("withdrawal is not %s", fromStatus)
		}
		frozen := tx.Exec(
			"UPDATE users SET withdraw_frozen = withdraw_frozen - ? WHERE id = ? AND withdraw_frozen >= ?",
			order.Amount, order.UserID, order.Amount,
		)
		if frozen.Error != nil {
			return fmt.Errorf("failed to unfreeze withdrawable balance: %w", frozen.Error)
		}
		if frozen.RowsAffected != 1 {
			return fmt.Errorf("withdrawable balance is inconsistent")
		}
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       order.UserID,
			Kind:         model.LedgerKindWithdrawUnfreeze,
			Amount:       order.Amount,
			BalanceAfter: balanceOf(tx, order.UserID),
			ActorName:    actorName,
			Note:         fmt.Sprintf(noteFormat, order.ID),
		})
	})
}

// WithdrawMarkPaid 管理员打款: approved → paid, 冻结转累计已提(资金出池)。
// Balance 与可提都不变(可提 = 收益 - 已提 - 冻结, 两项同减)。
// actualCurrency 可选: 管理员实际换了币种打款时改写单上 ActualCurrency(空=按单据 Currency 打款,
// 不改判); 非白名单币种直接拒绝。只改快照记录, 不重算金额 —— 打款额以 paidRef 凭证为准。
func WithdrawMarkPaid(ctx context.Context, id uint, paidRef, actualCurrency, actorName string) error {
	paidRef = strings.TrimSpace(paidRef)
	if paidRef == "" {
		return fmt.Errorf("payment reference is required")
	}
	payout := ""
	if actualCurrency = strings.ToUpper(strings.TrimSpace(actualCurrency)); actualCurrency != "" {
		if !model.ValidPriceCurrency(actualCurrency) {
			return fmt.Errorf("unsupported actual currency: %s", actualCurrency)
		}
		payout = actualCurrency
	}
	var order model.Withdrawal
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先拿写锁: 短事务的"读后写"升级会撞 SQLITE_BUSY_SNAPSHOT(517, busy_timeout 不覆盖),
		// 取锁后并发方在本事务的写锁上按 busy_timeout 排队。见 db.AcquireWriteLock。
		if err := db.AcquireWriteLock(tx); err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Take(&order).Error; err != nil {
			return fmt.Errorf("withdrawal not found")
		}
		now := time.Now()
		updates := map[string]any{"status": model.WithdrawStatusPaid, "paid_ref": paidRef, "paid_at": now}
		if payout != "" {
			updates["actual_currency"] = payout
		}
		result := tx.Model(&model.Withdrawal{}).
			Where("id = ? AND status = ?", id, model.WithdrawStatusApproved).
			Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("failed to mark paid: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("withdrawal is not approved")
		}
		moved := tx.Exec(
			"UPDATE users SET withdraw_frozen = withdraw_frozen - ?, total_withdrawn = total_withdrawn + ? WHERE id = ? AND withdraw_frozen >= ?",
			order.Amount, order.Amount, order.UserID, order.Amount,
		)
		if moved.Error != nil {
			return fmt.Errorf("failed to settle withdrawal: %w", moved.Error)
		}
		if moved.RowsAffected != 1 {
			return fmt.Errorf("withdrawable balance is inconsistent")
		}
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       order.UserID,
			Kind:         model.LedgerKindWithdrawPaid,
			Amount:       0,
			BalanceAfter: balanceOf(tx, order.UserID),
			ActorName:    actorName,
			Note: fmt.Sprintf("提现单 #%d 打款 %s%.2f 凭证 %s", order.ID,
				func() string {
					if order.ActualCurrency != "" {
						return order.ActualCurrency + " "
					}
					if order.Currency != "" && order.Currency != "CNY" {
						return order.Currency + " "
					}
					return "¥"
				}(), float64(order.ActualCents)/100, paidRef),
		})
	})
	if err != nil {
		return err
	}
	UserTouchBalance(order.UserID)
	return nil
}

// WithdrawApproveTimeout 已审批但超时未打款的提现单自动撤销的等待期。
//
// 这是一个写死在代码里的常量, 不是新增设置项: why 见下。
// 为什么需要它: approved 是单向门 —— 审批只把 pending 改成 approved, 冻结额一动不动;
// 唯一解冻路径是 WithdrawMarkPaid(approved→paid 时把冻结转累计已提) 或 WithdrawRollback
// (paid→approved, 那是打款失败纠错, 不是"取消")。管理员点了"通过"但忘了打款(离职、
// 换班、忘了这笔), 用户的钱就一直挂在 withdraw_frozen 上: 既提不走(已冻结), 又不会回可提,
// 用户自己也没法撤销(WithdrawCancel 只认 pending)。只要管理员不回头, 这笔钱永远冻结。
//
// 为什么选周期任务扫描(而非放开用户撤销 approved):
//  1. 放开用户撤销 approved 会与"管理员已经核过款、正在走银行打款"这段真实窗口冲突 ——
//     用户一撤销, 管理员手上的打款指令就成了打一笔已经撤销的单, 只能靠对账事后发现。
//     要安全就得再加"管理员可关闭开关"或"距审批 N 小时内不可撤销"的规则, 等于两条新设置
//     项 + 前端设置页 + 校验, 而这套系统里已有大量同类"周期任务兜底"的先例
//     (工单自动关闭、预扣回滚、告警补发), 兜底本就是这个仓库的既有风格。
//  2. 用常量而不是设置项: 这条阈值是资金安全的下界, 不是业务口味。做成设置项意味着可以
//     被调成 1000 天(等于关闭)或被误设为 0(审批完立刻撤销, 管理员永远打不了款),
//     两种极端都制造资损/投诉。写死成 72 小时把"足够打款"与"不至于永久冻结"钉在一起,
//     真要调就改代码走一次评审 —— 与 TicketAutoCloseAfter 同一取舍。
//
// 72 小时: 人工打款是工作日内完成的动作, 3 天覆盖"周五审批、周一打款"的周末空档;
// 再长会让"审批后忘了"的钱在用户视角里长期不可用, 再短会误撤正在走流程的单。
const WithdrawApproveTimeout = 72 * time.Hour

// withdrawSystemActor 系统自动撤销时记在流水里的操作者名(审计的 actorID 记 0 = 系统)。
const withdrawSystemActor = "系统"

// WithdrawTimeoutRevoke 撤销审批超时未打款的提现单, 返回本轮实际撤销的笔数。
//
// 语义: approved 且 reviewed_at 早于 now-WithdrawApproveTimeout 的单子 → cancelled(解冻)。
// 选用 cancelled 而不是 rejected: rejected 是"管理员审核不通过"的语义, 这个单管理员是
// 审过了的, 只是没在时限内打款; cancelled 表示"这笔申请作废、未实际出款", 与用户主动
// 取消同一终点, 提现列表/导出里也不会把管理员记成拒绝过。
//
// 并发与幂等:
//   - 判定与更新在同一条带 status/reviewed_at 守卫的条件 UPDATE 里, 与管理员此刻点
//     "打款"(WithdrawMarkPaid) 并发时, 谁先拿到写锁谁赢, 输的一方 RowsAffected=0 直接跳过,
//     绝不会出现"既撤销解冻、又打款出池"的双重记账。
//   - 用 withdrawUnfreeze 复用既有解冻路径: 条件更新 + 冻结额断言 + withdraw_unfreeze 流水
//     一次到位, 不另写一套资金迁移。
//   - 重复跑安全: 已撤销的单不再匹配 status='approved', 第二轮扫不到。
//
// 留痕: 每笔写一条审计(audit_logs, actorID=0 系统) 与 withdraw_unfreeze 流水,
// 聚合成一条站内告警(withdrawal_approve_timeout) + 逐笔通知用户。
func WithdrawTimeoutRevoke(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-WithdrawApproveTimeout)
	var stale []model.Withdrawal
	if err := db.GetDB().WithContext(ctx).
		Where("status = ? AND reviewed_at IS NOT NULL AND reviewed_at <= ?", model.WithdrawStatusApproved, cutoff).
		Find(&stale).Error; err != nil {
		return 0, fmt.Errorf("failed to load stale approved withdrawals: %w", err)
	}

	var revoked int64
	var totalAmount float64
	var auditFailures []uint
	for i := range stale {
		order := stale[i]
		// 先解冻、再写审计。顺序理由: withdrawUnfreeze 是原子且权威的资金迁移 —— 它同时落
		// 状态、冻结额与 withdraw_unfreeze 流水, 而流水本身就是钱这条线的账。审计记的是
		// "为什么撤", 必须只对真正撤成功的单写一次: 反过来先写审计, 就会在并发下留下一批
		// "我没抢到但审计说我撤了"的假记录, 事后对账比没有审计更误导。
		// 审计失败不回滚已完成的资金迁移(冻结额退回是可逆的人工动作, 伪造状态不是),
		// 而是记下来继续处理其余单, 最后并进返回值让任务失败告警出口接管:
		// 漏审计是要人去补的运维问题, 不能顺手把剩下的钱继续冻着。
		err := withdrawUnfreeze(ctx, order.ID, model.WithdrawStatusApproved, model.WithdrawStatusCancelled,
			"提现单 #%d 审批超时未打款自动撤销解冻", nil, withdrawSystemActor)
		if err != nil {
			// "is not approved" 表示并发下已被打款或已被别处撤销: 这不是错误, 只是本轮没抢到,
			// 不能因为这一笔就放弃剩余的单(每笔都是独立的资金事实)。
			if strings.Contains(err.Error(), "is not approved") {
				log.Infof("withdrawal #%d timeout revoke skipped: status changed concurrently", order.ID)
				continue
			}
			return revoked, fmt.Errorf("failed to revoke stale withdrawal #%d: %w", order.ID, err)
		}
		revoked++
		totalAmount += order.Amount
		if err := AuditLogAdd(ctx, 0, "withdrawal.timeout_revoke",
			fmt.Sprintf("withdrawal#%d", order.ID),
			fmt.Sprintf("approved 超过 %d 小时未打款, 自动撤销并解冻 %.6f (申请人 #%d, 审批人 #%d)",
				int(WithdrawApproveTimeout.Hours()), order.Amount, order.UserID, order.ReviewedBy)); err != nil {
			log.Errorf("withdrawal #%d timeout revoke audit failed: %v", order.ID, err)
			auditFailures = append(auditFailures, order.ID)
		}
		// 通知用户在入账提交之后发: 通知失败不能影响已生效的资金事实(见 notifyAsync)。
		notifyAsync(ctx, order.UserID, model.NotificationTypeWithdrawal,
			fmt.Sprintf("提现申请 #%d 已自动撤销", order.ID),
			fmt.Sprintf("您的提现申请 #%d（金额 %.2f %s，账面币种）审核通过后已超过 %d 小时仍未打款，系统已自动撤销并把金额退回可提现额度；如需提现请重新提交申请。",
				order.ID, order.Amount, model.LedgerCurrency, int(WithdrawApproveTimeout.Hours())),
			"", uint64(order.ID))
	}
	if revoked > 0 {
		// 聚合告警: 逐笔各发一条会刷屏, 且去重窗口内的第二条起本来就会被吞掉。
		// Reason 不含量化数字(去重键), 具体笔数金额进 Fields。
		RaiseAlertFacts(ctx, AlertFacts{
			Kind:   "withdrawal_approve_timeout",
			Reason: "已审批的提现单超过打款时限未出款, 已自动撤销并解冻, 请核对打款流程",
			Fields: []alert.Field{
				{Label: "撤销笔数", Value: fmt.Sprintf("%d 笔", revoked)},
				{Label: "解冻金额", Value: fmt.Sprintf("%.2f", totalAmount)},
				{Label: "审批时限", Value: fmt.Sprintf("%d 小时", int(WithdrawApproveTimeout.Hours()))},
				{Label: "影响", Value: "资金已退回用户可提现额度; 若本应打款请重新申请或核对审核流程"},
			},
		})
	}
	if len(auditFailures) > 0 {
		// 资金已退回(事实), 审计没写进去(证据缺失): 两类问题都要暴露, 但失败要计到任务上,
		// 否则这批单将来没人能解释"为什么少了钱"。
		return revoked, fmt.Errorf("timeout revoke audit failed for %d withdrawal(s): %v", len(auditFailures), auditFailures)
	}
	return revoked, nil
}

// WithdrawRollback 打款回滚: paid → approved, 累计已提转回冻结(打款失败纠错用)。
func WithdrawRollback(ctx context.Context, id uint, actorName string) error {
	var order model.Withdrawal
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先拿写锁: 短事务的"读后写"升级会撞 SQLITE_BUSY_SNAPSHOT(517, busy_timeout 不覆盖),
		// 取锁后并发方在本事务的写锁上按 busy_timeout 排队。见 db.AcquireWriteLock。
		if err := db.AcquireWriteLock(tx); err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Take(&order).Error; err != nil {
			return fmt.Errorf("withdrawal not found")
		}
		result := tx.Model(&model.Withdrawal{}).
			Where("id = ? AND status = ?", id, model.WithdrawStatusPaid).
			Updates(map[string]any{"status": model.WithdrawStatusApproved, "paid_ref": "", "paid_at": nil})
		if result.Error != nil {
			return fmt.Errorf("failed to rollback withdrawal: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("withdrawal is not paid")
		}
		moved := tx.Exec(
			"UPDATE users SET withdraw_frozen = withdraw_frozen + ?, total_withdrawn = total_withdrawn - ? WHERE id = ? AND total_withdrawn >= ?",
			order.Amount, order.Amount, order.UserID, order.Amount,
		)
		if moved.Error != nil {
			return fmt.Errorf("failed to rollback pool: %w", moved.Error)
		}
		if moved.RowsAffected != 1 {
			return fmt.Errorf("withdrawable balance is inconsistent")
		}
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       order.UserID,
			Kind:         model.LedgerKindWithdrawRollback,
			Amount:       0,
			BalanceAfter: balanceOf(tx, order.UserID),
			ActorName:    actorName,
			Note:         fmt.Sprintf("提现单 #%d 打款回滚", order.ID),
		})
	})
	if err != nil {
		return err
	}
	UserTouchBalance(order.UserID)
	return nil
}

// WithdrawView 提现单 + 申请人用户名(管理端审核要按人看)。
type WithdrawView struct {
	model.Withdrawal
	Username string `json:"username"`
}

// WithdrawExportQuery 管理端导出筛选: 全部字段可选, 零值表示不过滤。
type WithdrawExportQuery struct {
	UserID uint
	Status string
	From   time.Time // applied_at >= from
	To     time.Time // applied_at < to
}

// WithdrawExport 导出查询: 与列表同口径但无 200 上限(调用方负责上限), id 倒序;
// Username 由调用方按需联查(导出场景直接写 user_id 列即可)。
func WithdrawExport(ctx context.Context, query WithdrawExportQuery, limit int) ([]model.Withdrawal, error) {
	statement := db.GetDB().WithContext(ctx).Model(&model.Withdrawal{})
	if query.UserID != 0 {
		statement = statement.Where("user_id = ?", query.UserID)
	}
	if status := strings.ToLower(strings.TrimSpace(query.Status)); status != "" {
		statement = statement.Where("status = ?", status)
	}
	if !query.From.IsZero() {
		statement = statement.Where("applied_at >= ?", query.From)
	}
	if !query.To.IsZero() {
		statement = statement.Where("applied_at < ?", query.To)
	}
	orders := []model.Withdrawal{}
	if err := statement.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("failed to export withdrawals: %w", err)
	}
	return orders, nil
}

// WithdrawExportCount 同条件的命中总数(导出截断提示用)。
func WithdrawExportCount(ctx context.Context, query WithdrawExportQuery, total *int64) error {
	statement := db.GetDB().WithContext(ctx).Model(&model.Withdrawal{})
	if query.UserID != 0 {
		statement = statement.Where("user_id = ?", query.UserID)
	}
	if status := strings.ToLower(strings.TrimSpace(query.Status)); status != "" {
		statement = statement.Where("status = ?", status)
	}
	if !query.From.IsZero() {
		statement = statement.Where("applied_at >= ?", query.From)
	}
	if !query.To.IsZero() {
		statement = statement.Where("applied_at < ?", query.To)
	}
	if err := statement.Count(total).Error; err != nil {
		return fmt.Errorf("failed to count withdrawal export: %w", err)
	}
	return nil
}

// WithdrawList 提现单列表: userID 非零查指定用户, 为零查全部(管理员); status 为空不过滤。
func WithdrawList(ctx context.Context, userID uint, status string, limit int) ([]WithdrawView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	statement := db.GetDB().WithContext(ctx).Model(&model.Withdrawal{})
	if userID != 0 {
		statement = statement.Where("user_id = ?", userID)
	}
	if status = strings.ToLower(strings.TrimSpace(status)); status != "" {
		statement = statement.Where("status = ?", status)
	}
	orders := []model.Withdrawal{}
	if err := statement.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("failed to list withdrawals: %w", err)
	}
	views := make([]WithdrawView, 0, len(orders))
	names := map[uint]string{}
	ids := make([]uint, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.UserID)
	}
	if len(ids) > 0 {
		var users []model.User
		if err := db.GetDB().WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err == nil {
			for _, user := range users {
				names[user.ID] = user.Username
			}
		}
	}
	for _, order := range orders {
		// 派生到账字段在这里填一次, 用户端(/list)与管理端(带 user_id 的同一口)都覆盖到。
		order.DerivePayout()
		views = append(views, WithdrawView{Withdrawal: order, Username: names[order.UserID]})
	}
	return views, nil
}
