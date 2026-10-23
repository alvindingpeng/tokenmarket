package op

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 对账容差: 金额按 decimal(18,6) 记账, 低于该阈值的差异视为浮点误差。
const reconcileTolerance = 1e-6

// ReconcileDay 汇总某自然日的对账结果并落库。
// 只登记结论与差异, 不修改任何用户余额; 发现差异时状态为待核查。
func ReconcileDay(ctx context.Context, day time.Time) (model.BillingReconcile, error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)
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

	// 预扣状态: 有有效续租的长请求不是长期冻结; 过期租约及无租约的旧超时记录需要核查。
	var unsettled []model.BalanceReservation
	if err := db.GetDB().WithContext(ctx).
		Where("settled = ? AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= ?) OR (lease_expires_at IS NULL AND created_at < ?))", false, time.Now(), time.Now().Add(-30*time.Minute)).
		Find(&unsettled).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("load unsettled reservations: %w", err)
	}
	result.Unsettled = int64(len(unsettled))
	result.StaleHold = int64(len(unsettled))

	result.Status = reconcileStatusFor(result)
	detail, _ := json.Marshal(map[string]any{
		"records":    result.Records,
		"missing":    result.Missing,
		"duplicated": result.Duplicated,
		"stale":      result.StaleHold,
	})
	result.Detail = string(detail)

	// 历史请求日志可能已清理。没有证据不能把已记录的漏结算改成零并静默抹掉异常。
	var previous model.BillingReconcile
	previousErr := db.GetDB().WithContext(ctx).Where("day = ?", result.Day).Take(&previous).Error
	if previousErr != nil && previousErr != gorm.ErrRecordNotFound {
		return model.BillingReconcile{}, fmt.Errorf("load prior reconcile evidence: %w", previousErr)
	}
	// 已写处置说明的日子: 重算数字与当时一致就保持 explained; 数字变了说明情况已经不同
	// (差异真的消失, 或又冒出新的), 落回对账自己算出的状态, 让新情况重新进入视野。
	keepExplained := previousErr == nil &&
		previous.Status == model.ReconcileStatusExplained &&
		previous.Missing == result.Missing &&
		previous.Duplicated == result.Duplicated &&
		previous.StaleHold == result.StaleHold
	if keepExplained {
		result.Status = model.ReconcileStatusExplained
	}

	if previousErr == nil && previous.Missing > result.Missing {
		var retained int64
		if err := db.GetDB().WithContext(ctx).Model(&model.RelayRequest{}).
			Where("started_at >= ? AND started_at < ?", start, end).Count(&retained).Error; err != nil {
			return model.BillingReconcile{}, fmt.Errorf("check request evidence coverage: %w", err)
		}
		if retained == 0 {
			return previous, fmt.Errorf("reconcile %s has prior missing-request evidence but no retained request logs; restore archival evidence before replacing the original result", result.Day)
		}
	}

	// note/noted_at/noted_by 刻意不在 DoUpdates 里: 重算是机器动作, 人工写下的处置说明属于
	// 记录本身, 不能被下一次重算抹掉 —— 否则「查清了但数字改不了」的结论每次重算都要重来一遍。
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "day"}},
		DoUpdates: clause.AssignmentColumns([]string{"records", "user_cost", "owner_revenue", "platform_rev", "duplicated", "missing", "stale_hold", "unsettled", "status", "detail", "updated_at"}),
	}).Create(&result).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("save reconcile: %w", err)
	}
	// 回填保留下来的人工说明, 让调用方直接看到完整记录(界面刷新后用的是列表接口, 这里主要是返回值一致)。
	// 已写说明的记录状态以 explained 为准: 数字仍是对账算出的原始数字, 只是结论已定。
	var saved model.BillingReconcile
	if err := db.GetDB().WithContext(ctx).Where("day = ?", result.Day).Take(&saved).Error; err == nil {
		result = saved
	}
	// 告警闭环: 对账差异/长期预扣等都触发告警, 由 RaiseAlert 统一去重。
	if result.Status != model.ReconcileStatusBalanced {
		// 差异数字进明细而不进 reason: reason 是去重键, 带上每天都在变的数值会让每一天
		// 的对账都成为"新告警", 去重窗口形同虚设。
		RaiseAlertFacts(ctx, AlertFacts{
			Kind:   "mismatch",
			Reason: fmt.Sprintf("reconcile status %s", result.Status),
			Fields: []alert.Field{
				{Label: "对账日期", Value: result.Day},
				{Label: "结论", Value: reconcileStatusLabel(result.Status)},
				{Label: "明细条数", Value: fmt.Sprintf("%d", result.Records)},
				{Label: "用户成本", Value: fmt.Sprintf("%.6f", result.UserCost)},
				{Label: "缺失调用", Value: fmt.Sprintf("%d", result.Missing)},
				{Label: "重复结算", Value: fmt.Sprintf("%d", result.Duplicated)},
				{Label: "长期预扣", Value: fmt.Sprintf("%d", result.StaleHold)},
				{Label: "建议", Value: "对账只登记差异, 不会自动改余额; 请人工核对后处理"},
			},
		})
	}
	return result, nil
}

// reconcileStatusFor 按差异项判定对账状态: 有漏结算/重复即 mismatch, 只有长期预扣则 review。
func reconcileStatusFor(result model.BillingReconcile) string {
	if result.Missing > 0 || result.Duplicated > 0 {
		return model.ReconcileStatusMismatch
	}
	if result.StaleHold > 0 {
		return model.ReconcileStatusReview
	}
	return model.ReconcileStatusBalanced
}

// reconcileNoteMax 处置说明长度上限: 说明是给人读的结论, 不是证据仓库, 长了反而没人看。
const reconcileNoteMax = 2000

// ReconcileNoteSet 为某日对账写人工处置说明: **数字一个都不改**, 只把「为什么差」钉在记录上。
//
// 为什么需要它: 有些差异查清之后仍然改不了 —— 证据已过请求日志保留窗口, 或者差异本就来自
// 当时的产品策略(例如全量计费修复前的 admin 免计费流量)。这种日子会永远停在 mismatch,
// 于是「有差异」和「这个差异已经查过」在界面上长得一模一样, 下一次还得从头推演一遍。
// explained 状态把两者分开: 数字保持对账算出的原值(不粉饰), 结论写明是怎么查的、为什么不动。
//
// note 传空表示撤销处置: 清除说明并把状态交回对账自己算。
// 只允许对有差异的记录写说明: balanced 的日子没有差异可解释, 给它挂一段说明只会让状态语义混乱。
func ReconcileNoteSet(ctx context.Context, day, note string, actorID uint) (model.BillingReconcile, error) {
	note = strings.TrimSpace(note)
	if len(note) > reconcileNoteMax {
		return model.BillingReconcile{}, fmt.Errorf("note too long: %d > %d", len(note), reconcileNoteMax)
	}
	var record model.BillingReconcile
	if err := db.GetDB().WithContext(ctx).Where("day = ?", day).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return model.BillingReconcile{}, fmt.Errorf("no reconcile record for day %s", day)
		}
		return model.BillingReconcile{}, fmt.Errorf("load reconcile record: %w", err)
	}
	hasDiff := record.Missing > 0 || record.Duplicated > 0 || record.StaleHold > 0
	if note != "" && !hasDiff && record.Status != model.ReconcileStatusExplained {
		return model.BillingReconcile{}, fmt.Errorf("reconcile %s has no discrepancy to explain", day)
	}

	now := time.Now()
	if note == "" {
		record.Note, record.NotedAt, record.NotedBy = "", nil, 0
		record.Status = reconcileStatusFor(record)
	} else {
		record.Note, record.NotedAt, record.NotedBy = note, &now, actorID
		record.Status = model.ReconcileStatusExplained
	}
	record.UpdatedAt = now
	// 只更新这四个字段: 差异数字仍由对账逻辑掌管, 人工处置不得顺手改数字。
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingReconcile{}).
		Where("day = ?", day).
		Updates(map[string]any{
			"note":       record.Note,
			"noted_at":   record.NotedAt,
			"noted_by":   record.NotedBy,
			"status":     record.Status,
			"updated_at": record.UpdatedAt,
		}).Error; err != nil {
		return model.BillingReconcile{}, fmt.Errorf("save reconcile note: %w", err)
	}
	return record, nil
}

// reconcileStatusLabel 对账结论的中文说明: 告警里管理员不该只看到裸的 "review"。
func reconcileStatusLabel(status string) string {
	switch status {
	case model.ReconcileStatusBalanced:
		return "账目对平"
	case model.ReconcileStatusMismatch:
		return "发现差异, 待人工核查"
	case model.ReconcileStatusReview:
		return "存在待核查项(重复/漏结算/长期预扣)"
	case model.ReconcileStatusExplained:
		return "差异已查清并留存说明(数字未改动)"
	}
	return status
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
