package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// statsDimRow 构造一条带缓存词元的计费行。
func statsDimRow(userID uint, at time.Time, input, output, cacheRead, cacheWrite int64) model.BillingRecord {
	return model.BillingRecord{
		UserID:          userID,
		ModelName:       "dim-model",
		Status:          model.BillingStatusSettled,
		UserCost:        1,
		InputToken:      input,
		OutputToken:     output,
		CacheReadToken:  cacheRead,
		CacheWriteToken: cacheWrite,
		CreatedAt:       at,
	}
}

// TestUserStatsOverviewTokenBreakdown 词元拆分必须自洽: 四项之和等于总数, 命中率按 cacheRead/(cacheRead+input)。
func TestUserStatsOverviewTokenBreakdown(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	now := time.Now()
	userStatsInsert(t,
		statsDimRow(payer.ID, now.AddDate(0, 0, -2), 1000, 500, 3000, 200),
		statsDimRow(payer.ID, now.AddDate(0, 0, -1), 2000, 800, 5000, 400),
	)

	overview, err := UserStatsOverview(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.InputTokens != 3000 || overview.OutputTokens != 1300 {
		t.Fatalf("input=%d output=%d, want 3000/1300", overview.InputTokens, overview.OutputTokens)
	}
	if overview.CacheReadTokens != 8000 || overview.CacheWriteTokens != 600 {
		t.Fatalf("cacheRead=%d cacheWrite=%d, want 8000/600", overview.CacheReadTokens, overview.CacheWriteTokens)
	}
	// 拆分必须与总数同源, 否则页面会出现"明细加起来不等于总数"的自相矛盾。
	sum := overview.InputTokens + overview.OutputTokens + overview.CacheReadTokens + overview.CacheWriteTokens
	if sum != overview.TotalTokens {
		t.Fatalf("breakdown sum=%d != total=%d", sum, overview.TotalTokens)
	}
	// 8000/(8000+3000) = 72.72...%
	want := 8000 * 100.0 / 11000
	if diff := overview.CacheHitRate - want; diff > 0.01 || diff < -0.01 {
		t.Fatalf("cache hit rate = %v, want %v", overview.CacheHitRate, want)
	}
}

// TestUserStatsCacheHitRateExcludesOutput 输出不参与命中率分母, 否则命中率会被无谓压低。
func TestUserStatsCacheHitRateExcludesOutput(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	userStatsInsert(t, statsDimRow(payer.ID, time.Now().AddDate(0, 0, -1), 1000, 9000, 1000, 0))

	overview, err := UserStatsOverview(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	// 若把 output 计入分母会得到 1000/11000≈9.09%; 正确口径是 1000/2000=50%。
	if overview.CacheHitRate < 49.9 || overview.CacheHitRate > 50.1 {
		t.Fatalf("cache hit rate = %v, want 50 (output must not be in denominator)", overview.CacheHitRate)
	}
}

// TestUserStatsNoCacheDataIsZeroNotNaN 无缓存数据时命中率必须是 0, 不能把 NaN 传给前端。
func TestUserStatsNoCacheDataIsZeroNotNaN(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	overview, err := UserStatsOverview(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.CacheHitRate != 0 {
		t.Fatalf("empty hit rate = %v, want 0", overview.CacheHitRate)
	}
}

// TestUserStatsModelBreakdownCarriesCache 按模型的缓存命中率独立计算, 不受其他模型影响。
func TestUserStatsModelBreakdownCarriesCache(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	now := time.Now()
	userStatsInsert(t,
		model.BillingRecord{UserID: payer.ID, ModelName: "cached", Status: model.BillingStatusSettled, UserCost: 2,
			InputToken: 1000, CacheReadToken: 3000, CreatedAt: now.AddDate(0, 0, -1)},
		model.BillingRecord{UserID: payer.ID, ModelName: "plain", Status: model.BillingStatusSettled, UserCost: 1,
			InputToken: 4000, CreatedAt: now.AddDate(0, 0, -1)},
	)

	rows, err := UserStatsModelBreakdown(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	byName := map[string]model.UserStatsModel{}
	for _, row := range rows {
		byName[row.ModelName] = row
	}
	if got := byName["cached"].CacheHitRate; got < 74.9 || got > 75.1 {
		t.Fatalf("cached hit rate = %v, want 75", got)
	}
	if got := byName["plain"].CacheHitRate; got != 0 {
		t.Fatalf("plain hit rate = %v, want 0", got)
	}
}

// relayStatsNextID 调用日志的主键手动分配(与进程内请求共用一个 ID 空间, 不自增),
// 测试里必须自己给唯一值, 否则同一测试内的多行会撞主键。
var relayStatsNextID uint64 = 900000

// relayStatsRow 构造一条调用日志行, 用于延迟统计。
func relayStatsRow(userID uint, at time.Time, status string, firstTokenMs, durationMs, ttfbMs int64) model.RelayRequest {
	relayStatsNextID++
	return model.RelayRequest{
		ID:                   relayStatsNextID,
		UserID:               userID,
		Status:               status,
		StartedAt:            at,
		FirstTokenDurationNs: firstTokenMs * int64(time.Millisecond),
		DurationNs:           durationMs * int64(time.Millisecond),
		UpstreamTiming:       model.UpstreamTiming{TTFBNs: ttfbMs * int64(time.Millisecond)},
	}
}

// TestUserStatsLatencyAveragesAndP95 均值与 P95 都基于成功请求, 失败请求不得拉低延迟。
func TestUserStatsLatencyAveragesAndP95(t *testing.T) {
	payer, other := userStatsTestSetup(t)
	now := time.Now()
	rows := []model.RelayRequest{
		relayStatsRow(payer.ID, now.Add(-3*time.Hour), "success", 100, 1000, 80),
		relayStatsRow(payer.ID, now.Add(-2*time.Hour), "success", 200, 2000, 160),
		relayStatsRow(payer.ID, now.Add(-1*time.Hour), "success", 300, 3000, 240),
		// 失败请求耗时被取消截断, 若计入会把均值拉低到失真。
		relayStatsRow(payer.ID, now.Add(-30*time.Minute), "failed", 1, 1, 1),
		// 其他用户的请求绝不能混进来。
		relayStatsRow(other.ID, now.Add(-20*time.Minute), "success", 9999, 9999, 9999),
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("insert relay rows: %v", err)
	}

	latency, err := UserStatsLatency(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("latency: %v", err)
	}
	if latency.SampleCount != 3 {
		t.Fatalf("sample count = %d, want 3 (success only, own rows only)", latency.SampleCount)
	}
	if latency.AvgFirstTokenMs < 199.9 || latency.AvgFirstTokenMs > 200.1 {
		t.Fatalf("avg first token = %v, want 200", latency.AvgFirstTokenMs)
	}
	if latency.AvgDurationMs < 1999.9 || latency.AvgDurationMs > 2000.1 {
		t.Fatalf("avg duration = %v, want 2000", latency.AvgDurationMs)
	}
	if latency.AvgUpstreamTTFBMs < 159.9 || latency.AvgUpstreamTTFBMs > 160.1 {
		t.Fatalf("avg upstream ttfb = %v, want 160", latency.AvgUpstreamTTFBMs)
	}
	// 3 个样本的 P95 落在最大值 300ms 上。
	if latency.P95FirstTokenMs != 300 {
		t.Fatalf("p95 = %v, want 300", latency.P95FirstTokenMs)
	}
	if latency.CoverageStart == "" {
		t.Fatal("coverage start must be reported when samples exist")
	}
}

// TestUserStatsLatencyCoverageStartReflectsRealData 覆盖起点必须是有数据的最早一天,
// 这样调用日志被清理后页面能如实提示"只看得到最近 N 天"。
func TestUserStatsLatencyCoverageStartReflectsRealData(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	day := time.Now().AddDate(0, 0, -2)
	row := relayStatsRow(payer.ID, day, "success", 120, 1200, 100)
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}

	latency, err := UserStatsLatency(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("latency: %v", err)
	}
	if want := day.Format("2006-01-02"); latency.CoverageStart != want {
		t.Fatalf("coverage start = %q, want %q", latency.CoverageStart, want)
	}
	// 留存天数必须回传, 前端用它解释覆盖范围。
	if latency.RetentionDays <= 0 {
		t.Fatalf("retention days = %d, want positive", latency.RetentionDays)
	}
}

// TestUserStatsLatencyEmptyIsZeroNotNaN 无数据时所有数值为 0, 覆盖起点为空串。
func TestUserStatsLatencyEmptyIsZeroNotNaN(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	latency, err := UserStatsLatency(context.Background(), payer.ID, "", "")
	if err != nil {
		t.Fatalf("latency: %v", err)
	}
	if latency.SampleCount != 0 || latency.AvgFirstTokenMs != 0 || latency.P95FirstTokenMs != 0 {
		t.Fatalf("empty latency = %+v, want zeros", latency)
	}
	if latency.CoverageStart != "" {
		t.Fatalf("coverage start = %q, want empty", latency.CoverageStart)
	}
}

// TestCacheHitRateHelper 直接钉住口径, 防止后续重构改坏分母。
func TestCacheHitRateHelper(t *testing.T) {
	cases := []struct {
		cacheRead, input int64
		want             float64
	}{
		{0, 0, 0},
		{0, 100, 0},
		{100, 0, 100},
		{50, 50, 50},
		{3000, 1000, 75},
	}
	for _, c := range cases {
		if got := cacheHitRate(c.cacheRead, c.input); got != c.want {
			t.Fatalf("cacheHitRate(%d,%d) = %v, want %v", c.cacheRead, c.input, got, c.want)
		}
	}
}
