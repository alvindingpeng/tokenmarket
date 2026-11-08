package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func init() {
	router.NewGroupRouter("/api/v1/fx").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/rates", http.MethodGet).
			Allow(model.RoleAdmin, model.RoleReseller).
			Handle(getFxRates))

	// 管理员运维端点: 状态只读, 刷新触发与后台任务相同的采集和陈旧检查。
	router.NewGroupRouter("/api/v1/admin/fx").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/rates/status", http.MethodGet).
			Allow(model.RoleAdmin).
			Handle(getAdminFxRateStatus)).
		AddRoute(router.NewRoute("/rates/history", http.MethodGet).
			Allow(model.RoleAdmin).
			Handle(getAdminFxRateHistory)).
		AddRoute(router.NewRoute("/rates/refresh", http.MethodPost).
			Allow(model.RoleAdmin).
			Handle(refreshAdminFxRates)).
		AddRoute(router.NewRoute("/reconciliation", http.MethodGet).
			Allow(model.RoleAdmin).
			Handle(getAdminFxReconciliation)).
		AddRoute(router.NewRoute("/revaluation", http.MethodGet).
			Allow(model.RoleAdmin).
			Handle(getAdminFxRevaluation))
}

// getFxRates 当前生效的牌价快照(账面币种 USD 为基准)。
func getFxRates(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	day, items, err := op.FxRateSnapshotView(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"day": day, "items": items})
}

// getAdminFxRateStatus 返回当前牌价快照及日龄, 供管理员判断采集是否陈旧。
func getAdminFxRateStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	day, items, err := op.FxRateSnapshotView(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	age, err := op.FxRateStalenessCheck(ctx)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"day":              day,
		"items":            items,
		"age_days":         age,
		"stale":            age < 0 || age > op.FxRateMaxAgeDays,
		"max_age_days":     op.FxRateMaxAgeDays,
		"source":           op.FxRateSourceName(),
		"supported_quotes": op.SupportedQuoteCurrencies(),
	})
}

// fxHistoryDateParam 解析 YYYYMMDD 日期参数; 空串返回零值(表示未提供)。
func fxHistoryDateParam(c *gin.Context, name string) (time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.ParseInLocation("20060102", raw, time.Local)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, fmt.Sprintf("invalid %s: want YYYYMMDD", name))
		return time.Time{}, false
	}
	return parsed, true
}

// getAdminFxRateHistory 只读查询 fx_rates 历史快照(按币种/日期), 不触发采集也不重算账单。
// 参数口径: currency(USD 及 SupportedQuoteCurrencies 之一)、from/to(YYYYMMDD, 区间含首尾,
// 最多 366 天)、limit(1-500, 缺省 100)。按采集日升序返回, 每条含 source 与 created_at。
func getAdminFxRateHistory(c *gin.Context) {
	now := time.Now()
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	startDay := endDay.AddDate(0, 0, -29)

	if parsed, ok := fxHistoryDateParam(c, "from"); !ok {
		return
	} else if !parsed.IsZero() {
		startDay = parsed
	}
	if parsed, ok := fxHistoryDateParam(c, "to"); !ok {
		return
	} else if !parsed.IsZero() {
		endDay = parsed
	}
	if startDay.After(endDay) || endDay.After(startDay.AddDate(0, 0, fxHistoryMaxDays-1)) {
		resp.Error(c, http.StatusBadRequest, fmt.Sprintf("date range must be 1-%d days", fxHistoryMaxDays))
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 500 {
		resp.Error(c, http.StatusBadRequest, "limit must be between 1 and 500")
		return
	}

	// 币种: 省略表示全部; 给出时必须是账面币种 USD 或参与采集的报价币种之一。
	// 不用 NormalizeCurrency: 它会把空串归一成 USD, 省略就被错误地当成"只查 USD"。
	rawCurrency := strings.ToUpper(strings.TrimSpace(c.Query("currency")))
	quote := rawCurrency
	if rawCurrency != "" && rawCurrency != model.LedgerCurrency &&
		!slices.Contains(op.SupportedQuoteCurrencies(), rawCurrency) {
		resp.Error(c, http.StatusBadRequest, "unsupported currency")
		return
	}
	gormDB := db.GetDB()
	if gormDB == nil {
		resp.Error(c, http.StatusInternalServerError, "database not ready")
		return
	}
	query := gormDB.WithContext(c.Request.Context()).
		Where(map[string]any{"from": model.LedgerCurrency}).
		Where("day >= ? AND day <= ?", startDay.Format("20060102"), endDay.Format("20060102"))
	if quote != "" {
		query = query.Where(map[string]any{"to": quote})
	}
	items := make([]model.FxRate, 0)
	if err := query.Order("day ASC").Order("`to` ASC").Limit(limit).Find(&items).Error; err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to query fx rate history")
		return
	}
	resp.Success(c, gin.H{
		"currency": quote,
		"from":     startDay.Format("20060102"),
		"to":       endDay.Format("20060102"),
		"limit":    limit,
		"items":    items,
	})
}

// fxHistoryMaxDays 历史查询的区间上限(含首尾), 防止一次拉取过多年限。
const fxHistoryMaxDays = 366

// refreshAdminFxRates 手动执行与周期任务相同的刷新及陈旧检查。
func refreshAdminFxRates(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	written, refreshErr := op.FxRateRefresh(ctx)
	age, ageErr := op.FxRateStalenessCheck(ctx)
	if refreshErr != nil {
		resp.Error(c, http.StatusBadGateway, refreshErr.Error())
		return
	}
	if ageErr != nil {
		resp.Error(c, http.StatusInternalServerError, ageErr.Error())
		return
	}
	audit(c, "fx_rate.refresh", op.FxRateSourceName(), "")
	resp.Success(c, gin.H{
		"written":      written,
		"age_days":     age,
		"stale":        age < 0 || age > op.FxRateMaxAgeDays,
		"max_age_days": op.FxRateMaxAgeDays,
		"source":       op.FxRateSourceName(),
	})
}

// fxReconciliationBucket 单个折算状态桶: 笔数与金额占比。
// 正常 = fx_day 是有效采集日; 陈旧 = 快照日早于当日(仍折算了, 但用旧价);
// 缺失 = fx_day=unavailable(采集失败按原价入账, 财务重点复核)。
type fxReconciliationBucket struct {
	Key          string  `json:"key"`
	Count        int64   `json:"count"`
	CountPct     float64 `json:"count_pct"`
	UserCost     float64 `json:"user_cost"`
	OwnerRevenue float64 `json:"owner_revenue"`
	AmountPct    float64 `json:"amount_pct"` // 按用户支出金额占比
}

// getAdminFxReconciliation FX 折算质量对账: 按时间范围统计 billing_records 按 fx_day
// 状态(正常/陈旧/缺失)的笔数与金额占比, 供财务重点复核采集失败期间的账单。
// 参数: from/to YYYYMMDD(缺省近 30 天), 范围含首尾。
func getAdminFxReconciliation(c *gin.Context) {
	now := time.Now()
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	startDay := endDay.AddDate(0, 0, -29)
	if parsed, ok := fxHistoryDateParam(c, "from"); !ok {
		return
	} else if !parsed.IsZero() {
		startDay = parsed
	}
	if parsed, ok := fxHistoryDateParam(c, "to"); !ok {
		return
	} else if !parsed.IsZero() {
		endDay = parsed
	}
	if startDay.After(endDay) {
		resp.Error(c, http.StatusBadRequest, "from must not be after to")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	start := startDay
	end := endDay.AddDate(0, 0, 1) // created_at < to+1d, 含 end 当日全天

	type row struct {
		Bucket       string  `json:"bucket"`
		Count        int64   `json:"count"`
		UserCost     float64 `json:"user_cost"`
		OwnerRevenue float64 `json:"owner_revenue"`
	}
	var rows []row
	query := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Select("CASE "+
			"WHEN COALESCE(fx_day, '') = 'unavailable' THEN 'missing' "+
			"WHEN COALESCE(fx_day, '') = '' THEN 'usd_native' "+
			"WHEN COALESCE(fx_day, '') < ? THEN 'stale' "+
			"ELSE 'ok' END as bucket, "+
			"COUNT(*) as count, COALESCE(SUM(user_cost),0) as user_cost, COALESCE(SUM(owner_revenue),0) as owner_revenue", endDay.Format("20060102")).
		Group("bucket")
	if err := query.Scan(&rows).Error; err != nil {
		resp.Error(c, http.StatusInternalServerError, fmt.Sprintf("failed to aggregate reconciliation: %v", err))
		return
	}
	keys := []string{"ok", "stale", "missing", "usd_native"}
	byKey := map[string]*row{}
	for i := range rows {
		byKey[rows[i].Bucket] = &rows[i]
	}
	var totalCount int64
	var totalAmount float64
	for _, key := range keys {
		if r := byKey[key]; r != nil {
			totalCount += r.Count
			totalAmount += r.UserCost
		}
	}
	buckets := make([]fxReconciliationBucket, 0, len(keys))
	for _, key := range keys {
		r := byKey[key]
		bucket := fxReconciliationBucket{Key: key}
		if r != nil {
			bucket.Count = r.Count
			bucket.UserCost = r.UserCost
			bucket.OwnerRevenue = r.OwnerRevenue
		}
		if totalCount > 0 {
			bucket.CountPct = math.Round(float64(bucket.Count)/float64(totalCount)*10000) / 100
		}
		if totalAmount > 0 {
			bucket.AmountPct = math.Round(bucket.UserCost/totalAmount*10000) / 100
		}
		buckets = append(buckets, bucket)
	}
	resp.Success(c, gin.H{
		"from":            startDay.Format("20060102"),
		"to":              endDay.Format("20060102"),
		"total_count":     totalCount,
		"total_user_cost": totalAmount,
		"buckets":         buckets,
	})
}

// getAdminFxRevaluation 月末牌价重估: unavailable 与 USD 原生行排除并单独计数。
func getAdminFxRevaluation(c *gin.Context) {
	month := strings.TrimSpace(c.Query("month"))
	now := time.Now()
	if month == "" {
		month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -1, 0).Format("2006-01")
	}
	monthStart, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil || monthStart.Format("2006-01") != month {
		resp.Error(c, http.StatusBadRequest, "month must be YYYY-MM")
		return
	}
	monthEnd := monthStart.AddDate(0, 1, 0)
	endDay := monthEnd.AddDate(0, 0, -1).Format("20060102")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	baseQuery := func() *gorm.DB {
		return db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
			Where("created_at >= ? AND created_at < ? AND COALESCE(fx_day, '') <> '' AND fx_day <> 'unavailable'", monthStart, monthEnd)
	}
	var base struct {
		Count        int64
		UserCost     float64
		OwnerRevenue float64
	}
	if err := baseQuery().Select("COUNT(*) as count, COALESCE(SUM(user_cost),0) as user_cost, COALESCE(SUM(owner_revenue),0) as owner_revenue").Scan(&base).Error; err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to aggregate monthly billing")
		return
	}
	var excluded int64
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Where("created_at >= ? AND created_at < ? AND (COALESCE(fx_day, '') = '' OR fx_day = 'unavailable')", monthStart, monthEnd).Count(&excluded).Error; err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to count excluded billing")
		return
	}
	var records []model.BillingRecord
	if err := baseQuery().Find(&records).Error; err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to load monthly billing")
		return
	}
	out := gin.H{"month": month, "fx_day_summary": gin.H{"count": base.Count, "user_cost": base.UserCost, "owner_revenue": base.OwnerRevenue}, "excluded": gin.H{"count": excluded}}
	var revaluedUser, revaluedOwner float64
	// Cache month-end rates per currency; use the last persisted snapshot no later than month end.
	rates := map[string]float64{}
	for _, record := range records {
		currency := model.NormalizeCurrency(record.Currency)
		if currency == model.LedgerCurrency {
			revaluedUser += record.UserCost
			revaluedOwner += record.OwnerRevenue
			continue
		}
		if record.FxRate <= 0 {
			resp.Error(c, http.StatusBadGateway, "missing historical billing fx_rate for "+currency)
			return
		}
		rate, ok := rates[currency]
		if !ok {
			var row model.FxRate
			if err := db.GetDB().WithContext(ctx).Where("`from` = ? AND `to` = ? AND day <= ?", model.LedgerCurrency, currency, endDay).Order("day DESC").First(&row).Error; err != nil {
				resp.Error(c, http.StatusBadGateway, "missing month-end FX snapshot for "+currency)
				return
			}
			// Billing FxRate 的结算口径是 1 报价币种折多少 USD; FxRate 表是 1 USD 折多少报价币种,
			// 因此重估使用倒数, 与账单快照同向后再除原快照乘月末快照。
			rate = 1 / row.Rate
			rates[currency] = rate
		}
		revaluedUser += record.UserCost / record.FxRate * rate
		revaluedOwner += record.OwnerRevenue / record.FxRate * rate
	}
	out["revalued"] = gin.H{"user_cost": revaluedUser, "owner_revenue": revaluedOwner}
	out["delta"] = gin.H{"user_cost": revaluedUser - base.UserCost, "owner_revenue": revaluedOwner - base.OwnerRevenue}
	resp.Success(c, out)
}
