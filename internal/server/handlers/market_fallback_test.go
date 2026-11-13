package handlers

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// seedDailyMetrics 往 stats_channel_model_dailies 写一条当日记录。
//
// 直接写库而不走 op.ChannelModelDailyStatsUpdate: 那条路径写的是进程内缓存, 要等
// StatsSaveDB 才落库, 而广场读的是库里的持久化数据。回落功能的语义就是"读历史", 所以
// 用例必须先把历史造出来, 不能依赖内存态。
func seedDailyMetrics(t *testing.T, channelModelID int, date string, success, failed int64, waitMs int64) {
	t.Helper()
	row := model.StatsChannelModelDaily{ChannelModelID: channelModelID, Date: date}
	row.RequestSuccess = success
	row.RequestFailed = failed
	row.WaitTime = waitMs
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

// TestMarketFallsBackToDailyMetrics 验证进程重启后的回落: 进程内指标为空, 但近 7 天有持久化
// 统计时, 广场仍应给出数字, 且口径标注为 daily —— 让界面能如实写"来自近 7 天统计"。
func TestMarketFallsBackToDailyMetrics(t *testing.T) {
	_, token := marketAPIEnv(t)
	channel, _ := marketSeed(t, "daily-ch", "#9001", "daily-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, nil)
	// 找该渠道下的渠道模型主键(夹具里它就是唯一一条)。
	var cm model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&cm).Error; err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("20060102")
	seedDailyMetrics(t, cm.ID, today, 90, 10, 250000) // 100 次, 成功率 0.9, 平均 2500ms。

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	payload := marketPayload(t, marketRequest(t, engine, token, ""))
	if len(payload.Models) != 1 {
		t.Fatalf("want 1 model, got %d", len(payload.Models))
	}
	entry := payload.Models[0]
	if entry.Samples != 100 {
		t.Fatalf("samples: want 100, got %d", entry.Samples)
	}
	// 关键断言: 数字来自历史统计, 不能被标成 live。
	if entry.MetricsSource != marketMetricsDaily {
		t.Fatalf("metrics_source: want %q, got %q (进程内无样本, 数字只可能来自日级)", marketMetricsDaily, entry.MetricsSource)
	}
	if entry.Channels[0].MetricsSource != marketMetricsDaily {
		t.Fatalf("channel metrics_source: want %q, got %q", marketMetricsDaily, entry.Channels[0].MetricsSource)
	}
	if diff := entry.Success - 0.9; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("success: want 0.9, got %v", entry.Success)
	}
	// 平均首帧耗时: 250000ms / 100 次 = 2500ms。
	if diff := entry.WaitMs - 2500; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("wait_ms: want 2500, got %v", entry.WaitMs)
	}
}

// TestMarketDailyFallbackRespectsSampleThreshold 验证回落也守同一条样本门槛:
// 一周只跑了 3 次不该被当成"近期成功率 100%", 更不能标成 daily 可信。
func TestMarketDailyFallbackRespectsSampleThreshold(t *testing.T) {
	_, token := marketAPIEnv(t)
	channel, _ := marketSeed(t, "thin-ch", "#9002", "thin-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, nil)
	var cm model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&cm).Error; err != nil {
		t.Fatal(err)
	}
	seedDailyMetrics(t, cm.ID, time.Now().Format("20060102"), 3, 0, 300)

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	entry := marketPayload(t, marketRequest(t, engine, token, "")).Models[0]
	if entry.MetricsSource != marketMetricsInsufficient {
		t.Fatalf("metrics_source: want %q, got %q", marketMetricsInsufficient, entry.MetricsSource)
	}
	// 样本不足时不给数字: 3 次全成功不等于"成功率 100%"。
	if entry.Success != 0 || entry.WaitMs != 0 {
		t.Fatalf("样本不足不该给数字, got success=%v wait=%v", entry.Success, entry.WaitMs)
	}
}

// TestMarketDailyFallbackChannelSharesThreshold 验证回落与 live 守同一条门槛:
// 渠道级样本不足时, 回落的数字也不能出现在响应里。
//
// 这条用例来自一个真实缺陷: 回落分支最初无条件赋值 WaitMs/Success, 于是 gpt-6-luna 在
// 生产数据上以 insufficient 标注却给出 success=42.8% / wait=7.5s —— 标注说"不足以判定",
// 数字却写在旁边, 用户读到的是"这模型成功率四成", 而它其实只跑了 7 次。
func TestMarketDailyFallbackChannelSharesThreshold(t *testing.T) {
	_, token := marketAPIEnv(t)
	channel, _ := marketSeed(t, "share-th-ch", "#9006", "share-th-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, nil)
	var cm model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&cm).Error; err != nil {
		t.Fatal(err)
	}
	seedDailyMetrics(t, cm.ID, time.Now().Format("20060102"), 5, 2, 2000) // 7 次, 不足 30。

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	entry := marketPayload(t, marketRequest(t, engine, token, "")).Models[0]
	if entry.Channels[0].Samples != 7 {
		t.Fatalf("samples: want 7, got %d", entry.Channels[0].Samples)
	}
	// 关键: 标注是 insufficient, 那么旁边就不许有数字。
	if entry.Channels[0].Success != 0 || entry.Channels[0].WaitMs != 0 {
		t.Fatalf("样本不足的回落不该给数字, got success=%v wait=%v", entry.Channels[0].Success, entry.Channels[0].WaitMs)
	}
}

// TestMarketDailyFallbackIgnoresStaleWindow 验证窗口边界: 8 天前的统计不算"近 7 天"。
func TestMarketDailyFallbackIgnoresStaleWindow(t *testing.T) {
	_, token := marketAPIEnv(t)
	channel, _ := marketSeed(t, "stale-ch", "#9003", "stale-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, nil)
	var cm model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&cm).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -8).Format("20060102")
	seedDailyMetrics(t, cm.ID, old, 90, 10, 250000)

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	entry := marketPayload(t, marketRequest(t, engine, token, "")).Models[0]
	if entry.MetricsSource != marketMetricsNone {
		t.Fatalf("8 天前的数据不该被当成近期成绩, got metrics_source=%q", entry.MetricsSource)
	}
	if entry.Samples != 0 {
		t.Fatalf("samples: want 0, got %d", entry.Samples)
	}
}

// TestMarketQualityBandNeedsEnoughSamples 验证质量带只在样本达标时出现, 且只给区间不给精确值。
func TestMarketQualityBandNeedsEnoughSamples(t *testing.T) {
	_, token := marketAPIEnv(t)
	channel, _ := marketSeed(t, "band-ch", "#9004", "band-model", model.MediaKindText, model.LLMPrice{Input: 1, Output: 2}, nil)
	seedQualitySnapshot(t, channel.ID, time.Now().Format("20060102"), 100, 0.998, 1500, 99.95, 10, 0)
	// 再造一条样本不足的渠道, 它的质量带必须整段省略。
	thinChannel, _ := marketSeed(t, "band-thin", "#9005", "band-thin-model", model.MediaKindText, model.LLMPrice{Input: 3, Output: 4}, nil)
	seedQualitySnapshot(t, thinChannel.ID, time.Now().Format("20060102"), 5, 1.0, 1000, 100, 2, 0)

	engine := gin.New()
	engine.GET("/api/v1/market/models", middleware.Auth(), listMarketModels)
	payload := marketPayload(t, marketRequest(t, engine, token, ""))
	if len(payload.Models) != 2 {
		t.Fatalf("want 2 models, got %d", len(payload.Models))
	}
	for _, entry := range payload.Models {
		band := entry.Channels[0].QualityBand
		if entry.Name == "band-model" {
			if band == nil {
				t.Fatalf("样本达标时应有质量带")
			}
			// 只给区间: 精确成功率 0.998 必须落成 "99.5%+" 而不是 "99.8%"。
			if band.Success != "99.5%+" {
				t.Fatalf("success band: want %q, got %q", "99.5%+", band.Success)
			}
			if band.Latency != "<2s" {
				t.Fatalf("latency band: want %q, got %q", "<2s", band.Latency)
			}
			if band.Uptime != "99.9%+" {
				t.Fatalf("uptime band: want %q, got %q", "99.9%+", band.Uptime)
			}
			// 脱敏: 区间文案里不能出现原始数字的长尾(如 "99.8")。
			if containsRawRate(band) {
				t.Fatalf("质量带泄露了精确值: %+v", band)
			}
		}
		if entry.Name == "band-thin-model" && band != nil {
			t.Fatalf("样本不足不该给质量带, got %+v", band)
		}
	}
}

// seedQualitySnapshot 写一条渠道质量快照。
func seedQualitySnapshot(t *testing.T, channelID int, date string, requests int64, successRate float64, avgWaitMs int64, uptimePct float64, checks, fails int64) {
	t.Helper()
	snapshot := model.ChannelQualitySnapshot{
		ChannelID:    channelID,
		Date:         date,
		RequestCount: requests,
		SuccessCount: int64(float64(requests) * successRate),
		SuccessRate:  successRate,
		AvgWaitMs:    avgWaitMs,
		UptimePct:    uptimePct,
		HealthChecks: checks,
		HealthFails:  fails,
	}
	if err := db.GetDB().Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
}

// containsRawRate 检查质量带里是否混入了精确比率(带小数点后的非区间尾巴)。
// 区间文案只有固定几种(99.5%+ / 99%+ / 95%+ / <95%), 出现别的小数就说明把原始值透出去了。
func containsRawRate(band *op.MarketQualityBand) bool {
	suffixes := map[string]bool{"99.5%+": true, "99%+": true, "95%+": true, "<95%": true}
	return !suffixes[band.Success]
}
