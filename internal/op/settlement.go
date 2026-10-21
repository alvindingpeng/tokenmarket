package op

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// SettlementQuery 渠道结算单查询条件
type SettlementQuery struct {
	OwnerID uint
	Period  string // YYYYMMDD 或 YYYY-MM
	Status  string
	Limit   int
	Offset  int
}

// SettlementList 渠道结算单分页列表
func SettlementList(ctx context.Context, filter SettlementQuery) ([]model.SettlementStatement, int64, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	build := func() *gorm.DB {
		q := db.GetDB().WithContext(ctx).Model(&model.SettlementStatement{})
		if filter.OwnerID != 0 {
			q = q.Where("owner_id = ?", filter.OwnerID)
		}
		if filter.Period != "" {
			q = q.Where("period LIKE ?", "%"+strings.TrimSpace(filter.Period)+"%")
		}
		if filter.Status != "" {
			q = q.Where("status = ?", filter.Status)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SettlementStatement
	if err := build().Order("id DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// 结算单状态取值(与 model.SettlementStatement.Status 的取值注释一致)。
const (
	settlementStatusSettled = "settled"
	settlementStatusAudited = "audited"
)

// settlementDayWindow 把 YYYYMMDD 账期换算成 [start, end) 时间窗:
// 与 ReconcileDay 同一口径使用服务进程本地自然日(不是 UTC 日界), 否则同一张日结单与对账
// 会切出不同的记录集合; 一天按日历加一天, 不用固定 24 小时, 避开夏令时换挡产生的错位。
// 部署时应显式固定业务时区(见 docs/financial-hardening-phase1.md)。
func settlementDayWindow(day string) (time.Time, time.Time, error) {
	if len(day) != 8 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid day format %s, expected YYYYMMDD", day)
	}
	parsed, err := time.ParseInLocation("20060102", day, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse day %s: %w", day, err)
	}
	start := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.Local)
	return start, start.AddDate(0, 0, 1), nil
}

// settlementAmountEqual 金额比较容差: 与对账共用 1e-6(金额按 decimal(18,6) 记账)。
func settlementAmountEqual(a, b float64) bool {
	diff := a - b
	return diff <= reconcileTolerance && diff >= -reconcileTolerance
}

// settlementRowDiffer 判断重算结果与已有结算单是否有实质差异(定稿单据需要人工留调整凭证的口径)。
func settlementRowDiffer(old, next model.SettlementStatement) bool {
	return old.RequestCount != next.RequestCount ||
		old.InputToken != next.InputToken ||
		old.OutputToken != next.OutputToken ||
		!settlementAmountEqual(old.GrossRevenue, next.GrossRevenue) ||
		!settlementAmountEqual(old.RefundDeduction, next.RefundDeduction) ||
		!settlementAmountEqual(old.NetRevenue, next.NetRevenue)
}

// SettlementGenerateDay 为指定日期生成/刷新渠道商结算单 (幂等 upsert, 一次事务原子提交)。
//
// 账目口径(交易事件口径, 见 docs/financial-hardening-phase1.md 的"日结退款口径"):
//   - 毛额: 原调用的供应商应收(owner_revenue)计入调用发生日; 事后退款不改写当日毛额,
//     两个事件各记各的, 逐日看板才对得上原账单;
//   - 退款: 按"批准日"(reviewed_at)冲回原账单的供应商应收份额 owner_revenue, 而不是申诉单上的
//     买家支付额 user_cost —— 有上浮时 user_cost = owner_revenue + platform_revenue,
//     用 user_cost 扣减等于让渠道商替平台退上浮部分(毛额与扣减不同口径, 平白抹掉平台毛利);
//   - 退款日没有新收入的渠道商同样出调整单(净额可为负), 负额结转不截断为零: 截断会把债务悄悄
//     丢掉。负净额是财务调整, 不代表系统会自动向渠道商追偿或扣款;
//   - 边界: 与 ReconcileDay 一致的服务进程本地自然日, 一天按日历加一天。
//
// 生成只作用于未审核单: 已审核(status=audited)的结算单是定稿, 一个字段都不写。重算值与定稿
// 有差异时不静默覆盖, 而是拒绝本次生成并报错(一个字段都不写, 不留下半更新的账期), 由人工按
// "跨期差异留调整凭证"处理后再重跑; 无差异时未审核单照常刷新。任何查询/写入失败同样整体回滚。
// 未知状态(非 settled/audited/空)的单据同样在写入前拒绝并整次报错, 不静默跳过 —— 让人工先处置。
//
// 并发审核同样防住: 事务读快照之后才被定稿的单据, 写入被状态守卫拦下(见 settlementWriteRow),
// 不会被陈旧重算值覆盖 —— 已审核单的不可变性不依赖"审核与生成不同时发生"这个假设。
func SettlementGenerateDay(ctx context.Context, day string) error {
	start, end, err := settlementDayWindow(day)
	if err != nil {
		return err
	}

	// 查询与写入同处一个事务: 生成过程原子提交, 读写看到的是同一时刻的账, 任何查询/写入失败
	// 都整体回滚并向调用方报错(不再像旧实现那样吞掉错误继续跑)。
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		// 1) 毛额: 供应商应收按调用发生日归集(含事后被退款的账单, 退款另有批准日的冲回调整)。
		type ownerAgg struct {
			OwnerID      uint    `gorm:"column:owner_id"`
			RequestCount int64   `gorm:"column:request_count"`
			InputToken   int64   `gorm:"column:input_token"`
			OutputToken  int64   `gorm:"column:output_token"`
			GrossRevenue float64 `gorm:"column:gross_revenue"`
		}
		var aggs []ownerAgg
		if err := tx.Model(&model.BillingRecord{}).
			Select("owner_id, COUNT(*) as request_count, COALESCE(SUM(input_token),0) as input_token, COALESCE(SUM(output_token),0) as output_token, COALESCE(SUM(owner_revenue),0) as gross_revenue").
			Where("created_at >= ? AND created_at < ? AND owner_id > 0", start, end).
			Group("owner_id").
			Scan(&aggs).Error; err != nil {
			return fmt.Errorf("failed to aggregate billing records: %w", err)
		}

		// 2) 退款调整: 按批准日冲回原账单的供应商应收份额(owner_revenue), 金额与毛额同源同口径。
		var disputes []model.Dispute
		if err := tx.
			Where("status = ? AND reviewed_at >= ? AND reviewed_at < ?", model.DisputeStatusApproved, start, end).
			Find(&disputes).Error; err != nil {
			return fmt.Errorf("failed to load approved refunds: %w", err)
		}
		refundMap := map[uint]float64{}
		if len(disputes) > 0 {
			recordIDs := make([]uint64, 0, len(disputes))
			seen := map[uint64]bool{}
			for _, dispute := range disputes {
				if !seen[dispute.RecordID] {
					seen[dispute.RecordID] = true
					recordIDs = append(recordIDs, dispute.RecordID)
				}
			}
			var rows []model.BillingRecord
			if err := tx.Where("id IN ?", recordIDs).Find(&rows).Error; err != nil {
				return fmt.Errorf("failed to load refunded billing records: %w", err)
			}
			records := make(map[uint64]model.BillingRecord, len(rows))
			for _, row := range rows {
				records[row.ID] = row
			}
			if len(records) != len(recordIDs) {
				missing := make([]string, 0)
				for _, id := range recordIDs {
					if _, ok := records[id]; !ok {
						missing = append(missing, fmt.Sprintf("%d", id))
					}
				}
				return fmt.Errorf("approved refund(s) reference missing billing record(s): %s", strings.Join(missing, ", "))
			}
			counted := map[uint64]bool{}
			for _, dispute := range disputes {
				// 同一账单最多冲回一次: 重复冲回会把同一笔应收扣两遍。
				if counted[dispute.RecordID] {
					continue
				}
				counted[dispute.RecordID] = true
				record := records[dispute.RecordID]
				if record.OwnerID == 0 {
					continue // 无渠道归属的账单本来就没记供应商应收, 无可冲回
				}
				refundMap[record.OwnerID] += record.OwnerRevenue
			}
		}

		// 3) 目标集合 = 当日毛额渠道商 ∪ 当日退款渠道商 ∪ 已有结算单渠道商:
		//    退款日没有新收入的渠道商也要出调整单, 反过来源数据变化时已有单据同样按重算值刷新。
		var existing []model.SettlementStatement
		if err := tx.Where("period = ?", day).Find(&existing).Error; err != nil {
			return fmt.Errorf("failed to load existing statements: %w", err)
		}
		existingByOwner := make(map[uint]model.SettlementStatement, len(existing))
		for _, stmt := range existing {
			existingByOwner[stmt.OwnerID] = stmt
		}
		aggByOwner := make(map[uint]ownerAgg, len(aggs))
		ownerSet := map[uint]bool{}
		for _, agg := range aggs {
			aggByOwner[agg.OwnerID] = agg
			ownerSet[agg.OwnerID] = true
		}
		for ownerID := range refundMap {
			ownerSet[ownerID] = true
		}
		for ownerID := range existingByOwner {
			ownerSet[ownerID] = true
		}
		ownerIDs := make([]uint, 0, len(ownerSet))
		for ownerID := range ownerSet {
			ownerIDs = append(ownerIDs, ownerID)
		}
		sort.Slice(ownerIDs, func(i, j int) bool { return ownerIDs[i] < ownerIDs[j] })

		now := time.Now()
		// 先完整校验已审核单据，任何差异都在写入前拒绝，避免部分重算后再报错。
		for _, ownerID := range ownerIDs {
			old, exists := existingByOwner[ownerID]
			if !exists || old.Status != settlementStatusAudited {
				continue
			}
			agg := aggByOwner[ownerID]
			candidate := model.SettlementStatement{RequestCount: agg.RequestCount, InputToken: agg.InputToken, OutputToken: agg.OutputToken, GrossRevenue: agg.GrossRevenue, RefundDeduction: refundMap[ownerID], NetRevenue: agg.GrossRevenue - refundMap[ownerID]}
			if settlementRowDiffer(old, candidate) {
				return fmt.Errorf("audited statement %s differs from regenerated figures; no statements were changed", old.StatementNo)
			}
		}
		for _, ownerID := range ownerIDs {
			agg := aggByOwner[ownerID]
			refund := refundMap[ownerID]
			stmt := model.SettlementStatement{
				StatementNo:     fmt.Sprintf("STM-%s-%d", day, ownerID),
				OwnerID:         ownerID,
				Period:          day,
				RequestCount:    agg.RequestCount,
				InputToken:      agg.InputToken,
				OutputToken:     agg.OutputToken,
				GrossRevenue:    agg.GrossRevenue,
				RefundDeduction: refund,
				// 净额允许为负并结转: 负额是财务调整(如退款日无新收入), 不代表自动追偿或打款。
				NetRevenue: agg.GrossRevenue - refund,
				Status:     settlementStatusSettled,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			old, exists := existingByOwner[ownerID]
			if exists && old.Status == settlementStatusAudited {
				// 已审核单已在写入前校验为一致，保持原样。
				continue
			}
			var stale *model.SettlementStatement
			if exists {
				snapshot := old
				stale = &snapshot
			}
			if err := settlementWriteRow(tx, stale, &stmt, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// settlementWriteRow 在事务内落一张结算单(新建或刷新), 是结算单唯一的写入口。
//
// 刷新绝不用 tx.Save 整行覆盖: 事务早期读到的快照可能已经过时 —— 读完之后、写入之前,
// 并发的管理员审核随时可能把该单定稿(status → audited)。整行 Save 会把陈旧重算值连同状态列
// 一起盖回去, 定稿账被静默改写。因此:
//   - 状态白名单: 只有 settled/空状态(未定稿)可重算刷新; audited 与任何未知状态(人工挂起等)
//     在 Updates 之前直接拒绝 —— 不是"保状态改数字", 而是一个字段都不碰: 未知状态的数字
//     可能对应人工口径, 静默刷新会把人工账改掉。调用方跳过 audited 之外, 写入口自己也守;
//   - 状态守卫的条件更新: WHERE 状态仍等于读快照时的值, 撞上并发审核(或任何状态迁移)时
//     一行都改不到, 返回错误让整次生成回滚, 由人工留调整凭证后再处理;
//   - 刷新只写金额与计数字段, statement_no/owner_id/period/created_at 这些身份字段不动;
//     空状态仅在刷新时归一为 settled(建单默认值)。
func settlementWriteRow(tx *gorm.DB, old *model.SettlementStatement, next *model.SettlementStatement, now time.Time) error {
	if old == nil {
		if err := tx.Create(next).Error; err != nil {
			return fmt.Errorf("failed to create statement %s: %w", next.StatementNo, err)
		}
		return nil
	}
	if old.Status != "" && old.Status != settlementStatusSettled {
		return fmt.Errorf("statement %s has immutable status %q; no statements were changed", old.StatementNo, old.Status)
	}
	updates := map[string]any{
		"request_count":    next.RequestCount,
		"input_token":      next.InputToken,
		"output_token":     next.OutputToken,
		"gross_revenue":    next.GrossRevenue,
		"refund_deduction": next.RefundDeduction,
		"net_revenue":      next.NetRevenue,
		"updated_at":       now,
	}
	if old.Status == "" {
		updates["status"] = settlementStatusSettled // 只把空状态归一; 未知与已审核状态保持不变
	}
	result := tx.Model(&model.SettlementStatement{}).
		Where("id = ? AND COALESCE(status, '') = ?", old.ID, old.Status).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update statement %s: %w", old.StatementNo, result.Error)
	}
	if result.RowsAffected != 1 {
		// 读快照之后被并发改动(典型是被审核定稿): 不覆盖、不重试, 整次生成回滚。
		return fmt.Errorf("statement %s changed after it was read (e.g. audited concurrently); no statements were changed", old.StatementNo)
	}
	return nil
}

// ProfitMarginQuery 经营毛利看板查询
type ProfitMarginQuery struct {
	From time.Time
	To   time.Time
}

// ProfitMarginGet 汇总指定区间的平台总体收入、渠道成本、平台毛利、退款及充值总额
func ProfitMarginGet(ctx context.Context, filter ProfitMarginQuery) (*model.ProfitMarginSummary, []model.ProfitMarginSummary, error) {
	build := func() *gorm.DB {
		q := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{})
		if !filter.From.IsZero() {
			q = q.Where("created_at >= ?", filter.From)
		}
		if !filter.To.IsZero() {
			q = q.Where("created_at <= ?", filter.To)
		}
		return q
	}

	type sumResult struct {
		UserCost        float64 `gorm:"column:user_cost"`
		OwnerRevenue    float64 `gorm:"column:owner_revenue"`
		PlatformRevenue float64 `gorm:"column:platform_revenue"`
		RequestCount    int64   `gorm:"column:request_count"`
	}
	var overall sumResult
	_ = build().Select("COALESCE(SUM(user_cost),0) as user_cost, COALESCE(SUM(owner_revenue),0) as owner_revenue, COALESCE(SUM(platform_revenue),0) as platform_revenue, COUNT(*) as request_count").Scan(&overall).Error

	// 看板口径的退款是"退给用户的现金"(user_cost, GMV 的回冲), 与结算单按供应商应收份额
	// (owner_revenue)冲回是两个不同口径, 不要混用: 毛利看板对用户侧, 日结单对渠道商侧。
	var refundSum float64
	refQ := db.GetDB().WithContext(ctx).Model(&model.Dispute{}).Where("status = ?", model.DisputeStatusApproved)
	if !filter.From.IsZero() {
		refQ = refQ.Where("reviewed_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		refQ = refQ.Where("reviewed_at <= ?", filter.To)
	}
	_ = refQ.Select("COALESCE(SUM(user_cost), 0)").Scan(&refundSum).Error

	var rechargeSumCents int64
	rechQ := db.GetDB().WithContext(ctx).Model(&model.RechargeOrder{}).Where("status = ?", model.RechargeStatusPaid)
	if !filter.From.IsZero() {
		rechQ = rechQ.Where("paid_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		rechQ = rechQ.Where("paid_at <= ?", filter.To)
	}
	_ = rechQ.Select("COALESCE(SUM(amount_cents), 0)").Scan(&rechargeSumCents).Error

	marginRate := 0.0
	if overall.UserCost > 0 {
		marginRate = (overall.PlatformRevenue / overall.UserCost) * 100
	}

	total := &model.ProfitMarginSummary{
		Period:          "all",
		UserCost:        overall.UserCost,
		OwnerRevenue:    overall.OwnerRevenue,
		PlatformRevenue: overall.PlatformRevenue,
		MarginRate:      marginRate,
		RequestCount:    overall.RequestCount,
		RefundAmount:    refundSum,
		RechargeAmount:  float64(rechargeSumCents) / 100.0,
	}

	// 按日趋势聚合 (最多返回 30 天)
	type dayRow struct {
		Date            string  `gorm:"column:day"`
		UserCost        float64 `gorm:"column:user_cost"`
		OwnerRevenue    float64 `gorm:"column:owner_revenue"`
		PlatformRevenue float64 `gorm:"column:platform_revenue"`
		RequestCount    int64   `gorm:"column:request_count"`
	}
	var days []dayRow
	// 使用 strftime (sqlite) 或通用格式按日切片
	_ = build().Select("strftime('%Y%m%d', created_at) as day, COALESCE(SUM(user_cost),0) as user_cost, COALESCE(SUM(owner_revenue),0) as owner_revenue, COALESCE(SUM(platform_revenue),0) as platform_revenue, COUNT(*) as request_count").
		Group("day").Order("day DESC").Limit(30).Scan(&days).Error

	trends := make([]model.ProfitMarginSummary, 0, len(days))
	for _, d := range days {
		mr := 0.0
		if d.UserCost > 0 {
			mr = (d.PlatformRevenue / d.UserCost) * 100
		}
		trends = append(trends, model.ProfitMarginSummary{
			Period:          d.Date,
			UserCost:        d.UserCost,
			OwnerRevenue:    d.OwnerRevenue,
			PlatformRevenue: d.PlatformRevenue,
			MarginRate:      mr,
			RequestCount:    d.RequestCount,
		})
	}

	return total, trends, nil
}
