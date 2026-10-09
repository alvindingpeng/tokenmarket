package op

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm/clause"
)

// 对账容差: 金额按 decimal(18,6) 记账, 低于该阈值的差异视为浮点误差。
const reconcileTolerance = 1e-6

// ReconcileDay 汇总某自然日的对账结果并落库。
// 只登记结论与差异, 不修改任何用户余额; 发现差异时状态为待核查。
func ReconcileDay(ctx context.Context, day time.Time) (model.BillingReconcile, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	end := start.Add(24 * time.Hour)
	records := []model.BillingRecord{}
	if err := db.GetDB().WithContext(ctx).
		Where("created_at >= ? AND created_at < ?", start, end).
		Find(&records).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("load billing records: %w", err)
	}

	result := model.BillingReconcile{Day: start.Format("20060102"), Status: model.ReconcileStatusBalanced, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	seen := map[uint64]int64{}
	for _, record := range records {
		result.Records++
		result.UserCost += record.UserCost
		result.OwnerRevenue += record.OwnerRevenue
		result.PlatformRev += record.PlatformRevenue
		seen[record.RequestID]++
		// 三方对平: 平台收入应等于用户支出减去发布者收入。
		if diff := record.UserCost - record.OwnerRevenue - record.PlatformRevenue; diff > reconcileTolerance || diff < -reconcileTolerance {
			result.Duplicated++
		}
	}
	// 同一请求出现多条明细即为重复结算, 只计差额部分不影响主账。
	for _, count := range seen {
		if count > 1 {
			result.Duplicated++
		}
	}

	// 漏结算: 当天有用量记录但没有对应计费明细的请求。
	var unmapped int64
	if err := db.GetDB().WithContext(ctx).Model(&model.RelayRequest{}).
		Where("started_at >= ? AND started_at < ? AND prompt_tokens + completion_tokens > 0", start, end).
		Where("id NOT IN (SELECT request_id FROM billing_records)").
		Count(&unmapped).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("count unmapped requests: %w", err)
	}
	result.Missing = unmapped

	// 预扣状态: 已结算的正常, 未结算且早于回滚阈值的是长期冻结。
	var unsettled []model.BalanceReservation
	if err := db.GetDB().WithContext(ctx).
		Where("settled = ? AND created_at < ?", false, time.Now().Add(-30*time.Minute)).
		Find(&unsettled).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("load unsettled reservations: %w", err)
	}
	result.Unsettled = int64(len(unsettled))
	result.StaleHold = int64(len(unsettled))

	if result.StaleHold > 0 {
		result.Status = model.ReconcileStatusReview
	}
	if result.Missing > 0 || result.Duplicated > 0 {
		result.Status = model.ReconcileStatusMismatch
	}
	detail, _ := json.Marshal(map[string]any{
		"records":    result.Records,
		"missing":    result.Missing,
		"duplicated": result.Duplicated,
		"stale":      result.StaleHold,
	})
	result.Detail = string(detail)

	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "day"}},
		DoUpdates: clause.AssignmentColumns([]string{"records", "user_cost", "owner_revenue", "platform_rev", "duplicated", "missing", "stale_hold", "unsettled", "status", "detail", "updated_at"}),
	}).Create(&result).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("save reconcile: %w", err)
	}
	// 告警闭环: 对账差异/长期预扣等都触发告警, 由 RaiseAlert 统一去重。
	if result.Status != model.ReconcileStatusBalanced {
		RaiseAlert(ctx, 0, "system", "mismatch", fmt.Sprintf("reconcile %s: status=%s missing=%d duplicated=%d stale=%d",
			result.Day, result.Status, result.Missing, result.Duplicated, result.StaleHold))
	}
	return result, nil
}

// ReconcileRun 对最近若干天执行对账, 返回逐日结果。
func ReconcileRun(ctx context.Context, days int) ([]model.BillingReconcile, error) {
	if days <= 0 || days > 31 {
		days = 1
	}
	results := make([]model.BillingReconcile, 0, days)
	// 从昨天开始回溯: 当天仍在写入, 提前对账会得出漏结算的假阳性。
	for offset := days; offset >= 1; offset-- {
		day := time.Now().AddDate(0, 0, -offset)
		result, err := ReconcileDay(ctx, day)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// ReconcileList 返回最近的对账结果, 新的在前。
func ReconcileList(limit int) ([]model.BillingReconcile, error) {
	if limit <= 0 || limit > 365 {
		limit = 30
	}
	rows := []model.BillingReconcile{}
	if err := db.GetDB().Order("day DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// BalanceFlow 余额流水: 由预扣、计费明细与用户余额变动派生的可核对视图。
// 仅读取现有账目, 不新增账本表, 避免与已有计费链路产生两套事实。
type BalanceFlow struct {
	RequestID   uint64    `json:"request_id"`
	UserID      uint      `json:"user_id"`
	Kind        string    `json:"kind"` // reserve | settle | release
	Amount      float64   `json:"amount"`
	Settled     bool      `json:"settled"`
	CreatedAt   time.Time `json:"created_at"`
	UserCost    float64   `json:"user_cost"`
	OwnerRev    float64   `json:"owner_revenue"`
	PlatformRev float64   `json:"platform_revenue"`
}

// BalanceFlowList 按用户返回余额流水, 预扣与结算逐笔对应。
func BalanceFlowList(ctx context.Context, userID uint, limit int) ([]BalanceFlow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	reservations := []model.BalanceReservation{}
	query := db.GetDB().WithContext(ctx).Model(&model.BalanceReservation{})
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Order("id DESC").Limit(limit).Find(&reservations).Error; err != nil {
		return nil, err
	}
	if len(reservations) == 0 {
		return []BalanceFlow{}, nil
	}
	requestIDs := make([]uint64, 0, len(reservations))
	for _, reservation := range reservations {
		requestIDs = append(requestIDs, reservation.RequestID)
	}
	records := []model.BillingRecord{}
	if err := db.GetDB().WithContext(ctx).Where("request_id IN ?", requestIDs).Find(&records).Error; err != nil {
		return nil, err
	}
	byRequest := make(map[uint64]model.BillingRecord, len(records))
	for _, record := range records {
		byRequest[record.RequestID] = record
	}
	flows := make([]BalanceFlow, 0, len(reservations))
	for _, reservation := range reservations {
		flow := BalanceFlow{
			RequestID: reservation.RequestID,
			UserID:    reservation.UserID,
			Kind:      "reserve",
			Amount:    reservation.Amount,
			Settled:   reservation.Settled,
			CreatedAt: reservation.CreatedAt,
		}
		if record, ok := byRequest[reservation.RequestID]; ok {
			flow.Kind = "settle"
			flow.UserCost = record.UserCost
			flow.OwnerRev = record.OwnerRevenue
			flow.PlatformRev = record.PlatformRevenue
		} else if reservation.Settled {
			flow.Kind = "release"
		}
		flows = append(flows, flow)
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].CreatedAt.After(flows[j].CreatedAt) })
	return flows, nil
}

// BillingReconcileInit 无额外初始化; 保留以与其他 op 模块对称。
func BillingReconcileInit(context.Context) error { return nil }
