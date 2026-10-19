package op

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 公开状态页(GET /api/v1/public/channel/stats, 无需登录)的后端实现。
//
// 这是「全站已上线渠道模型」的对外健康视图, 面向匿名访问者, 因此整条链路按最小暴露原则构造:
// 渠道身份只给发布编码(ShareCode), 渠道名 / 归属者 / 主键 / 凭据 / 上游地址一概不出。
// 供给侧口径与 ChannelGrantCandidates 一致(已发布 + 已过审 + 已上架), 这是「新增暴露」的同一道闸门:
// 状态页比分享页暴露面更大(列出全部渠道), 故连审核闸口一起收严, 与对外候选同标准。
//
// 指标口径(与 docs/public-status.md §3 同步, 改口径必须两处一起改):
//   - 样本 = relay_request_attempts 的持久化尝试行, 按 (channel_id, target_model) 归并。
//     两类行不计入尝试次数与成功率: 状态 "running" 的在途行(计进来会把正在跑的请求当失败),
//     以及 canceled / client_error 这类客户端取消或客户端请求错误(需求口径「已取消 / 主动中断
//     的请求不计入」; 这类失败任何渠道都会发生, 记进渠道健康度只会冤枉好渠道)。
//     其余终态二值化: "success" 计成功, 其它一切终态(failed/limited/timeout/
//     policy_blocked/auth_error/quota/overloaded/model_missing/...)计失败。
//   - 上游延迟 = 成功尝试的 duration_ms 均值; 没有成功尝试即 null。
//   - token 用量(CachedTokens/PromptTokens)从 relay_requests 取, 且只 join「最终成功尝试」
//     (request_id + round 对齐到 relay_requests.round 且该尝试 status='success'):
//     按 request_id+round join 天然一个请求最多命中一行, 重试不会重复计费, 失败渠道也不会
//     把最终成功的用量记到自己头上(防 retry 乘算与跨渠道错记)。
//   - 缓存命中率 = Σcached_tokens / Σprompt_tokens(同一归并组), 分母为 0(无数据)即 null。

const (
	channelStatusWindowSeconds  = 900              // 滚动统计窗口: 15 分钟。
	channelStatusRefreshSeconds = 15               // 前端建议刷新间隔: 15 秒(快照 10s 缓存 + 余量)。
	channelStatusBucketCount    = 15               // 历史桶个数。
	channelStatusBucketSeconds  = 60               // 每桶 1 分钟; 15 桶恰好铺满 900s 窗口。
	channelStatusSnapshotTTL    = 10 * time.Second // 快照缓存: 窗口查询挡在缓存后, 同一时刻只有一条重建在跑。
)

// 状态取值: no_data / operational / degraded / down。
const (
	channelStatusNoData      = "no_data"     // 窗口内无有效样本(空闲模型照常列出, 指标为 null)。
	channelStatusOperational = "operational" // 成功率 >= 0.99。
	channelStatusDegraded    = "degraded"    // 0.50 <= 成功率 < 0.99。
	channelStatusDown        = "down"        // 成功率 < 0.50, 或渠道模型级熔断生效中。
)

// 状态阈值(按窗口内已完成尝试的成功率), 分桶状态同口径(熔断不回溯历史桶)。
// 阈值定稿: 0.99 起算正常 —— 公开页宣称"正常"应偏保守; 0.50 以下才算挂掉, 中间为波动带。
const (
	channelStatusOperationalMin = 0.99
	channelStatusDegradedMin    = 0.50
)

// PublicChannelStats 公开状态页快照; 顶层只给窗口参数与模型数组, 不带任何渠道身份之外的字段。
type PublicChannelStats struct {
	GeneratedAt    time.Time                 `json:"generated_at"`
	WindowSeconds  int                       `json:"window_seconds"`
	RefreshSeconds int                       `json:"refresh_seconds"`
	Models         []PublicChannelModelStats `json:"models"`
}

// PublicChannelModelStats 单个渠道模型的公开状态; 字段即对外白名单, 增删必须同步改隐私测试。
// 身份只有 channel_code(发布编码, 裸数字形式, 展示方自行加 # 前缀)与 model_name。
type PublicChannelModelStats struct {
	ChannelCode    string               `json:"channel_code"`
	ModelName      string               `json:"model_name"`
	Kind           string               `json:"kind"`             // 媒体形态原样透传: 空串=文本, image=生图, video=生视频(契约见 docs/public-status.md §2)。
	UserPrice      PublicLLMPrice       `json:"user_price"`       // 用户价 = 供货价 × (1 + markup_ratio), 与计费同口径。
	OfficialPrice  *PublicLLMPrice      `json:"official_price"`   // 官方价目表价格; 表里没有该模型即 null。
	PriceRatio     PublicPriceRatio     `json:"price_ratio"`      // 用户价 / 官方价, 分量各自可空。
	UserMedia      PublicMediaPrice     `json:"user_media"`       // 媒体价目原样带单位输出, 不折算 token 比值。
	Status         string               `json:"status"`           // no_data | operational | degraded | down。
	Attempts       int64                `json:"attempts"`         // 窗口内计入口径的尝试数(在途与客户端取消不计)。
	SuccessRate    *float64             `json:"success_rate"`     // 成功/已完成, [0,1]; 无样本 null。
	LatencyMs      *int64               `json:"latency_ms"`       // 成功尝试耗时均值; 无成功尝试 null。
	CacheReadRatio *float64             `json:"cache_read_ratio"` // Σcached/Σprompt, [0,1]; 无数据 null。
	History        []PublicStatusBucket `json:"history"`          // 15 个一分钟桶, 恰好铺满窗口; 恒为数组。
}

// PublicStatusBucket 一分钟历史桶; start 为桶起点, 桶区间左闭右开(最后一桶右端含 now)。
type PublicStatusBucket struct {
	Start       time.Time `json:"start"`
	Status      string    `json:"status"`
	Attempts    int64     `json:"attempts"`
	SuccessRate *float64  `json:"success_rate"`
}

// PublicLLMPrice token 四类单价(每百万 token); 四键恒在, 未设置即 0。
type PublicLLMPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// PublicPriceRatio 用户价/官方价的分项比值; 官方分量缺失或为 0 时该分量为 null(不伪造 0 比值)。
type PublicPriceRatio struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// PublicMediaPrice 媒体价目, 保留计价单位(每张/每条/每秒/分档), 一律不折算 token 比值。
type PublicMediaPrice struct {
	PerImage   float64            `json:"per_image"`
	PerVideo   float64            `json:"per_video"`
	PerSecond  float64            `json:"per_second"`
	Resolution map[string]float64 `json:"resolution"` // 恒为对象, 无分档即空对象。
}

// channelStatusTarget 一个进入公开面的 (渠道, 模型) 组合; 主键只在内部查询定位用, 不出 DTO。
type channelStatusTarget struct {
	channelID   int
	channelCode string
	enabled     bool
	model       model.ChannelModel
}

// channelStatusTargets 定稿公开面可见的渠道模型清单。
// 供给侧隔离同对外候选: 已发布 + 已过审 + 已上架, 再加状态页自有的「渠道启用中」与「有发布编码」两道:
// 已发布的停用渠道保持可见并标红，撤销信任/下架/收回发布不再公开。
func channelStatusTargets() []channelStatusTarget {
	targets := make([]channelStatusTarget, 0, channelModelCache.Len())
	for _, channelModel := range channelModelCache.GetAll() {
		channel, ok := channelCache.Get(channelModel.ChannelID)
		if !ok {
			continue
		}
		if !channel.Shared || channel.TrustLevel == model.ChannelTrustRevoked || channel.ReviewStatus != model.ChannelReviewApproved {
			continue
		}
		if channel.ShareCode == "" || !channelModel.Listed {
			continue
		}
		targets = append(targets, channelStatusTarget{
			channelID:   channel.ID,
			channelCode: channel.ShareCode,
			enabled:     channel.Enabled,
			model:       channelModel,
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].channelCode != targets[j].channelCode {
			return targets[i].channelCode < targets[j].channelCode
		}
		return targets[i].model.Name < targets[j].model.Name
	})
	return targets
}

// channelStatusKey 归并键: 同名模型可能挂在多个渠道上, 各自独立计数, 绝不互相串。
type channelStatusKey struct {
	channelID int
	modelName string
}

// channelStatusAgg 窗口内一个 (渠道, 模型) 的累计样本与分桶样本。
type channelStatusAgg struct {
	attempts     int64
	successes    int64
	latencyCount int64
	latencySum   int64 // 成功尝试的 duration_ms 合计。
	bucketAtt    [channelStatusBucketCount]int64
	bucketSucc   [channelStatusBucketCount]int64
}

// channelStatusTokenRow 用量归并结果: 只含最终成功尝试命中的行。
type channelStatusTokenRow struct {
	ChannelID    int    `gorm:"column:channel_id"`
	TargetModel  string `gorm:"column:target_model"`
	PromptTokens int64  `gorm:"column:prompt_tokens"`
	CachedTokens int64  `gorm:"column:cached_tokens"`
}

// ChannelStatusSnapshot 返回公开状态页快照(10s 进程内缓存)。
// 返回值为快照副本, 只读使用; 缓存命中时不做深拷贝, 消费方不得改写。
func ChannelStatusSnapshot(ctx context.Context) (PublicChannelStats, error) {
	now := time.Now()
	// 锁覆盖重建过程: 并发请求同一时刻只有一条窗口查询在跑, 其余等锁后直接拿新快照。
	if !channelStatusCacheMu.TryLock() {
		return PublicChannelStats{}, fmt.Errorf("status snapshot busy")
	}
	defer channelStatusCacheMu.Unlock()
	if channelStatusLastError != nil && now.Before(channelStatusErrorExpiry) {
		return PublicChannelStats{}, channelStatusLastError
	}
	if channelStatusCache != nil && now.Before(channelStatusCacheExpiry) {
		return channelStatusVisible(*channelStatusCache), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		channelStatusLastError = err
		channelStatusErrorExpiry = time.Now().Add(5 * time.Second)
		return PublicChannelStats{}, err
	}
	channelStatusLastError = nil
	channelStatusCache = &snapshot
	channelStatusCacheExpiry = now.Add(channelStatusSnapshotTTL)
	return snapshot, nil
}

var (
	channelStatusLastError   error
	channelStatusErrorExpiry time.Time
	channelStatusCacheMu     sync.Mutex
	channelStatusCache       *PublicChannelStats
	channelStatusCacheExpiry time.Time
)

// channelStatusBuild 以 now 为窗口右端点构造快照; 时间显式传入, 便于测试钉住窗口边界。
func channelStatusVisible(snapshot PublicChannelStats) PublicChannelStats {
	visible := make(map[string]bool)
	for _, target := range channelStatusTargets() {
		visible[target.channelCode+"\x00"+target.model.Name] = target.enabled
	}
	rows := make([]PublicChannelModelStats, 0, len(snapshot.Models))
	for _, row := range snapshot.Models {
		enabled, ok := visible[row.ChannelCode+"\x00"+row.ModelName]
		if !ok {
			continue
		}
		if !enabled {
			row.Status = "disabled"
		}
		rows = append(rows, row)
	}
	snapshot.Models = rows
	return snapshot
}

func channelStatusBuild(ctx context.Context, now time.Time) (PublicChannelStats, error) {
	snapshot := PublicChannelStats{
		GeneratedAt:    now,
		WindowSeconds:  channelStatusWindowSeconds,
		RefreshSeconds: channelStatusRefreshSeconds,
		Models:         make([]PublicChannelModelStats, 0),
	}
	targets := channelStatusTargets()
	if len(targets) == 0 {
		return snapshot, nil
	}

	channelIDs := make([]int, 0, len(targets))
	seenChannel := make(map[int]struct{}, len(targets))
	for _, target := range targets {
		if _, ok := seenChannel[target.channelID]; ok {
			continue
		}
		seenChannel[target.channelID] = struct{}{}
		channelIDs = append(channelIDs, target.channelID)
	}

	from := now.Add(-time.Duration(channelStatusWindowSeconds) * time.Second)
	aggs, err := channelStatusLoadAttempts(ctx, from, now, channelIDs)
	if err != nil {
		return PublicChannelStats{}, err
	}
	tokens, err := channelStatusLoadTokens(ctx, from, now, channelIDs)
	if err != nil {
		return PublicChannelStats{}, err
	}

	markup := SettingGetFloat(model.SettingKeyMarkupRatio)
	for _, target := range targets {
		key := channelStatusKey{channelID: target.channelID, modelName: target.model.Name}
		agg := aggs[key]
		usage := tokens[key]
		snapshot.Models = append(snapshot.Models, channelStatusModel(target, agg, usage, markup, now, from))
	}
	return snapshot, nil
}

// channelStatusLoadAttempts 读取窗口内的尝试行, 按 (渠道, 模型) 与分桶累计。
// 仅读取带索引的15分钟时间窗与所选渠道的必要列，不截断样本；调用方5秒超时。
func channelStatusLoadAttempts(ctx context.Context, from, now time.Time, channelIDs []int) (map[channelStatusKey]*channelStatusAgg, error) {
	rows := make([]model.RelayRequestAttempt, 0, 64)
	if err := db.GetDB().WithContext(ctx).
		Model(&model.RelayRequestAttempt{}).
		Select("relay_request_attempts.channel_id", "relay_request_attempts.target_model", "relay_request_attempts.status", "relay_request_attempts.ttfb_ns", "relay_request_attempts.started_at").
		Joins("LEFT JOIN relay_requests r ON r.id = relay_request_attempts.request_id AND r.round = relay_request_attempts.round").
		Where("relay_request_attempts.status <> ? OR r.status = ?", "success", "success").
		Where("relay_request_attempts.started_at >= ? AND relay_request_attempts.started_at <= ?", from, now).
		Where("channel_id IN ?", channelIDs).
		Order("relay_request_attempts.started_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load channel status attempts: %w", err)
	}
	aggs := make(map[channelStatusKey]*channelStatusAgg, len(rows))
	for _, row := range rows {
		if !channelStatusAttemptCounted(row.Status) {
			continue
		}
		bucket, ok := channelStatusBucketIndex(from, now, row.StartedAt)
		if !ok {
			continue
		}
		key := channelStatusKey{channelID: row.ChannelID, modelName: row.TargetModel}
		agg := aggs[key]
		if agg == nil {
			agg = &channelStatusAgg{}
			aggs[key] = agg
		}
		agg.attempts++
		agg.bucketAtt[bucket]++
		if channelStatusAttemptSucceeded(row.Status) {
			agg.successes++
			agg.bucketSucc[bucket]++
			if row.TTFBNs > 0 {
				agg.latencySum += row.TTFBNs
				agg.latencyCount++
			}
		}
	}
	return aggs, nil
}

// channelStatusLoadTokens 读取窗口内的用量归并: 只 join 每个请求的最终成功尝试。
// join 条件 request_id + round 且 a.round = r.round: 一个请求最多命中一行, 重试不乘算;
// 再加 a.status = 'success', 失败渠道拿不到任何最终成功请求的用量(防跨渠道错记)。
func channelStatusLoadTokens(ctx context.Context, from, now time.Time, channelIDs []int) (map[channelStatusKey]channelStatusTokenRow, error) {
	rows := make([]channelStatusTokenRow, 0, 16)
	if err := db.GetDB().WithContext(ctx).Raw(`
		SELECT a.channel_id AS channel_id,
		       a.target_model AS target_model,
		       COALESCE(SUM(r.prompt_tokens), 0) AS prompt_tokens,
		       COALESCE(SUM(r.cached_tokens), 0) AS cached_tokens
		FROM relay_request_attempts a
		JOIN relay_requests r ON r.id = a.request_id AND r.round = a.round
		WHERE a.status = 'success' AND r.status = 'success'
		  AND a.started_at >= ? AND a.started_at <= ?
		  AND a.channel_id IN ?
		GROUP BY a.channel_id, a.target_model`, from, now, channelIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load channel status tokens: %w", err)
	}
	tokens := make(map[channelStatusKey]channelStatusTokenRow, len(rows))
	for _, row := range rows {
		tokens[channelStatusKey{channelID: row.ChannelID, modelName: row.TargetModel}] = row
	}
	return tokens, nil
}

// channelStatusModel 组装单个渠道模型的公开条目: 价格、指标、分桶状态一次定稿。
func channelStatusModel(target channelStatusTarget, agg *channelStatusAgg, usage channelStatusTokenRow, markup float64, now, from time.Time) PublicChannelModelStats {
	cm := target.model
	userPrice := cm.UserPriceLLM(markup)
	userMedia := cm.UserPriceMedia(markup)

	item := PublicChannelModelStats{
		ChannelCode: target.channelCode,
		ModelName:   cm.Name,
		Kind:        string(cm.Kind), // 媒体形态原样透传: 空串=文本(见 Kind 字段注释)。
		UserPrice:   channelStatusLLMPrice(userPrice),
		UserMedia: PublicMediaPrice{
			PerImage:   userMedia.PerImage,
			PerVideo:   userMedia.PerVideo,
			PerSecond:  userMedia.PerSecond,
			Resolution: channelStatusResolution(userMedia.Resolution),
		},
		PriceRatio: PublicPriceRatio{},
		History:    make([]PublicStatusBucket, 0, channelStatusBucketCount),
	}

	// 官方价: 价格表里没有该模型即 null, 比值四个分量跟着全空。
	if official, err := LLMGet(cm.Name); err == nil && cm.Kind == model.MediaKindText {
		price := channelStatusLLMPrice(official)
		item.OfficialPrice = &price
		item.PriceRatio = channelStatusPriceRatio(userPrice, official)
	}

	attempts, successes := int64(0), int64(0)
	if agg != nil {
		attempts, successes = agg.attempts, agg.successes
	}
	item.Attempts = attempts
	item.SuccessRate = channelStatusSuccessRate(successes, attempts)
	item.LatencyMs = channelStatusLatency(agg)
	item.CacheReadRatio = channelStatusCacheReadRatio(usage)

	// 熔断覆盖: 模型级熔断生效中即 down —— 这是"此刻能不能用"的直接事实, 优先于窗口样本。
	broken := cm.DisabledUntil != nil && cm.DisabledUntil.After(now)
	item.Status = channelStatusStatus(successes, attempts, broken)
	if !target.enabled {
		item.Status = "disabled"
	}

	for i := 0; i < channelStatusBucketCount; i++ {
		bucket := PublicStatusBucket{
			Start: from.Add(time.Duration(i*channelStatusBucketSeconds) * time.Second),
		}
		if agg != nil {
			bucket.Attempts = agg.bucketAtt[i]
			bucket.SuccessRate = channelStatusSuccessRate(agg.bucketSucc[i], agg.bucketAtt[i])
		}
		bucket.Status = channelStatusStatus(aggSucc(agg, i), bucket.Attempts, false)
		item.History = append(item.History, bucket)
	}
	return item
}

// aggSucc 取分桶成功数; agg 为 nil(无样本)时按 0 处理。
func aggSucc(agg *channelStatusAgg, bucket int) int64 {
	if agg == nil {
		return 0
	}
	return agg.bucketSucc[bucket]
}

// channelStatusBucketIndex 把采样时间映射进 15 个一分钟桶; 不在 [from, now] 内返回 false。
// 桶沿窗口精确铺开(而非墙钟整分对齐): Σ桶样本 == 窗口样本, 边界上不重不漏。
// 采样恰在窗口右端点(now)时落最后一桶: 窗口两端都是闭区间。
func channelStatusBucketIndex(from, now, at time.Time) (int, bool) {
	if at.Before(from) || at.After(now) {
		return 0, false
	}
	idx := int(at.Sub(from) / (time.Duration(channelStatusBucketSeconds) * time.Second))
	if idx >= channelStatusBucketCount {
		idx = channelStatusBucketCount - 1
	}
	return idx, true
}

// channelStatusAttemptCounted 判断一次尝试是否计入口径(尝试次数与成功率的样本)。
// 两类行剔除:
//   - "running": 在途登记行, 尚无终态, 计进来会把正在跑的请求当成失败;
//   - "canceled" / "client_error": 客户端取消、断开或请求本身不合法, 任何渠道都会如此,
//     需求口径「已取消 / 主动中断的请求不计入尝试次数与成功率」, 记进渠道健康度只会冤枉好渠道。
func channelStatusAttemptCounted(status string) bool {
	switch status {
	case "", "running", "committed", "canceled", "client_error", "limited", "policy_blocked", "model_missing":
		return false
	default:
		return true
	}
}

// channelStatusAttemptSucceeded 二值化计入口径内的尝试终态: 只有 "success" 计成功, 其余计失败。
func channelStatusAttemptSucceeded(status string) bool { return status == "success" }

// channelStatusSuccessRate 成功率; 无样本返回 null 而不是 0(0 是"全失败", 两者含义完全不同)。
func channelStatusSuccessRate(successes, attempts int64) *float64 {
	if attempts <= 0 {
		return nil
	}
	rate := channelStatusRound4(float64(successes) / float64(attempts))
	return &rate
}

// channelStatusLatency 成功尝试的平均耗时(毫秒); 无成功尝试返回 null。
func channelStatusLatency(agg *channelStatusAgg) *int64 {
	if agg == nil || agg.latencyCount <= 0 {
		return nil
	}
	mean := int64(math.Round(float64(agg.latencySum) / float64(agg.latencyCount) / 1_000_000))
	return &mean
}

// channelStatusCacheReadRatio 缓存命中率 Σcached/Σprompt; 分母为 0(无数据)返回 null。
// 结果夹进 [0,1]: 上游自报口径偶尔会给出 cached > prompt, 不把 >1 的数字推给公开面。
func channelStatusCacheReadRatio(usage channelStatusTokenRow) *float64 {
	if usage.PromptTokens <= 0 {
		return nil
	}
	ratio := float64(usage.CachedTokens) / float64(usage.PromptTokens)
	if ratio < 0 {
		return nil
	}
	if ratio > 1 {
		return nil
	}
	ratio = channelStatusRound4(ratio)
	return &ratio
}

// channelStatusStatus 由成功率与熔断状态定状态。
// 阈值: >= 0.99 operational; >= 0.50 degraded; 其余 down; 无样本 no_data; 熔断生效中恒 down。
func channelStatusStatus(successes, attempts int64, breakerActive bool) string {
	if breakerActive {
		return channelStatusDown
	}
	if attempts <= 0 {
		return channelStatusNoData
	}
	if attempts < 5 {
		return "low_sample"
	}
	rate := float64(successes) / float64(attempts)
	switch {
	case rate >= channelStatusOperationalMin:
		return channelStatusOperational
	case rate >= channelStatusDegradedMin:
		return channelStatusDegraded
	default:
		return channelStatusDown
	}
}

// channelStatusPriceRatio 用户价/官方价的分项比值; 官方分量缺失或为 0 即该分量 null(分母不存在不折算)。
func channelStatusPriceRatio(user, official model.LLMPrice) PublicPriceRatio {
	one := func(u, o float64) *float64 {
		if o <= 0 {
			return nil
		}
		ratio := channelStatusRound4(u / o)
		return &ratio
	}
	return PublicPriceRatio{
		Input:      one(user.Input, official.Input),
		Output:     one(user.Output, official.Output),
		CacheRead:  one(user.CacheRead, official.CacheRead),
		CacheWrite: one(user.CacheWrite, official.CacheWrite),
	}
}

// channelStatusLLMPrice 内部价目转公开形状(字段一一对应)。
func channelStatusLLMPrice(price model.LLMPrice) PublicLLMPrice {
	return PublicLLMPrice{
		Input:      price.Input,
		Output:     price.Output,
		CacheRead:  price.CacheRead,
		CacheWrite: price.CacheWrite,
	}
}

// channelStatusResolution 分档价目补齐空对象: 读取侧承诺该字段不为 null。
func channelStatusResolution(resolution map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(resolution))
	for name, price := range resolution {
		out[name] = price
	}
	return out
}

// channelStatusRound4 比值统一保留 4 位小数: 公开 DTO 不吐 0.3333333333333333 这类长尾。
func channelStatusRound4(value float64) float64 { return math.Round(value*10000) / 10000 }
