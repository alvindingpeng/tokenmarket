package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

var activityMaxRequestCount int64     // 最近 54 周每日请求量的最大值。
var activityMaxCalculatedAt time.Time // 最大值上次计算时间。
var activityMaxMu sync.Mutex          // 保护最大值及计算时间的并发更新。

type statsDailyResponse struct {
	MaxRequestCount int64              `json:"max_request_count"` // 最近 54 周每日请求量的最大值。
	Items           []model.StatsDaily `json:"items"`             // 每日原始统计数据。
}

// statsHomeRankMetrics 榜单所需的精简累计指标, 只保留排名与展示用到的五个合计值。
// 合计项在后端算好: 前端 formatStatsMetrics 的口径(总词元=输入+输出, 金额=输入成本+输出成本,
// 请求数=成功+失败)在这里定稿, 榜单不必为了求和多带七个原始字段。
type statsHomeRankMetrics struct {
	TotalCost      float64 `json:"total_cost"`      // 合计成本 = 输入成本 + 输出成本。
	TotalToken     int64   `json:"total_token"`     // 合计词元 = 输入词元 + 输出词元。
	RequestCount   int64   `json:"request_count"`   // 合计请求数 = 成功 + 失败。
	RequestSuccess int64   `json:"request_success"` // 成功请求数。
	RequestFailed  int64   `json:"request_failed"`  // 失败请求数。
}

// statsHomeModelStats 榜单里的单个渠道模型, 只带展示与聚合所需的最小字段。
type statsHomeModelStats struct {
	ID          int    `json:"id"`          // 渠道模型主键, 供榜单做行 key。
	Name        string `json:"name"`        // 模型名; 同模型可跨渠道出现, 前端按名称聚合成一行。
	ChannelName string `json:"channelName"` // 所属渠道的展示名, 与渠道项同名(已发布为 #编码)。
	statsHomeRankMetrics
}

// statsHomeChannelStats 榜单里的单个渠道: 名称与精简统计, 外加该渠道各模型的精简统计。
// 用户侧榜单只消费这一份, 不再拉 /api/v1/channel/stats 的整份渠道配置。
type statsHomeChannelStats struct {
	ID          int    `json:"id"`          // 渠道主键, 供榜单做行 key。
	Name        string `json:"name"`        // 展示名: 已发布渠道给 #编码, 否则真名, 与渠道页榜单保持一致。
	ChannelName string `json:"channelName"` // 真实渠道名; 只有归属者/管理员看得到, 用户侧榜单永远为空。
	statsHomeRankMetrics
	Models []statsHomeModelStats `json:"models"` // 该渠道各模型的精简统计, 恒为数组。
}

// statsHomeResponse 首页首屏的聚合响应: 累计量, 每日, 每小时与榜单一次给全。
// 字段名与首页各组件原本各自的接口一一对应, 前端换成一次查询后展示口径不变:
// daily 复用 /stats/daily 的响应形状(含 max_request_count), hourly 复用 /stats/hourly 的原始行。
type statsHomeResponse struct {
	Total    model.StatsTotal        `json:"total"`    // 累计统计, 对应 /stats/total。
	Daily    statsDailyResponse      `json:"daily"`    // 每日统计与热力图上限, 对应 /stats/daily。
	Hourly   []model.StatsHourly     `json:"hourly"`   // 当日逐小时统计, 对应 /stats/hourly。
	Channels []statsHomeChannelStats `json:"channels"` // 榜单数据; 普通用户无渠道归属, 为空数组。
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
		// 首页首屏聚合: 一次往返给出 total/daily/hourly 与精简榜单, 替代原先的四条独立查询。
		AddRoute(
			router.NewRoute("/home", http.MethodGet).
				Handle(getStatsHome),
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
		// 计费明细导出是敏感导出(逐笔资金记录): 要求 step-up。
		AddRoute(
			router.NewRoute("/billing/records/export", http.MethodGet).
				Use(middleware.RequireStepUp()).
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
	billable := func(row op.BillingAggregate) model.StatsMetrics {
		return model.StatsMetrics{
			// 毛输入 + 输出即总词元(计费明细没有独立净读桶): 与管理员侧 usageMetrics 同口径,
			// 缓存读写字段丢弃会让非管理员的"总词元"漏记缓存命中的请求。
			InputToken:     row.InputToken + row.CacheReadToken + row.CacheWriteToken,
			OutputToken:    row.OutputToken,
			InputCost:      row.UserCost,
			RequestSuccess: row.RequestCount,
		}
	}
	dailyRows, err := op.BillingDaily(c.Request.Context(), "user", userID, since)
	if err != nil {
		return nil, nil, total, err
	}
	daily = make([]model.StatsDaily, 0, len(dailyRows))
	for _, row := range dailyRows {
		daily = append(daily, model.StatsDaily{Date: row.Date, StatsMetrics: billable(row)})
	}
	hourlyRows, err := op.BillingHourly(c.Request.Context(), "user", userID, since)
	if err != nil {
		return nil, nil, total, err
	}
	hourly = make([]model.StatsHourly, 0, 24)
	for hour, row := range hourlyRows {
		hourly = append(hourly, model.StatsHourly{Hour: hour, Date: time.Now().Format("20060102"), StatsMetrics: billable(row)})
	}
	totalRow, err := op.BillingTotal(c.Request.Context(), "user", userID)
	if err != nil {
		return nil, nil, total, err
	}
	total = model.StatsTotal{StatsMetrics: billable(totalRow)}
	return daily, hourly, total, nil
}

// viewerDailyStats 按访问者身份取每日统计: 管理员读全站长期表, 其余读本人在一年窗口内的计费明细。
// /stats/daily 与 /stats/home 共用这一份口径, 免得两处各写一遍而慢慢漂移。
func viewerDailyStats(c *gin.Context) ([]model.StatsDaily, error) {
	now := time.Now()
	if isViewerAdmin(c) {
		since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")
		return op.StatsGetDaily(c.Request.Context(), since)
	}
	daily, _, _, err := userBillableStats(c)
	if err != nil {
		return nil, err
	}
	return daily, nil
}

// viewerHourlyStats 按访问者身份取当日逐小时统计, 口径同 viewerDailyStats。
func viewerHourlyStats(c *gin.Context) ([]model.StatsHourly, error) {
	if isViewerAdmin(c) {
		return op.StatsHourlyGet(c.Request.Context()), nil
	}
	_, hourly, _, err := userBillableStats(c)
	if err != nil {
		return nil, err
	}
	return hourly, nil
}

// viewerTotalStats 按访问者身份取累计统计, 口径同 viewerDailyStats。
func viewerTotalStats(c *gin.Context) (model.StatsTotal, error) {
	if isViewerAdmin(c) {
		return op.StatsTotalGet(), nil
	}
	_, _, total, err := userBillableStats(c)
	if err != nil {
		return model.StatsTotal{}, err
	}
	return total, nil
}

// activityMax 返回热力图的请求量上界; 全量扫描 54 周日行只做一次, 24 小时内复用。
func activityMax(now time.Time, statsDaily []model.StatsDaily) int64 {
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
	return maxRequestCount
}

func getStatsDaily(c *gin.Context) {
	statsDaily, err := viewerDailyStats(c)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, statsDailyResponse{
		MaxRequestCount: activityMax(time.Now(), statsDaily),
		Items:           statsDaily,
	})
}

func getStatsHourly(c *gin.Context) {
	hourly, err := viewerHourlyStats(c)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, hourly)
}

func getStatsTotal(c *gin.Context) {
	total, err := viewerTotalStats(c)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, total)
}

// homeRankMetrics 把一份完整积累统计压成榜单需要的五个合计项, 口径与前端 formatStatsMetrics 一致。
func homeRankMetrics(metrics model.StatsMetrics) statsHomeRankMetrics {
	return statsHomeRankMetrics{
		TotalCost:      metrics.InputCost + metrics.OutputCost,
		TotalToken:     metrics.InputToken + metrics.OutputToken,
		RequestCount:   metrics.RequestSuccess + metrics.RequestFailed,
		RequestSuccess: metrics.RequestSuccess,
		RequestFailed:  metrics.RequestFailed,
	}
}

// homeChannelStats 把 op.ChannelStatsList 的结果投影成榜单用的精简结构。
// 复用同一份数据来源(作用域过滤、发布编码与模型清单都在里面), 只丢掉榜单不消费的配置字段:
// 首页因此不必再拉整份 /api/v1/channel/stats —— 那份带着路径、代理与全部模型配置。
func homeChannelStats(scope model.Scope) []statsHomeChannelStats {
	rows := op.ChannelStatsList(scope)
	channels := make([]statsHomeChannelStats, 0, len(rows))
	for _, row := range rows {
		// 展示名与渠道页榜单同规则: 已发布渠道对外只给发布编码, 真名只有归属者/管理员可见。
		name := row.ChannelName
		if row.ShareCode != "" {
			name = "#" + row.ShareCode
		}
		models := make([]statsHomeModelStats, 0, len(row.Models))
		for _, channelModel := range row.Models {
			models = append(models, statsHomeModelStats{
				ID:                   channelModel.ModelID,
				Name:                 channelModel.ModelName,
				ChannelName:          name,
				statsHomeRankMetrics: homeRankMetrics(channelModel.StatsMetrics),
			})
		}
		channels = append(channels, statsHomeChannelStats{
			ID:                   row.ChannelID,
			Name:                 name,
			ChannelName:          row.ChannelName,
			statsHomeRankMetrics: homeRankMetrics(row.StatsMetrics),
			Models:               models,
		})
	}
	return channels
}

// getStatsHome 首页首屏的聚合接口: 累计, 每日, 每小时与排行榜一次返回。
// 认证与作用域与 /api/v1/stats/* 完全一致(同一个 Auth 中间件与同一套身份口径), 只是把四次往返并成一次:
// 首屏此前要打 total/daily/hourly/channel-stats 四条, 响应里还带着榜单用不到的整份渠道配置。
func getStatsHome(c *gin.Context) {
	now := time.Now()
	admin := isViewerAdmin(c)
	var daily []model.StatsDaily
	var hourly []model.StatsHourly
	var total model.StatsTotal
	var err error
	if admin {
		// 管理员的三段分别来自日表与两个缓存快照, 与 /stats/daily、/stats/hourly、/stats/total 同源。
		since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")
		daily, err = op.StatsGetDaily(c.Request.Context(), since)
		hourly = op.StatsHourlyGet(c.Request.Context())
		total = op.StatsTotalGet()
	} else {
		// 非管理员的三段本来就出自同一份计费明细, 只聚合一次: 分开取等于把同一次扫描做三遍。
		daily, hourly, total, err = userBillableStats(c)
	}
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 榜单只对有渠道视图的角色有意义; 普通用户没有渠道归属, 直接给空数组而不是空跑一次缓存遍历。
	scope := scopeOf(c)
	channels := []statsHomeChannelStats{}
	if admin || scope.Role == model.RoleReseller {
		channels = homeChannelStats(scope)
	}

	resp.Success(c, statsHomeResponse{
		Total:    total,
		Daily:    statsDailyResponse{MaxRequestCount: activityMax(now, daily), Items: daily},
		Hourly:   hourly,
		Channels: channels,
	})
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
		FxDay:    c.Query("fx_day"),
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
	c.Header("Content-Disposition", "attachment; filename=switchyard-billing-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"id", "created_at", "request_id", "user_id", "api_key_id", "group_model", "model_name",
		"channel_id", "owner_id", "share_code", "price_source",
		"input_token", "output_token", "cache_read_token", "cache_write_token",
		"user_price_input", "user_price_output", "supply_price_input", "supply_price_output",
		"user_cost", "owner_revenue", "platform_revenue", "fx_day", "status",
	})
	for _, record := range records {
		_ = writer.Write([]string{
			strconv.FormatUint(record.ID, 10),
			record.CreatedAt.Format(time.RFC3339),
			strconv.FormatUint(record.RequestID, 10),
			strconv.FormatUint(uint64(record.UserID), 10),
			strconv.Itoa(record.APIKeyID),
			sanitizeCSVCell(record.GroupModel),
			sanitizeCSVCell(record.ModelName),
			strconv.Itoa(record.ChannelID),
			strconv.FormatUint(uint64(record.OwnerID), 10),
			sanitizeCSVCell(record.ShareCode),
			sanitizeCSVCell(record.PriceSource),
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
			sanitizeCSVCell(record.FxDay),
			sanitizeCSVCell(record.Status),
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
