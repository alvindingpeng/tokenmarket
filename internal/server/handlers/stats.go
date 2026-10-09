package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

var activityMaxRequestCount int64     // 最近 54 周每日请求量的最大值。
var activityMaxCalculatedAt time.Time // 最大值上次计算时间。
var activityMaxMu sync.Mutex          // 保护最大值及计算时间的并发更新。

type statsDailyResponse struct {
	MaxRequestCount int64              `json:"max_request_count"` // 最近 54 周每日请求量的最大值。
	Items           []model.StatsDaily `json:"items"`             // 每日原始统计数据。
}

func init() {
	router.NewGroupRouter("/api/v1/stats").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/daily", http.MethodGet).
				Handle(getStatsDaily),
		).
		AddRoute(
			router.NewRoute("/hourly", http.MethodGet).
				Handle(getStatsHourly),
		).
		AddRoute(
			router.NewRoute("/total", http.MethodGet).
				Handle(getStatsTotal),
		).
		AddRoute(
			router.NewRoute("/apikey", http.MethodGet).
				Handle(getStatsAPIKey),
		).
		AddRoute(
			router.NewRoute("/billing", http.MethodGet).
				Handle(getBillingStats),
		).
		AddRoute(
			router.NewRoute("/billing/records", http.MethodGet).
				Handle(getBillingRecords),
		).
		AddRoute(
			router.NewRoute("/billing/records/export", http.MethodGet).
				Handle(exportBillingRecords),
		).
		AddRoute(
			router.NewRoute("/balance/ledger", http.MethodGet).
				Handle(getBalanceLedger),
		).
		AddRoute(
			router.NewRoute("/billing/revenue", http.MethodGet).
				Handle(getRevenueStats),
		)
}

// isViewerAdmin 当前访问者是否管理员; 管理员看全量统计, 其余按计费明细自证其范围。
func isViewerAdmin(c *gin.Context) bool {
	_, role := middleware.CurrentUser(c)
	return role == model.RoleAdmin
}

// userBillableStats 非管理员的统计口径: 全部来自本人的计费明细, 支出按上浮后价格。
func userBillableStats(c *gin.Context) (daily []model.StatsDaily, hourly []model.StatsHourly, total model.StatsTotal, err error) {
	userID, _ := middleware.CurrentUser(c)
	since := time.Now().AddDate(-1, 0, 0)
	dailyRows, err := op.BillingDaily(c.Request.Context(), "user", userID, since)
	if err != nil {
		return nil, nil, total, err
	}
	daily = make([]model.StatsDaily, 0, len(dailyRows))
	for _, row := range dailyRows {
		daily = append(daily, model.StatsDaily{
			Date: row.Date,
			StatsMetrics: model.StatsMetrics{
				InputToken:     row.InputToken,
				OutputToken:    row.OutputToken,
				InputCost:      row.UserCost,
				RequestSuccess: row.RequestCount,
			},
		})
	}
	hourlyRows, err := op.BillingHourly(c.Request.Context(), "user", userID, since)
	if err != nil {
		return nil, nil, total, err
	}
	hourly = make([]model.StatsHourly, 0, 24)
	for hour, row := range hourlyRows {
		hourly = append(hourly, model.StatsHourly{
			Hour: hour,
			Date: time.Now().Format("20060102"),
			StatsMetrics: model.StatsMetrics{
				InputToken:     row.InputToken,
				OutputToken:    row.OutputToken,
				InputCost:      row.UserCost,
				RequestSuccess: row.RequestCount,
			},
		})
	}
	totalRow, err := op.BillingTotal(c.Request.Context(), "user", userID)
	if err != nil {
		return nil, nil, total, err
	}
	total = model.StatsTotal{StatsMetrics: model.StatsMetrics{
		InputToken:     totalRow.InputToken,
		OutputToken:    totalRow.OutputToken,
		InputCost:      totalRow.UserCost,
		RequestSuccess: totalRow.RequestCount,
	}}
	return daily, hourly, total, nil
}

func getStatsDaily(c *gin.Context) {
	now := time.Now()
	var statsDaily []model.StatsDaily
	if isViewerAdmin(c) {
		since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")
		var err error
		statsDaily, err = op.StatsGetDaily(c.Request.Context(), since)
		if err != nil {
			resp.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		daily, _, _, err := userBillableStats(c)
		if err != nil {
			resp.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		statsDaily = daily
	}

	activityMaxMu.Lock()
	if activityMaxCalculatedAt.IsZero() || now.Sub(activityMaxCalculatedAt) >= 24*time.Hour {
		maxRequestCount := int64(0)
		for _, daily := range statsDaily {
			requestCount := daily.RequestSuccess + daily.RequestFailed
			if requestCount > maxRequestCount {
				maxRequestCount = requestCount
			}
		}
		activityMaxRequestCount = maxRequestCount
		activityMaxCalculatedAt = now
	}
	maxRequestCount := activityMaxRequestCount
	activityMaxMu.Unlock()

	resp.Success(c, statsDailyResponse{
		MaxRequestCount: maxRequestCount,
		Items:           statsDaily,
	})
}

func getStatsHourly(c *gin.Context) {
	if isViewerAdmin(c) {
		resp.Success(c, op.StatsHourlyGet())
		return
	}
	_, hourly, _, err := userBillableStats(c)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, hourly)
}

func getStatsTotal(c *gin.Context) {
	if isViewerAdmin(c) {
		resp.Success(c, op.StatsTotalGet())
		return
	}
	_, _, total, err := userBillableStats(c)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, total)
}

// getStatsAPIKey 返回访问者自有的 API Key 统计(与密钥列表同口径, 严格按归属隔离)。
func getStatsAPIKey(c *gin.Context) {
	stats := op.StatsAPIKeyList()
	if isViewerAdmin(c) {
		resp.Success(c, stats)
		return
	}
	userID, role := middleware.CurrentUser(c)
	keys, err := op.APIKeyList(model.Scope{ID: userID, Role: role}, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	owned := make(map[int]bool, len(keys))
	for _, key := range keys {
		owned[key.ID] = true
	}
	filtered := make([]model.StatsAPIKey, 0, len(stats))
	for _, stat := range stats {
		if owned[stat.APIKeyID] {
			filtered = append(filtered, stat)
		}
	}
	resp.Success(c, filtered)
}

// getBillingStats 当前用户的消费统计(上浮后价格口径)。
func getBillingStats(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	since := time.Now().AddDate(0, -1, 0)
	rows, err := op.BillingDaily(c.Request.Context(), "user", userID, since)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, rows)
}

// billingFilterFromQuery 解析计费明细筛选条件; 列表与导出共用同一套口径。
// 可见性铁律: 非管理员无论传什么参数都只看自己, 管理员可按 user_id/owner_id 看他人。
func billingFilterFromQuery(c *gin.Context) op.BillingQuery {
	userID, role := middleware.CurrentUser(c)
	filter := op.BillingQuery{
		UserID:   userID,
		Model:    c.Query("model"),
		Channel:  c.Query("channel"),
		Status:   c.Query("status"),
		PriceSrc: c.Query("price_source"),
	}
	if role == model.RoleAdmin {
		filter.UserID = 0
		if raw := c.Query("user_id"); raw != "" {
			if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
				filter.UserID = uint(parsed)
			}
		}
		if raw := c.Query("owner_id"); raw != "" {
			if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
				filter.OwnerID = uint(parsed)
			}
		}
	}
	if raw := c.Query("api_key_id"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			filter.APIKeyID = parsed
		}
	}
	if raw := c.Query("from"); raw != "" {
		if from, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.From = from
		}
	}
	if raw := c.Query("to"); raw != "" {
		if to, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.To = to
		}
	}
	filter.Limit, _ = strconv.Atoi(c.Query("limit"))
	filter.Offset, _ = strconv.Atoi(c.Query("offset"))
	return filter
}

// getBillingRecords 计费明细分页: 用户看自己的, 管理员看全部(可按 user_id/owner_id 过滤)。
func getBillingRecords(c *gin.Context) {
	filter := billingFilterFromQuery(c)
	records, total, err := op.BillingSearch(c.Request.Context(), filter)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": records, "total": total})
}

// exportBillingRecords 以 CSV 导出命中筛选条件的计费明细, 单次上限 5000 条; 可见性与列表一致。
func exportBillingRecords(c *gin.Context) {
	filter := billingFilterFromQuery(c)
	if filter.Limit <= 0 || filter.Limit > 5000 {
		filter.Limit = 5000
	}
	filter.Offset = 0
	records, total, err := op.BillingSearch(c.Request.Context(), filter)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=octopus-billing-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"id", "created_at", "request_id", "user_id", "api_key_id", "group_model", "model_name",
		"channel_id", "owner_id", "share_code", "price_source",
		"input_token", "output_token", "cache_read_token", "cache_write_token",
		"user_price_input", "user_price_output", "supply_price_input", "supply_price_output",
		"user_cost", "owner_revenue", "platform_revenue", "status",
	})
	for _, record := range records {
		_ = writer.Write([]string{
			strconv.FormatUint(record.ID, 10),
			record.CreatedAt.Format(time.RFC3339),
			strconv.FormatUint(record.RequestID, 10),
			strconv.FormatUint(uint64(record.UserID), 10),
			strconv.Itoa(record.APIKeyID),
			record.GroupModel,
			record.ModelName,
			strconv.Itoa(record.ChannelID),
			strconv.FormatUint(uint64(record.OwnerID), 10),
			record.ShareCode,
			record.PriceSource,
			strconv.FormatInt(record.InputToken, 10),
			strconv.FormatInt(record.OutputToken, 10),
			strconv.FormatInt(record.CacheReadToken, 10),
			strconv.FormatInt(record.CacheWriteToken, 10),
			strconv.FormatFloat(record.UserPrice.Input, 'f', 6, 64),
			strconv.FormatFloat(record.UserPrice.Output, 'f', 6, 64),
			strconv.FormatFloat(record.SupplyPrice.Input, 'f', 6, 64),
			strconv.FormatFloat(record.SupplyPrice.Output, 'f', 6, 64),
			strconv.FormatFloat(record.UserCost, 'f', 6, 64),
			strconv.FormatFloat(record.OwnerRevenue, 'f', 6, 64),
			strconv.FormatFloat(record.PlatformRevenue, 'f', 6, 64),
			record.Status,
		})
	}
	writer.Flush()
	if total > int64(len(records)) {
		_, _ = c.Writer.WriteString("# truncated: exported " + strconv.Itoa(len(records)) + " of " + strconv.FormatInt(total, 10) + " rows\n")
	}
	audit(c, "billing.export", "", fmt.Sprintf("rows=%d total=%d", len(records), total))
}

// getBalanceLedger 余额流水分页: 用户看自己的, 管理员可按 user_id 看指定用户。
func getBalanceLedger(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	query := op.LedgerQuery{UserID: userID, Kind: c.Query("kind")}
	if role == model.RoleAdmin {
		query.UserID = 0
		if raw := c.Query("user_id"); raw != "" {
			if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
				query.UserID = uint(parsed)
			}
		}
	}
	query.Limit, _ = strconv.Atoi(c.Query("limit"))
	query.Offset, _ = strconv.Atoi(c.Query("offset"))
	entries, total, err := op.LedgerList(c.Request.Context(), query)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": entries, "total": total})
}

// getRevenueStats 渠道商收入统计(供货价口径): 渠道商看自己, 管理员看全部(可按 owner_id 过滤)。
func getRevenueStats(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	ownerID := userID
	if role == model.RoleAdmin {
		if raw := c.Query("owner_id"); raw != "" {
			if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
				ownerID = uint(parsed)
			}
		}
	}
	if ownerID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	since := time.Now().AddDate(0, -1, 0)
	rows, err := op.BillingDaily(c.Request.Context(), "owner", ownerID, since)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, rows)
}
