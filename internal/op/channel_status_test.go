package op

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// channel_status_test.go 钉住公开状态页的口径: 测试隔离、零样本、重试归属、窗口边界、比值、隐私。
// 这条链路直接对匿名访问者开放, 任何"顺手多给一个字段"的改动都必须在这里被拦下。

// resetChannelStatusCache 清空 10s 快照缓存: 测试各自造数, 不清会读到上一个测试的快照。
func resetChannelStatusCache() {
	channelStatusCacheMu.Lock()
	defer channelStatusCacheMu.Unlock()
	channelStatusCache = nil
	channelStatusCacheExpiry = time.Time{}
}

// channelStatusTestEnv 建独立库并预热缓存; 每个测试用独立 TempDir, 互不串库。
func channelStatusTestEnv(t *testing.T) context.Context {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel_status.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ctx := context.Background()
	// 上浮比例显式钉死 0.2: 测试的价格断言不依赖默认设置的取值。
	if err := SettingUpsert(model.SettingKeyMarkupRatio, "0.2"); err != nil {
		t.Fatalf("set markup: %v", err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	resetChannelStatusCache()
	return ctx
}

// channelStatusSeedChannel 直接落库一个渠道; mutate 在写库前改字段(审核态、启用、发布编码等)。
func channelStatusSeedChannel(t *testing.T, code, name string, mutate func(*model.Channel)) model.Channel {
	t.Helper()
	channel := model.Channel{UserID: 1, Shared: true, ShareCode: code}
	channel.ChannelConfig = model.ChannelConfig{Name: name, Enabled: true, BaseURL: "http://upstream.example"}
	channel.ReviewStatus = model.ChannelReviewApproved
	if mutate != nil {
		mutate(&channel)
	}
	// enabled 带 default:true, false 是零值: gorm 建行时让位给列默认值并把 true 回填进结构体。
	// 先取出真值再建行, 落库后显式回写 —— 否则 mutate 置 false 的渠道落库后又变回启用,
	// 供给侧隔离测试会漏掉禁用渠道。
	enabled := channel.Enabled
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel %q: %v", name, err)
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channel.ID).
		Update("enabled", enabled).Error; err != nil {
		t.Fatalf("pin channel enabled %q: %v", name, err)
	}
	channel.Enabled = enabled
	return channel
}

// channelStatusSeedModel 直接落库一个渠道模型; 默认上架。
func channelStatusSeedModel(t *testing.T, channelID int, name string, kind model.MediaKind, supply model.LLMPrice, mutate func(*model.ChannelModel)) model.ChannelModel {
	t.Helper()
	channelModel := model.ChannelModel{ChannelID: channelID, Name: name, Listed: true, Kind: kind, SupplyPrice: supply}
	if mutate != nil {
		mutate(&channelModel)
	}
	if err := db.GetDB().Create(&channelModel).Error; err != nil {
		t.Fatalf("create channel model %q: %v", name, err)
	}
	return channelModel
}

// channelStatusSeedAttempt 落库一次上游尝试(等价 relay 写手的 RelayAttemptSave 首次写)。
func channelStatusSeedAttempt(t *testing.T, requestID uint64, round, channelID int, modelName string, at time.Time, status string, durationMs int64) {
	t.Helper()
	row := model.RelayRequestAttempt{
		RequestID: requestID, Round: round, ChannelID: channelID,
		TargetModel: modelName, StartedAt: at, DurationMs: durationMs, Status: status,
		UpstreamTiming: model.UpstreamTiming{TTFBNs: durationMs * 1_000_000},
	}
	if status == "success" {
		db.GetDB().Where("id = ?", requestID).FirstOrCreate(&model.RelayRequest{ID: requestID, Round: round, Status: "success"})
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("create attempt req=%d round=%d: %v", requestID, round, err)
	}
}

// channelStatusSeedRequest 落库一条请求终态快照(round 即最终轮次)。
func channelStatusSeedRequest(t *testing.T, id uint64, round int, prompt, cached, cacheWrite int64) {
	t.Helper()
	row := model.RelayRequest{
		ID: id, Round: round, Status: "success",
		PromptTokens: prompt, CachedTokens: cached, CacheWriteTokens: cacheWrite,
	}
	if err := db.GetDB().Save(&row).Error; err != nil {
		t.Fatalf("create request %d: %v", id, err)
	}
}

// channelStatusReload 造数后重载渠道缓存(渠道/模型在缓存里定稿供给侧口径)。
func channelStatusReload(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := channelRefreshCache(ctx); err != nil {
		t.Fatalf("refresh channel cache: %v", err)
	}
}

// channelStatusFind 按 (发布编码, 模型名) 取条目。
func channelStatusFind(t *testing.T, snapshot PublicChannelStats, code, modelName string) *PublicChannelModelStats {
	t.Helper()
	for i := range snapshot.Models {
		if snapshot.Models[i].ChannelCode == code && snapshot.Models[i].ModelName == modelName {
			return &snapshot.Models[i]
		}
	}
	return nil
}

// channelStatusMustFind 同上, 找不到即失败。
func channelStatusMustFind(t *testing.T, snapshot PublicChannelStats, code, modelName string) PublicChannelModelStats {
	t.Helper()
	item := channelStatusFind(t, snapshot, code, modelName)
	if item == nil {
		t.Fatalf("模型 %s/%s 不在快照里", code, modelName)
	}
	return *item
}

// channelStatusJSONKeys 序列化后取 JSON 对象的 key 集合: 契约另一端是前端, 只有编码后才算真实形状。
func channelStatusJSONKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func channelStatusSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustFloat(t *testing.T, p *float64, want float64, what string) {
	t.Helper()
	if p == nil {
		t.Fatalf("%s = null, want %v", what, want)
	}
	if diff := *p - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("%s = %v, want %v", what, *p, want)
	}
}

// TestChannelStatusRetryAttribution 重试归属: 用量只归「最终成功尝试」所在的渠道与模型。
// 两处陷阱各钉一针:
//   - 跨渠道错记: 请求 1001 第 1 轮在 A 失败、第 2 轮在 B 成功, 用量只能记给 B, A 的缓存命中率必须是 null;
//   - 重试乘算: 请求 1002 在 B 上重试三轮才成功, 用量只能计一次(join request_id + round, 最多命中一行)。
func TestChannelStatusRetryAttribution(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	chA := channelStatusSeedChannel(t, "1001", "alpha-internal-name", nil)
	chB := channelStatusSeedChannel(t, "1002", "beta-internal-name", nil)
	supply := model.LLMPrice{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}
	channelStatusSeedModel(t, chA.ID, "m-retry", "", supply, nil)
	channelStatusSeedModel(t, chB.ID, "m-retry", "", supply, nil)
	channelStatusReload(t, ctx)

	// 请求 1001: 第 1 轮 A 失败 → 第 2 轮 B 成功。
	channelStatusSeedAttempt(t, 1001, 1, chA.ID, "m-retry", now.Add(-300*time.Second), "failed", 40)
	channelStatusSeedAttempt(t, 1001, 2, chB.ID, "m-retry", now.Add(-290*time.Second), "success", 100)
	channelStatusSeedRequest(t, 1001, 2, 1000, 400, 0)

	// 请求 1002: 同渠道连败两轮, 第 3 轮成功。
	channelStatusSeedAttempt(t, 1002, 1, chB.ID, "m-retry", now.Add(-200*time.Second), "timeout", 5000)
	channelStatusSeedAttempt(t, 1002, 2, chB.ID, "m-retry", now.Add(-190*time.Second), "failed", 50)
	channelStatusSeedAttempt(t, 1002, 3, chB.ID, "m-retry", now.Add(-180*time.Second), "success", 200)
	channelStatusSeedRequest(t, 1002, 3, 500, 100, 0)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// 白盒断言用量归并: join 一次都没多给, 失败渠道一行都没有。
	tokens, err := channelStatusLoadTokens(ctx, now.Add(-900*time.Second), now, []int{chA.ID, chB.ID})
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	if _, ok := tokens[channelStatusKey{channelID: chA.ID, modelName: "m-retry"}]; ok {
		t.Fatal("失败渠道 A 拿到了用量 —— 跨渠道错记")
	}
	bTokens := tokens[channelStatusKey{channelID: chB.ID, modelName: "m-retry"}]
	if bTokens.PromptTokens != 1500 || bTokens.CachedTokens != 500 {
		t.Fatalf("B 用量 = prompt %d cached %d, want 1500/500 —— 重试被重复计入", bTokens.PromptTokens, bTokens.CachedTokens)
	}

	a := channelStatusMustFind(t, snapshot, "1001", "m-retry")
	if a.Attempts != 1 || a.SuccessRate == nil || *a.SuccessRate != 0 {
		t.Fatalf("A 样本 = attempts %d rate %v, want 1/0", a.Attempts, a.SuccessRate)
	}
	if a.LatencyMs != nil {
		t.Fatalf("A 无成功尝试却给出延迟 %d", *a.LatencyMs)
	}
	if a.CacheReadRatio != nil {
		t.Fatalf("A 拿到了缓存命中率 %v —— 失败渠道不得分享最终成功请求的用量", *a.CacheReadRatio)
	}

	b := channelStatusMustFind(t, snapshot, "1002", "m-retry")
	if b.Attempts != 4 {
		t.Fatalf("B attempts = %d, want 4(三轮重试 + 1001 的成功轮)", b.Attempts)
	}
	mustFloat(t, b.SuccessRate, 0.5, "B success_rate")
	// 命中率 = (400+100) / (1000+500): 若重试被乘算会变成 700/2500 = 0.28。
	mustFloat(t, b.CacheReadRatio, 0.3333, "B cache_read_ratio")
	if b.LatencyMs == nil || *b.LatencyMs != 150 {
		t.Fatalf("B latency = %v, want 150(成功尝试 100/200 的均值)", b.LatencyMs)
	}
}

// TestChannelStatusZeroSamplesKeepIdleModels 空闲模型照常列出, 指标一律 null 而不是 0。
// 0 成功率与"没有样本"是两回事, 公开页不能把没流量的模型画成红色。
func TestChannelStatusZeroSamplesKeepIdleModels(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	channel := channelStatusSeedChannel(t, "1003", "idle-channel-name", nil)
	supply := model.LLMPrice{Input: 1, Output: 2}
	channelStatusSeedModel(t, channel.ID, "m-idle", "", supply, nil)
	channelStatusSeedModel(t, channel.ID, "m-unlisted", "", supply, func(cm *model.ChannelModel) { cm.Listed = false })
	channelStatusReload(t, ctx)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(snapshot.Models) != 1 {
		t.Fatalf("models = %d 条, want 1(未上架模型不得出现)", len(snapshot.Models))
	}
	item := channelStatusMustFind(t, snapshot, "1003", "m-idle")
	if item.Attempts != 0 || item.Status != channelStatusNoData {
		t.Fatalf("空闲模型 = attempts %d status %q, want 0/no_data", item.Attempts, item.Status)
	}
	if item.SuccessRate != nil || item.LatencyMs != nil || item.CacheReadRatio != nil {
		t.Fatalf("空闲模型指标必须为 null, 得到 %v %v %v", item.SuccessRate, item.LatencyMs, item.CacheReadRatio)
	}
	if len(item.History) != channelStatusBucketCount {
		t.Fatalf("history = %d 桶, want %d", len(item.History), channelStatusBucketCount)
	}
	for i, bucket := range item.History {
		if bucket.Attempts != 0 || bucket.SuccessRate != nil || bucket.Status != channelStatusNoData {
			t.Fatalf("第 %d 桶 = %+v, want no_data/0/null", i, bucket)
		}
	}
	// null 字段必须真的以 null 出现在 JSON 里(不能被 omitempty 吃掉)。
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"success_rate":null`, `"latency_ms":null`, `"cache_read_ratio":null`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("JSON 缺少 %s: %s", key, raw)
		}
	}
}

// TestChannelStatusWindowAndBucketBoundaries 窗口与分桶边界: 两端闭区间、桶沿窗口铺开、窗外样本(含未来时间)不计。
// Σ桶样本 == 窗口样本是硬约束, 图表 15 根柱子与总数对不上就没人信这张页。
func TestChannelStatusWindowAndBucketBoundaries(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	channel := channelStatusSeedChannel(t, "1004", "boundary-channel-name", nil)
	supply := model.LLMPrice{Input: 1, Output: 1}
	channelStatusSeedModel(t, channel.ID, "m-window", "", supply, nil)
	channelStatusReload(t, ctx)

	// 窗口 [now-900s, now]; 下面每一行都钉一个边界位置。
	channelStatusSeedAttempt(t, 3001, 1, channel.ID, "m-window", now.Add(-901*time.Second), "success", 10) // 左边界外 1s。
	channelStatusSeedRequest(t, 3001, 1, 100, 100, 0)                                                      // 窗外请求的用量同样不得计入。
	channelStatusSeedAttempt(t, 3002, 1, channel.ID, "m-window", now.Add(-900*time.Second), "success", 20) // 左边界(闭) → 桶 0。
	channelStatusSeedRequest(t, 3002, 1, 100, 0, 0)
	channelStatusSeedAttempt(t, 3003, 1, channel.ID, "m-window", now.Add(-840*time.Second), "failed", 30) // 桶 1 起点。
	channelStatusSeedAttempt(t, 3004, 1, channel.ID, "m-window", now.Add(-60*time.Second), "success", 40) // 桶 14 起点。
	channelStatusSeedRequest(t, 3004, 1, 300, 100, 0)
	channelStatusSeedAttempt(t, 3005, 1, channel.ID, "m-window", now, "success", 50) // 右边界(闭) → 仍落桶 14。
	channelStatusSeedRequest(t, 3005, 1, 200, 100, 0)
	channelStatusSeedAttempt(t, 3006, 1, channel.ID, "m-window", now.Add(1*time.Second), "success", 60) // 右边界外(未来)。
	channelStatusSeedRequest(t, 3006, 1, 999, 999, 0)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	item := channelStatusMustFind(t, snapshot, "1004", "m-window")
	if item.Attempts != 4 {
		t.Fatalf("attempts = %d, want 4(900s 左闭右闭, 窗外两行不计)", item.Attempts)
	}
	mustFloat(t, item.SuccessRate, 0.75, "success_rate")
	if item.LatencyMs == nil || *item.LatencyMs != 37 {
		t.Fatalf("latency = %v, want 37((20+40+50)/3 四舍五入)", item.LatencyMs)
	}
	// 用量同样按窗口裁剪: 窗外 3001 与未来 3006 都不计, 命中率 = 200/600。
	mustFloat(t, item.CacheReadRatio, 0.3333, "cache_read_ratio")

	wantBucketAtt := map[int]int64{0: 1, 1: 1, 14: 2}
	total := int64(0)
	for i, bucket := range item.History {
		if bucket.Attempts != wantBucketAtt[i] {
			t.Fatalf("第 %d 桶 attempts = %d, want %d", i, bucket.Attempts, wantBucketAtt[i])
		}
		// 桶起点从窗口左端(now-900s)起算: 15 桶恰好铺满 900s 窗口。
		want := now.Add(-time.Duration(channelStatusWindowSeconds)*time.Second + time.Duration(i*channelStatusBucketSeconds)*time.Second)
		if !bucket.Start.Equal(want) {
			t.Fatalf("第 %d 桶 start = %v, want %v", i, bucket.Start, want)
		}
		total += bucket.Attempts
	}
	if total != item.Attempts {
		t.Fatalf("Σ桶样本 = %d, 窗口样本 = %d —— 两者必须相等", total, item.Attempts)
	}
	if item.History[1].SuccessRate == nil || *item.History[1].SuccessRate != 0 {
		t.Fatalf("第 1 桶 success_rate = %v, want 0", item.History[1].SuccessRate)
	}
}

// TestChannelStatusMetricsIsolatedPerChannelModel 同名模型挂在多个渠道、同渠道多个模型, 指标互不串。
// 归并键必须是 (渠道, 模型) 二元组: 只按模型名归并会让 A 渠道的失败拖垮 B 渠道的同名模型。
func TestChannelStatusMetricsIsolatedPerChannelModel(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	chA := channelStatusSeedChannel(t, "2001", "iso-a-name", nil)
	chB := channelStatusSeedChannel(t, "2002", "iso-b-name", nil)
	supply := model.LLMPrice{Input: 1}
	channelStatusSeedModel(t, chA.ID, "m-iso", "", supply, nil)
	channelStatusSeedModel(t, chB.ID, "m-iso", "", supply, nil)
	channelStatusSeedModel(t, chA.ID, "m-iso-b", "", supply, nil)
	channelStatusReload(t, ctx)

	for i := 0; i < 3; i++ {
		channelStatusSeedAttempt(t, uint64(4000+i), 1, chA.ID, "m-iso", now.Add(-time.Duration(i+1)*time.Minute), "success", 100)
	}
	channelStatusSeedAttempt(t, 4100, 1, chB.ID, "m-iso", now.Add(-1*time.Minute), "failed", 100)
	channelStatusSeedAttempt(t, 4101, 1, chB.ID, "m-iso", now.Add(-2*time.Minute), "failed", 100)
	channelStatusSeedAttempt(t, 4200, 1, chA.ID, "m-iso-b", now.Add(-1*time.Minute), "success", 300)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(snapshot.Models) != 3 {
		t.Fatalf("models = %d 条, want 3", len(snapshot.Models))
	}
	// 定序: 发布编码升序, 同编码按模型名升序 —— 前端渲染顺序由此定稿。
	for i := 1; i < len(snapshot.Models); i++ {
		prev, cur := snapshot.Models[i-1], snapshot.Models[i]
		if prev.ChannelCode > cur.ChannelCode || (prev.ChannelCode == cur.ChannelCode && prev.ModelName > cur.ModelName) {
			t.Fatalf("排序错乱: %s/%s 在 %s/%s 之前", prev.ChannelCode, prev.ModelName, cur.ChannelCode, cur.ModelName)
		}
	}

	aIso := channelStatusMustFind(t, snapshot, "2001", "m-iso")
	if aIso.Attempts != 3 || *aIso.SuccessRate != 1 {
		t.Fatalf("2001/m-iso = %d/%v, want 3/1", aIso.Attempts, aIso.SuccessRate)
	}
	bIso := channelStatusMustFind(t, snapshot, "2002", "m-iso")
	if bIso.Attempts != 2 || *bIso.SuccessRate != 0 {
		t.Fatalf("2002/m-iso = %d/%v, want 2/0 —— 同名模型不得互相串数", bIso.Attempts, bIso.SuccessRate)
	}
	aOther := channelStatusMustFind(t, snapshot, "2001", "m-iso-b")
	if aOther.Attempts != 1 || *aOther.SuccessRate != 1 {
		t.Fatalf("2001/m-iso-b = %d/%v, want 1/1", aOther.Attempts, aOther.SuccessRate)
	}
}

// TestChannelStatusRatiosNullsAndClamp 比值口径: 分母缺失/为 0 即 null, 命中率夹进 [0,1], 官方价缺失整体置空。
func TestChannelStatusRatiosNullsAndClamp(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	channel := channelStatusSeedChannel(t, "1005", "ratio-channel-name", nil)
	supply := model.LLMPrice{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}
	channelStatusSeedModel(t, channel.ID, "m-ratio", "", supply, nil)
	channelStatusSeedModel(t, channel.ID, "m-clamp", "", supply, nil)
	channelStatusSeedModel(t, channel.ID, "m-noofficial", "", supply, nil)
	// 官方价: Output 与 CacheWrite 为 0 —— 这两个分量的比值分母不存在, 必须 null。
	if err := db.GetDB().Create(&model.LLMInfo{Name: "m-ratio", LLMPrice: model.LLMPrice{Input: 2, Output: 0, CacheRead: 1.5, CacheWrite: 0}}).Error; err != nil {
		t.Fatalf("create official price: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		t.Fatalf("refresh llm cache: %v", err)
	}
	channelStatusReload(t, ctx)

	channelStatusSeedAttempt(t, 5001, 1, channel.ID, "m-ratio", now.Add(-1*time.Minute), "success", 100)
	channelStatusSeedRequest(t, 5001, 1, 0, 0, 0) // prompt 为 0: 无数据, 命中率必须 null 而不是 0。
	channelStatusSeedAttempt(t, 5002, 1, channel.ID, "m-clamp", now.Add(-1*time.Minute), "success", 100)
	channelStatusSeedRequest(t, 5002, 1, 100, 250, 0) // 上游自报 cached > prompt: 夹到 1, 不把 >1 的数字推给公开面。
	channelStatusSeedAttempt(t, 5003, 1, channel.ID, "m-noofficial", now.Add(-1*time.Minute), "success", 100)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ratio := channelStatusMustFind(t, snapshot, "1005", "m-ratio")
	// 用户价 = 供货价 × 1.2; 官方 Input 2 / CacheRead 1.5 有分母, Output / CacheWrite 无。
	mustFloat(t, &ratio.UserPrice.Input, 1.2, "user_price.input")
	if ratio.OfficialPrice == nil {
		t.Fatal("m-ratio 在官方价目表里, official_price 不应为 null")
	}
	mustFloat(t, &ratio.OfficialPrice.Input, 2, "official_price.input")
	mustFloat(t, ratio.PriceRatio.Input, 0.6, "price_ratio.input")
	mustFloat(t, ratio.PriceRatio.CacheRead, 2.4, "price_ratio.cache_read") // 3×1.2 / 1.5
	if ratio.PriceRatio.Output != nil || ratio.PriceRatio.CacheWrite != nil {
		t.Fatalf("官方分量为 0 的比值必须为 null, 得到 %v %v", ratio.PriceRatio.Output, ratio.PriceRatio.CacheWrite)
	}
	if ratio.CacheReadRatio != nil {
		t.Fatalf("prompt=0 时命中率必须为 null, 得到 %v", *ratio.CacheReadRatio)
	}

	clamp := channelStatusMustFind(t, snapshot, "1005", "m-clamp")
	if clamp.CacheReadRatio != nil {
		t.Fatal("invalid cached/prompt must be unknown")
	}

	none := channelStatusMustFind(t, snapshot, "1005", "m-noofficial")
	if none.OfficialPrice != nil {
		t.Fatal("官方价目表没有 m-noofficial, official_price 必须为 null")
	}
	if none.PriceRatio.Input != nil || none.PriceRatio.Output != nil || none.PriceRatio.CacheRead != nil || none.PriceRatio.CacheWrite != nil {
		t.Fatalf("官方价缺失时四个比值分量都必须为 null, 得到 %+v", none.PriceRatio)
	}
}

// TestChannelStatusMediaPriceUnits 媒体价保留计价单位, 不折算 token 比值。
func TestChannelStatusMediaPriceUnits(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	channel := channelStatusSeedChannel(t, "1006", "media-channel-name", nil)
	channelStatusSeedModel(t, channel.ID, "m-image", model.MediaKindImage, model.LLMPrice{}, func(cm *model.ChannelModel) {
		cm.MediaSupply = model.MediaPrice{PerImage: 0.5, Resolution: map[string]float64{"1024x1024": 1}}
	})
	channelStatusReload(t, ctx)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	item := channelStatusMustFind(t, snapshot, "1006", "m-image")
	if item.Kind != "image" {
		t.Fatalf("kind = %q, want image", item.Kind)
	}
	// 媒体价同样按上浮比例放大, 单位(每张/每档)原样保留。
	mustFloat(t, &item.UserMedia.PerImage, 0.6, "user_media.per_image")
	mustFloat(t, &item.UserMedia.PerSecond, 0, "user_media.per_second")
	if got := item.UserMedia.Resolution["1024x1024"]; got != 1.2 {
		t.Fatalf("user_media.resolution[1024x1024] = %v, want 1.2", got)
	}
}

// TestChannelStatusStatusThresholds 状态阈值逐档钉死: 0.99 起算正常, 0.50 起算降级, 熔断优先。
func TestChannelStatusStatusThresholds(t *testing.T) {
	cases := []struct {
		successes, attempts int64
		breaker             bool
		want                string
	}{
		{0, 0, false, channelStatusNoData},
		{99, 100, false, channelStatusOperational},
		{98, 100, false, channelStatusDegraded},
		{50, 100, false, channelStatusDegraded},
		{49, 100, false, channelStatusDown},
		{100, 100, true, channelStatusDown}, // 熔断生效中压过一切样本。
	}
	for _, tc := range cases {
		if got := channelStatusStatus(tc.successes, tc.attempts, tc.breaker); got != tc.want {
			t.Fatalf("status(%d,%d,breaker=%v) = %q, want %q", tc.successes, tc.attempts, tc.breaker, got, tc.want)
		}
	}
}

// TestChannelStatusBreakerOverridesStatus 熔断当前状态并入 status: 熔断生效中即 down, 已解除的熔断不背锅。
// 历史桶不回溯熔断: 桶记录的是"当时发生了什么", 熔断是"此刻的路由状态"。
func TestChannelStatusBreakerOverridesStatus(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	channel := channelStatusSeedChannel(t, "1007", "breaker-channel-name", nil)
	supply := model.LLMPrice{Input: 1}
	tripped := now.Add(5 * time.Minute)
	expired := now.Add(-1 * time.Minute)
	channelStatusSeedModel(t, channel.ID, "m-tripped", "", supply, func(cm *model.ChannelModel) { cm.DisabledUntil = &tripped })
	channelStatusSeedModel(t, channel.ID, "m-recovered", "", supply, func(cm *model.ChannelModel) { cm.DisabledUntil = &expired })
	channelStatusReload(t, ctx)

	for i, name := range []string{"m-tripped", "m-recovered"} {
		channelStatusSeedAttempt(t, uint64(6001+i), 1, channel.ID, name, now.Add(-1*time.Minute), "success", 100)
	}

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	trip := channelStatusMustFind(t, snapshot, "1007", "m-tripped")
	if trip.Status != channelStatusDown {
		t.Fatalf("熔断生效中 status = %q, want down", trip.Status)
	}
	if trip.History[14].Status != "low_sample" {
		t.Fatalf("历史桶被熔断改写成 %q —— 桶只反映样本", trip.History[14].Status)
	}
	rec := channelStatusMustFind(t, snapshot, "1007", "m-recovered")
	if rec.Status != "low_sample" {
		t.Fatalf("熔断已解除 status = %q, want operational", rec.Status)
	}
}

// TestChannelStatusSupplyPolicyIsolation 供给侧隔离: 公开面只出「已发布 + 已过审 + 启用中 + 已上架 + 有编码」的渠道模型。
// 状态页是全站清单, 比分享页暴露面更大, 过审闸口按对外候选同标准执行。
func TestChannelStatusSupplyPolicyIsolation(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	supply := model.LLMPrice{Input: 1}

	ok := channelStatusSeedChannel(t, "3001", "ok-channel", nil)
	channelStatusSeedModel(t, ok.ID, "m-listed", "", supply, nil)
	channelStatusSeedModel(t, ok.ID, "m-unlisted", "", supply, func(cm *model.ChannelModel) { cm.Listed = false })

	pending := channelStatusSeedChannel(t, "3002", "pending-channel", func(ch *model.Channel) { ch.ReviewStatus = model.ChannelReviewPending })
	channelStatusSeedModel(t, pending.ID, "m-x", "", supply, nil)
	stale := channelStatusSeedChannel(t, "3003", "stale-channel", func(ch *model.Channel) { ch.ReviewStatus = model.ChannelReviewStale })
	channelStatusSeedModel(t, stale.ID, "m-x", "", supply, nil)
	rejected := channelStatusSeedChannel(t, "3004", "rejected-channel", func(ch *model.Channel) { ch.ReviewStatus = model.ChannelReviewRejected })
	channelStatusSeedModel(t, rejected.ID, "m-x", "", supply, nil)
	draft := channelStatusSeedChannel(t, "3005", "draft-channel", func(ch *model.Channel) { ch.ReviewStatus = model.ChannelReviewDraft })
	channelStatusSeedModel(t, draft.ID, "m-x", "", supply, nil)
	disabled := channelStatusSeedChannel(t, "3006", "disabled-channel", func(ch *model.Channel) { ch.Enabled = false })
	channelStatusSeedModel(t, disabled.ID, "m-x", "", supply, nil)
	unshared := channelStatusSeedChannel(t, "3007", "unshared-channel", func(ch *model.Channel) { ch.Shared = false })
	channelStatusSeedModel(t, unshared.ID, "m-x", "", supply, nil)
	nocode := channelStatusSeedChannel(t, "", "nocode-channel", nil)
	channelStatusSeedModel(t, nocode.ID, "m-x", "", supply, nil)
	channelStatusReload(t, ctx)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(snapshot.Models) != 2 {
		t.Fatalf("models = %v, want listed and disabled listings", snapshot.Models)
	}
	item := channelStatusFind(t, snapshot, "3001", "m-listed")
	if item == nil {
		t.Fatal("3001/m-listed 缺失")
	}
}

// TestChannelStatusPrivacyAllowlistDTOShape 公开 DTO 的字段白名单 + 敏感串零泄漏。
// 这条是隐私红线: 渠道身份只有发布编码, 渠道名/归属者/主键/凭据/上游地址一个字都不许出。
func TestChannelStatusPrivacyAllowlistDTOShape(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)

	owner := model.User{Username: "owner-secret-name", Role: model.RoleReseller, Status: model.StatusActive}
	if err := db.GetDB().Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	channel := channelStatusSeedChannel(t, "4242", "top-secret-channel-name", func(ch *model.Channel) {
		ch.UserID = owner.ID
		ch.BaseURL = "http://secret-upstream.example"
		ch.CustomHeader = []model.CustomHeader{{HeaderKey: "X-Api-Key", HeaderValue: "sk-header-secret"}}
	})
	key := model.ChannelKey{ChannelID: channel.ID}
	key.ChannelKeyConfig = model.ChannelKeyConfig{Name: "k-secret-name", Key: "sk-super-secret-key", Enabled: true}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	channelStatusSeedModel(t, channel.ID, "m-public-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, func(cm *model.ChannelModel) {
		cm.MediaSupply = model.MediaPrice{PerImage: 0.5}
	})
	if err := db.GetDB().Create(&model.LLMInfo{Name: "m-public-model", LLMPrice: model.LLMPrice{Input: 2, Output: 4}}).Error; err != nil {
		t.Fatalf("create official price: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		t.Fatalf("refresh llm cache: %v", err)
	}
	channelStatusReload(t, ctx)

	channelStatusSeedAttempt(t, 7001, 1, channel.ID, "m-public-model", now.Add(-1*time.Minute), "success", 120)
	channelStatusSeedRequest(t, 7001, 1, 1000, 250, 0)

	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)

	// 字段白名单: 加字段必须显式改这里, 免得"顺手多给一个"直接开到公网上。
	wantTop := []string{"generated_at", "models", "refresh_seconds", "window_seconds"}
	if got := channelStatusSortedKeys(channelStatusJSONKeys(t, snapshot)); !reflect.DeepEqual(got, wantTop) {
		t.Fatalf("顶层字段 = %v, want %v", got, wantTop)
	}
	wantModel := []string{
		"attempts", "cache_read_ratio", "channel_code", "history", "kind", "latency_ms",
		"model_name", "official_price", "price_ratio", "status", "success_rate",
		"user_media", "user_price",
	}
	if got := channelStatusSortedKeys(channelStatusJSONKeys(t, snapshot.Models[0])); !reflect.DeepEqual(got, wantModel) {
		t.Fatalf("模型条目字段 = %v, want %v", got, wantModel)
	}
	wantBucket := []string{"attempts", "start", "status", "success_rate"}
	if got := channelStatusSortedKeys(channelStatusJSONKeys(t, snapshot.Models[0].History[0])); !reflect.DeepEqual(got, wantBucket) {
		t.Fatalf("历史桶字段 = %v, want %v", got, wantBucket)
	}
	wantLLM := []string{"cache_read", "cache_write", "input", "output"}
	for name, obj := range map[string]any{"user_price": snapshot.Models[0].UserPrice, "official_price": snapshot.Models[0].OfficialPrice, "price_ratio": snapshot.Models[0].PriceRatio} {
		if got := channelStatusSortedKeys(channelStatusJSONKeys(t, obj)); !reflect.DeepEqual(got, wantLLM) {
			t.Fatalf("%s 字段 = %v, want %v", name, got, wantLLM)
		}
	}
	wantMedia := []string{"per_image", "per_second", "per_video", "resolution"}
	if got := channelStatusSortedKeys(channelStatusJSONKeys(t, snapshot.Models[0].UserMedia)); !reflect.DeepEqual(got, wantMedia) {
		t.Fatalf("user_media 字段 = %v, want %v", got, wantMedia)
	}

	// 身份只有发布编码(裸数字), 窗口参数与约定一致。
	item := channelStatusMustFind(t, snapshot, "4242", "m-public-model")
	if item.ChannelCode != "4242" {
		t.Fatalf("channel_code = %q, want 4242", item.ChannelCode)
	}
	if snapshot.WindowSeconds != 900 || snapshot.RefreshSeconds != 15 {
		t.Fatalf("window/refresh = %d/%d, want 900/15", snapshot.WindowSeconds, snapshot.RefreshSeconds)
	}

	// 敏感串零泄漏: 渠道名、归属者、凭据、上游地址、内部键名一律不得出现在序列化结果里。
	for _, secret := range []string{
		"top-secret-channel-name", "owner-secret-name", "k-secret-name", "sk-super-secret-key",
		"sk-header-secret", "secret-upstream.example", "X-Api-Key",
		"channel_id", "user_id", "channel_name", "owner_name", "base_url",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("公开 DTO 泄漏 %q: %s", secret, text)
		}
	}
}

// TestChannelStatusSnapshotCache 10s 快照缓存: TTL 内重复请求不打库, 过期后重建。
func TestChannelStatusSnapshotCache(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	channel := channelStatusSeedChannel(t, "3001", "cache-channel-name", nil)
	supply := model.LLMPrice{Input: 1}
	channelStatusSeedModel(t, channel.ID, "m-cache", "", supply, nil)
	channelStatusReload(t, ctx)
	channelStatusSeedAttempt(t, 8001, 1, channel.ID, "m-cache", time.Now().Add(-1*time.Minute), "success", 100)

	first, err := ChannelStatusSnapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := channelStatusMustFind(t, first, "3001", "m-cache").Attempts; got != 1 {
		t.Fatalf("首帧 attempts = %d, want 1", got)
	}

	// 新样本进库但缓存未过期: 第二次仍拿旧快照。
	channelStatusSeedAttempt(t, 8002, 1, channel.ID, "m-cache", time.Now(), "success", 100)
	second, err := ChannelStatusSnapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := channelStatusMustFind(t, second, "3001", "m-cache").Attempts; got != 1 {
		t.Fatalf("TTL 内 attempts = %d, want 1(缓存帧)", got)
	}
	if !second.GeneratedAt.Equal(first.GeneratedAt) {
		t.Fatalf("TTL 内 generated_at 变了: %v -> %v", first.GeneratedAt, second.GeneratedAt)
	}

	// 缓存过期后重建, 新样本进来。
	resetChannelStatusCache()
	third, err := ChannelStatusSnapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := channelStatusMustFind(t, third, "3001", "m-cache").Attempts; got != 2 {
		t.Fatalf("过期后 attempts = %d, want 2", got)
	}
}
