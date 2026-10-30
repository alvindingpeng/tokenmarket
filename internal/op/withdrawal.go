package op

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// WithdrawSummary 提现口径: 可提 = 成熟收益 - 累计已提 - 提现中冻结。
// 成熟收益 = 冷却期外且未退款的累计收益; 冷却关闭时等于 TotalRevenue(池子已净扣退款)。
type WithdrawSummary struct {
	Available float64 `json:"available"` // 可提余额(收益池口径)
	Frozen    float64 `json:"frozen"`    // 提现中冻结
	Withdrawn float64 `json:"withdrawn"` // 累计已提
	Revenue   float64 `json:"revenue"`   // 累计收益
	Rate      float64 `json:"rate"`      // 汇率快照: 1 USD = ? CNY
	FeeRate   float64 `json:"fee_rate"`
	FeeMin    float64 `json:"fee_min"`
	MinAmount float64 `json:"min_amount"`
	CanApply  bool    `json:"can_apply"` // 角色是否允许提现(D4: 仅渠道商/管理员)
}

// WithdrawSpec 提现申请参数。
type WithdrawSpec struct {
	UserID      uint
	Role        model.Role
	Amount      float64
	Method      string
	AccountName string
	AccountNo   string
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
	rate, _ := StaticRateProvider{}.Rate(ctx, "CNY")
	return &WithdrawSummary{
		Available: available,
		Frozen:    user.WithdrawFrozen,
		Withdrawn: user.TotalWithdrawn,
		Revenue:   user.TotalRevenue,
		Rate:      rate,
		FeeRate:   rules.feeRate,
		FeeMin:    rules.feeMin,
		MinAmount: rules.minAmount,
		CanApply:  canWithdraw(role),
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
	rate, err := StaticRateProvider{}.Rate(ctx, "CNY")
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
		UserID:      spec.UserID,
		Amount:      spec.Amount,
		Fee:         fee,
		Actual:      actual,
		Rate:        rate,
		ActualCents: int64(math.Round(actual * rate * 100)),
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
		notifyAsync(ctx, order.UserID, model.NotificationTypeWithdrawal, "提现审核通过",
			fmt.Sprintf("您的提现申请 #%d（金额 %.2f 元）已审核通过，等待打款。", id, order.Amount),
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
func WithdrawMarkPaid(ctx context.Context, id uint, paidRef string, actorName string) error {
	paidRef = strings.TrimSpace(paidRef)
	if paidRef == "" {
		return fmt.Errorf("payment reference is required")
	}
	var order model.Withdrawal
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", id).Take(&order).Error; err != nil {
			return fmt.Errorf("withdrawal not found")
		}
		now := time.Now()
		result := tx.Model(&model.Withdrawal{}).
			Where("id = ? AND status = ?", id, model.WithdrawStatusApproved).
			Updates(map[string]any{"status": model.WithdrawStatusPaid, "paid_ref": paidRef, "paid_at": now})
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
			Note:         fmt.Sprintf("提现单 #%d 打款 ¥%.2f 凭证 %s", order.ID, float64(order.ActualCents)/100, paidRef),
		})
	})
	if err != nil {
		return err
	}
	UserTouchBalance(order.UserID)
	return nil
}

// WithdrawRollback 打款回滚: paid → approved, 累计已提转回冻结(打款失败纠错用)。
func WithdrawRollback(ctx context.Context, id uint, actorName string) error {
	var order model.Withdrawal
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		views = append(views, WithdrawView{Withdrawal: order, Username: names[order.UserID]})
	}
	return views, nil
}
