package op

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 用户花费预算: 周期性上限, warn/block 两档。
//
// 判定放在 relay 预扣之前(与 CheckAPIKeyQuota 同一位置): block 档在请求开始前拒绝,
// 不产生半截请求; warn 档在花费达到 warn_ratio 时发一条去重通知。
// 聚合口径与 max_cost 兜底一致: billing_records.user_cost, settled 状态, 按周期时间窗过滤。

// BudgetList 返回全部预算(管理页展示)。量少(用户自设), 不分页。
func BudgetList(ctx context.Context) ([]model.SpendBudget, error) {
	var budgets []model.SpendBudget
	err := db.GetDB().WithContext(ctx).Order("id DESC").Find(&budgets).Error
	return budgets, err
}

// BudgetCreate 新建预算。同一(user,key)组合 + 同周期只允许一条, 避免两条预算叠加时
// 判定顺序决定实际行为的混乱。
func BudgetCreate(ctx context.Context, b model.SpendBudget) (model.SpendBudget, error) {
	if b.UserID == 0 && b.APIKeyID == 0 {
		return model.SpendBudget{}, errors.New("budget requires user_id or api_key_id")
	}
	if !model.ValidBudgetPeriod(b.Period) {
		return model.SpendBudget{}, fmt.Errorf("invalid period %q (day or month)", b.Period)
	}
	if !model.ValidBudgetAction(b.Action) {
		return model.SpendBudget{}, fmt.Errorf("invalid action %q (warn or block)", b.Action)
	}
	if b.LimitAmount <= 0 {
		return model.SpendBudget{}, errors.New("limit_amount must be positive")
	}
	if b.WarnRatio < 1 || b.WarnRatio > 100 {
		return model.SpendBudget{}, errors.New("warn_ratio must be between 1 and 100")
	}
	var count int64
	q := db.GetDB().WithContext(ctx).Model(&model.SpendBudget{}).Where("period = ?", b.Period)
	if b.UserID > 0 {
		q = q.Where("user_id = ?", b.UserID)
	} else {
		q = q.Where("user_id = 0 AND api_key_id = ?", b.APIKeyID)
	}
	if err := q.Count(&count).Error; err != nil {
		return model.SpendBudget{}, err
	}
	if count > 0 {
		return model.SpendBudget{}, errors.New("a budget for this scope and period already exists")
	}
	b.Enabled = true
	if err := db.GetDB().WithContext(ctx).Create(&b).Error; err != nil {
		return model.SpendBudget{}, err
	}
	return b, nil
}

// BudgetUpdate 修改预算字段(不改 scope, 换 scope 等于删了重建)。
func BudgetUpdate(ctx context.Context, id uint, period, action string, limitAmount float64, warnRatio int, enabled bool) (model.SpendBudget, error) {
	if !model.ValidBudgetPeriod(period) {
		return model.SpendBudget{}, fmt.Errorf("invalid period %q", period)
	}
	if !model.ValidBudgetAction(action) {
		return model.SpendBudget{}, fmt.Errorf("invalid action %q", action)
	}
	if limitAmount <= 0 {
		return model.SpendBudget{}, errors.New("limit_amount must be positive")
	}
	if warnRatio < 1 || warnRatio > 100 {
		return model.SpendBudget{}, errors.New("warn_ratio must be between 1 and 100")
	}
	var b model.SpendBudget
	if err := db.GetDB().WithContext(ctx).First(&b, id).Error; err != nil {
		return model.SpendBudget{}, err
	}
	b.Period, b.Action, b.LimitAmount, b.WarnRatio, b.Enabled = period, action, limitAmount, warnRatio, enabled
	if err := db.GetDB().WithContext(ctx).Save(&b).Error; err != nil {
		return model.SpendBudget{}, err
	}
	return b, nil
}

// BudgetDelete 删除预算。删不存在的行不报错(GORM Delete 零行即成功), 幂等。
func BudgetDelete(ctx context.Context, id uint) error {
	return db.GetDB().WithContext(ctx).Delete(&model.SpendBudget{}, id).Error
}

// budgetPeriodWindow 返回周期的时间窗 [start, end): 日 = 本地自然日, 月 = 本地自然月。
// 与 quota 的自然日重置口径一致, 用户看到的"今天/本月"不会有歧义。
func budgetPeriodWindow(now time.Time, period string) (time.Time, time.Time) {
	switch period {
	case model.BudgetPeriodMonth:
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return start, start.AddDate(0, 1, 0)
	default: // day
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return start, start.AddDate(0, 0, 1)
	}
}

func budgetPeriodLabel(period string) string {
	if period == model.BudgetPeriodMonth {
		return "月"
	}
	return "日"
}

// budgetSpentInWindow 周期内已花费金额(user_cost, settled)。每次请求都聚合一次:
// 预算的语义就是"这个周期内", 必须实时, 不能像 max_cost 兜底那样用 TTL 缓存。
func budgetSpentInWindow(ctx context.Context, b model.SpendBudget, start, end time.Time) (float64, error) {
	q := db.GetDB().WithContext(ctx).
		Table("billing_records").
		Select("COALESCE(SUM(user_cost), 0)").
		Where("status = ? AND created_at >= ? AND created_at < ?", model.BillingStatusSettled, start, end)
	if b.UserID > 0 {
		q = q.Where("user_id = ?", b.UserID)
	} else {
		q = q.Where("api_key_id = ?", b.APIKeyID)
	}
	var total float64
	err := q.Scan(&total).Error
	return total, err
}

// CheckUserBudget 预算判定入口, relay 在预扣前调用。
// 返回 model.BudgetExceeded 表示 block 档拦截; warn 档达标时发去重通知, 永不拦截。
// 查询失败返回错误(调用方决定口径): 与 max_cost 兜底同样原则 —— 预算故障不误杀请求。
func CheckUserBudget(ctx context.Context, userID uint, apiKeyID int) error {
	var budgets []model.SpendBudget
	// 条件要精确到 scope: user 级预算匹配 user_id=userID, key 级预算匹配 api_key_id=apiKeyID。
	// 不能写 "user_id = ? OR api_key_id = ?" —— user_id=0 的调用(纯 Key 判定)会把所有 Key 级预算都拉进来。
	q := db.GetDB().WithContext(ctx).Where("enabled = ?", true)
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	if apiKeyID > 0 {
		q = q.Where("api_key_id = ?", apiKeyID)
	}
	if err := q.Find(&budgets).Error; err != nil {
		return fmt.Errorf("failed to load budgets: %w", err)
	}
	now := time.Now()
	for _, b := range budgets {
		start, end := budgetPeriodWindow(now, b.Period)
		spent, err := budgetSpentInWindow(ctx, b, start, end)
		if err != nil {
			return fmt.Errorf("failed to compute budget spend: %w", err)
		}
		scope := "user:" + fmt.Sprint(b.UserID)
		if b.UserID == 0 {
			scope = "api_key:" + fmt.Sprint(b.APIKeyID)
		}
		if b.Action == model.BudgetActionBlock && spent >= b.LimitAmount {
			return model.BudgetExceeded{Scope: scope, Current: spent, Limit: b.LimitAmount}
		}
		// warn: 达到 warn_ratio 即通知; RaiseAlertFacts 的去重窗口挡住每请求重复推送
		// (reason 只含比例不含实时金额, 否则金额每次都变, 去重等于失效)。
		if spent > 0 && spent >= b.LimitAmount*float64(b.WarnRatio)/100 {
			kind := "low_balance"
			if b.UserID == 0 {
				kind = "quota_exceeded"
			}
			RaiseAlertFacts(ctx, AlertFacts{
				Kind:    kind,
				Subject: scope,
				Reason:  fmt.Sprintf("预算警告: %s 本%s花费已达上限的 %d%%", scope, budgetPeriodLabel(b.Period), b.WarnRatio),
				Fields: []alert.Field{
					{Label: "预算范围", Value: scope},
					{Label: "周期", Value: budgetPeriodLabel(b.Period)},
					{Label: "上限", Value: fmt.Sprintf("%.2f", b.LimitAmount)},
					{Label: "已花费", Value: fmt.Sprintf("%.2f", spent)},
				},
			})
		}
	}
	return nil
}

// ===== 用户侧(self-service)预算 =====
// 与管理员接口同表同校验, 差异只在 scope: 用户只能看到和操作自己的 user 级预算。
// key 级预算是管理员分配全局 Key 池的手段, 不开放给用户自建(避免用户给自己扩容)。

// MyBudgetView 用户视角的一条预算: 本体 + 当前周期已花费与比例, 前端直接渲染进度条。
type MyBudgetView struct {
	model.SpendBudget
	Spent     float64 `json:"spent"`
	Limit     float64 `json:"limit"`
	RatioPct  int     `json:"ratio_pct"`
	WindowEnd string  `json:"window_end"`
}

// MyBudgetList 本人预算清单(含禁用), 附实时用量。
func MyBudgetList(ctx context.Context, userID uint) ([]MyBudgetView, error) {
	var budgets []model.SpendBudget
	if err := db.GetDB().WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id DESC").Find(&budgets).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	views := make([]MyBudgetView, 0, len(budgets))
	for _, b := range budgets {
		view := MyBudgetView{SpendBudget: b, Limit: b.LimitAmount}
		start, end := budgetPeriodWindow(now, b.Period)
		spent, err := budgetSpentInWindow(ctx, b, start, end)
		if err != nil {
			return nil, fmt.Errorf("failed to compute budget spend: %w", err)
		}
		view.Spent = spent
		if b.LimitAmount > 0 {
			view.RatioPct = int(spent * 100 / b.LimitAmount)
		}
		view.WindowEnd = end.Format(time.RFC3339)
		views = append(views, view)
	}
	return views, nil
}

// MyBudgetCreate 用户自建预算: 强制 user 级 scope, 拒绝带 api_key_id。
func MyBudgetCreate(ctx context.Context, userID uint, b model.SpendBudget) (model.SpendBudget, error) {
	if b.APIKeyID != 0 {
		return model.SpendBudget{}, errors.New("api_key budgets are managed by admins")
	}
	b.UserID = userID
	return BudgetCreate(ctx, b)
}

// MyBudgetDelete 删除自己的预算: 必须是 user 级且归属本人, 否则当不存在。
func MyBudgetDelete(ctx context.Context, userID uint, id uint) error {
	res := db.GetDB().WithContext(ctx).
		Where("id = ? AND user_id = ? AND user_id > 0", id, userID).
		Delete(&model.SpendBudget{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("budget not found")
	}
	return nil
}

// MyBudgetUpdate 改自己的预算(不改 scope 与归属)。
func MyBudgetUpdate(ctx context.Context, userID uint, id uint, period, action string, limitAmount float64, warnRatio int, enabled bool) (model.SpendBudget, error) {
	var b model.SpendBudget
	if err := db.GetDB().WithContext(ctx).
		Where("id = ? AND user_id = ? AND user_id > 0", id, userID).
		First(&b).Error; err != nil {
		return model.SpendBudget{}, errors.New("budget not found")
	}
	return BudgetUpdate(ctx, id, period, action, limitAmount, warnRatio, enabled)
}
