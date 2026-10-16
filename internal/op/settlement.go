package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
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

// SettlementGenerateDay 为指定日期生成/刷新渠道商结算单 (幂等 upsert)
func SettlementGenerateDay(ctx context.Context, day string) error {
	if len(day) != 8 {
		return fmt.Errorf("invalid day format %s, expected YYYYMMDD", day)
	}
	t, err := time.Parse("20060102", day)
	if err != nil {
		return fmt.Errorf("parse day %s: %w", day, err)
	}
	start := t
	end := t.AddDate(0, 0, 1)

	type ownerAgg struct {
		OwnerID      uint    `gorm:"column:owner_id"`
		RequestCount int64   `gorm:"column:request_count"`
		InputToken   int64   `gorm:"column:input_token"`
		OutputToken  int64   `gorm:"column:output_token"`
		GrossRevenue float64 `gorm:"column:gross_revenue"`
	}

	var aggs []ownerAgg
	err = db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select("owner_id, COUNT(*) as request_count, SUM(input_token) as input_token, SUM(output_token) as output_token, SUM(owner_revenue) as gross_revenue").
		Where("created_at >= ? AND created_at < ? AND owner_id > 0", start, end).
		Group("owner_id").
		Scan(&aggs).Error
	if err != nil {
		return fmt.Errorf("failed to aggregate billing records: %w", err)
	}

	type refundAgg struct {
		OwnerID      uint    `gorm:"column:owner_id"`
		RefundAmount float64 `gorm:"column:refund_amount"`
	}
	var refunds []refundAgg
	_ = db.GetDB().WithContext(ctx).Model(&model.Dispute{}).
		Select("owner_id, SUM(user_cost) as refund_amount").
		Where("status = ? AND reviewed_at >= ? AND reviewed_at < ? AND owner_id > 0", model.DisputeStatusApproved, start, end).
		Group("owner_id").
		Scan(&refunds).Error
	refundMap := make(map[uint]float64)
	for _, r := range refunds {
		refundMap[r.OwnerID] = r.RefundAmount
	}

	now := time.Now()
	for _, agg := range aggs {
		statementNo := fmt.Sprintf("STM-%s-%d", day, agg.OwnerID)
		refund := refundMap[agg.OwnerID]
		net := agg.GrossRevenue - refund
		if net < 0 {
			net = 0
		}
		stmt := model.SettlementStatement{
			StatementNo:     statementNo,
			OwnerID:         agg.OwnerID,
			Period:          day,
			RequestCount:    agg.RequestCount,
			InputToken:      agg.InputToken,
			OutputToken:     agg.OutputToken,
			GrossRevenue:    agg.GrossRevenue,
			RefundDeduction: refund,
			NetRevenue:      net,
			Status:          "settled",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		var existing model.SettlementStatement
		if err := db.GetDB().WithContext(ctx).Where("statement_no = ?", statementNo).First(&existing).Error; err == nil {
			stmt.ID = existing.ID
			stmt.CreatedAt = existing.CreatedAt
			_ = db.GetDB().WithContext(ctx).Save(&stmt).Error
		} else {
			_ = db.GetDB().WithContext(ctx).Create(&stmt).Error
		}
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
