package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

func init() {
	// 渠道管理: 管理员(全部渠道)与渠道商(自有渠道); 用户无渠道管理。
	router.NewGroupRouter("/api/v1/channel").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/detail/:id", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(getChannelDetail),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(listChannelStats),
		).
		AddRoute(
			router.NewRoute("/stats/daily/:id", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(getChannelDailyStats),
		).
		AddRoute(
			router.NewRoute("/grants", http.MethodGet).
				Handle(listChannelGrant),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(createChannel),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(updateChannel),
		).
		AddRoute(
			router.NewRoute("/enable", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(enableChannel),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(deleteChannel),
		).
		AddRoute(
			router.NewRoute("/fetch-model", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(fetchModel),
		).
		AddRoute(
			router.NewRoute("/publish", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(publishChannel),
		).
		AddRoute(
			router.NewRoute("/models/:id", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(getChannelModelListings),
		).
		AddRoute(
			router.NewRoute("/models/update", http.MethodPost).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(updateChannelModelListings),
		)
}

// scopeOf 从上下文取访问者作用域。
func scopeOf(c *gin.Context) model.Scope {
	userID, role := middleware.CurrentUser(c)
	return model.Scope{ID: userID, Role: role}
}

// publishChannel 发布或取消发布渠道到用户侧; 发布自动生成唯一编码。
func publishChannel(c *gin.Context) {
	var request struct {
		ID     int  `json:"id"`
		Shared bool `json:"shared"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	channel, err := op.ChannelPublish(request.ID, request.Shared, scopeOf(c), c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.publish", fmt.Sprintf("channel#%d", channel.ID), fmt.Sprintf("shared=%v share_code=%s", channel.Shared, channel.ShareCode))
	resp.Success(c, gin.H{"id": channel.ID, "shared": channel.Shared, "share_code": channel.ShareCode})
}

// getChannelModelListings 返回渠道模型的上架配置(含四类供货价), 供发布管理界面编辑。
func getChannelModelListings(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	listings, err := op.ChannelModelListingGet(id, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, listings)
}

// listingChange 描述一行改价/上下架的前后差异, 用于审计与回执。
type listingChange struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// diffListings 比对上架配置的改动, 只回报真正变化的行。
// 整体提交的语义是"这份清单生效", 但审计要回答的是"谁把哪个模型的价格从多少改成了多少",
// 因此这里按名称逐字段比对, 未变动的行不进审计, 避免每次保存都写几十条噪声。
func diffListings(before []op.ChannelModelListing, after []op.ChannelModelListing) []listingChange {
	previous := make(map[string]op.ChannelModelListing, len(before))
	for _, listing := range before {
		previous[listing.Name] = listing
	}
	changes := make([]listingChange, 0)
	for _, listing := range after {
		old, ok := previous[listing.Name]
		if !ok {
			continue
		}
		if old.Listed != listing.Listed {
			changes = append(changes, listingChange{
				Name:   listing.Name,
				Detail: fmt.Sprintf("listed: %v -> %v", old.Listed, listing.Listed),
			})
		}
		fields := []struct {
			name     string
			oldValue float64
			newValue float64
		}{
			{"input", old.SupplyPrice.Input, listing.SupplyPrice.Input},
			{"output", old.SupplyPrice.Output, listing.SupplyPrice.Output},
			{"cache_read", old.SupplyPrice.CacheRead, listing.SupplyPrice.CacheRead},
			{"cache_write", old.SupplyPrice.CacheWrite, listing.SupplyPrice.CacheWrite},
		}
		for _, field := range fields {
			if field.oldValue == field.newValue {
				continue
			}
			changes = append(changes, listingChange{
				Name:   listing.Name,
				Detail: fmt.Sprintf("%s: %g -> %g", field.name, field.oldValue, field.newValue),
			})
		}
	}
	return changes
}

// updateChannelModelListings 更新渠道模型的上架与供货价; 未设置价格的模型按 0 计费。
// 回执带上本次真正改动的行, 供前端提示"改了什么"; 改动同时写入审计, 使调价可追溯。
func updateChannelModelListings(c *gin.Context) {
	var request struct {
		ID       int                      `json:"id"`
		Listings []op.ChannelModelListing `json:"listings"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	before, err := op.ChannelModelListingGet(request.ID, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	if err := op.ChannelModelListingUpdate(request.ID, request.Listings, scopeOf(c), c.Request.Context()); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	changes := diffListings(before, request.Listings)
	if len(changes) > 0 {
		parts := make([]string, 0, len(changes))
		for _, change := range changes {
			parts = append(parts, change.Name+" "+change.Detail)
		}
		audit(c, "channel.model-price", fmt.Sprintf("channel#%d", request.ID), strings.Join(parts, "; "))
	}
	resp.Success(c, gin.H{"changed": changes})
}

// getChannelDetail 返回单个渠道的完整配置, 供编辑表单打开时读取。
// 与列表分开: 整份配置带着路径, 代理与凭据明文, 只有正在编辑的那一个渠道用得上。
func getChannelDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	detail, err := op.ChannelDetailGet(id, scopeOf(c))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, detail)
}

// getChannelDailyStats 返回渠道自身与各模型近若干天的按天统计, 供渠道页展示成功率与延迟的逐日变化。
func getChannelDailyStats(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	days, err := strconv.Atoi(c.DefaultQuery("days", "14"))
	if err != nil || days < 1 || days > 90 {
		days = 14
	}
	userID, role := middleware.CurrentUser(c)
	channelDaily, modelDaily, err := op.ChannelDailyStatsGet(id, days, model.Scope{ID: userID, Role: role})
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	// 模型名按渠道模型主键补齐, 前端按 channel_model_id 归组即可。
	names, err := op.ChannelModelNames(id)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	models := make([]gin.H, 0)
	byModel := make(map[int][]model.StatsChannelModelDaily)
	for _, row := range modelDaily {
		byModel[row.ChannelModelID] = append(byModel[row.ChannelModelID], row)
	}
	for modelID, rows := range byModel {
		models = append(models, gin.H{
			"channel_model_id": modelID,
			"model_name":       names[modelID],
			"days":             rows,
		})
	}
	resp.Success(c, gin.H{"channel": channelDaily, "models": models})
}

// listChannelStats 返回全部渠道及其模型的累计统计, 也是渠道列表页的数据来源。
// 不带整份配置: 统计每次转发都在变, 界面按更短的间隔刷新它, 而路径, 代理与凭据明文只在编辑时用得上。
func listChannelStats(c *gin.Context) {
	resp.Success(c, op.ChannelStatsList(scopeOf(c)))
}

// listChannelGrant 返回全部渠道授权候选, 供分组页选取成员。
func listChannelGrant(c *gin.Context) {
	// 候选口径按访问者区分: 归属者看自有渠道, 其他用户只看已发布渠道的上架模型(编码脱敏)。
	resp.Success(c, op.ChannelGrantCandidates(scopeOf(c)))
}

func createChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	channel, err := op.ChannelCreate(&req, scopeOf(c), c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	autoAddAfterSave(c, channel.ID, req.ChannelConfig, scopeOf(c).ID)
	resp.Success(c, channel)
}

func updateChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.ID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	channel, err := op.ChannelUpdate(&req, scopeOf(c), c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	autoAddAfterSave(c, channel.ID, req.ChannelConfig, scopeOf(c).ID)
	resp.Success(c, channel)
}

// autoAddAfterSave 渠道保存成功后, 若开启了 model_auto_add 则按配置自动探测并并入上游模型。
// 探测与并入是 best-effort: 失败只记日志, 不影响已经成功的保存; 客户端构建失败同样只记日志。
func autoAddAfterSave(c *gin.Context, channelID int, config model.ChannelConfig, actorID uint) {
	if !config.ModelAutoAdd {
		return
	}
	client, err := probeHTTPClient(config)
	if err != nil {
		log.Warnf("auto-add: build probe client for channel %d failed: %v", channelID, err)
		return
	}
	if err := op.AutoAddChannelModels(c.Request.Context(), channelID, config, client, actorID); err != nil {
		log.Warnf("auto-add: channel %d failed: %v", channelID, err)
	}
}

func enableChannel(c *gin.Context) {
	var request struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.ChannelEnabled(request.ID, request.Enabled, scopeOf(c), c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func deleteChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.ChannelDel(id, scopeOf(c), c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

// addChannelModelPrices 为渠道模型匹配校准价格，并批量写入尚不存在的价格记录。
func addChannelModelPrices(modelNames []string, ctx context.Context) error {
	seen := make(map[string]struct{}, len(modelNames))
	llmInfos := make([]model.LLMInfo, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.ToLower(modelName)
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		llmInfo := model.LLMInfo{Name: modelName}
		if modelPrice := price.GetLLMPrice(modelName); modelPrice != nil {
			llmInfo.LLMPrice = *modelPrice
		}
		llmInfos = append(llmInfos, llmInfo)
	}
	return op.LLMBatchCreate(llmInfos, ctx)
}

// fetchModel 按提交的渠道配置与凭据拉取上游模型列表; 探测与协议判定在 op.ProbeChannelModels,
// 这里只负责按代理配置构建客户端并按失败类型映射状态码。
func fetchModel(c *gin.Context) {
	var request model.ChannelFetchModelRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	client, err := probeHTTPClient(request.Channel)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	fetched, err := op.ProbeChannelModels(client, request.Channel, request.Key, c.Request.Context())
	if err != nil {
		// 地址缺失与过滤正则编译错误属请求参数问题, 按 400; 两侧探测都失败是调用方配置问题, 按 502 带上游原文。
		if errors.Is(err, op.ErrProbeBaseURLRequired) || errors.Is(err, op.ErrProbeInvalidFilter) {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	resp.Success(c, fetched)
}

// probeHTTPClient 按渠道配置构建探测用的 HTTP 客户端: 直连 / 应用代理 / 渠道专用代理。
// 渠道专用代理的客户端不共享, 用完即关空闲连接; 编辑表单的手动刷新与保存后的自动添加共用。
func probeHTTPClient(target model.ChannelConfig) (*http.Client, error) {
	var httpClient *http.Client
	var err error
	switch {
	case !target.Proxy:
		httpClient, err = rhttp.Direct()
	case strings.TrimSpace(target.ChannelProxy) == "":
		httpClient, err = rhttp.Proxy()
	default:
		httpClient, err = rhttp.New(strings.TrimSpace(target.ChannelProxy))
		if httpClient != nil {
			defer httpClient.CloseIdleConnections()
		}
	}
	return httpClient, err
}
