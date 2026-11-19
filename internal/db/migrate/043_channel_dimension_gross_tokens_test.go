package migrate

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// migrate043Fixture 建起渠道维度回填需要的五张表 + 计费明细。
func migrate043Fixture(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.StatsChannelDaily{}, &model.StatsChannelModelDaily{},
		&model.BillingRecord{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return conn
}

// TestMigration043AlignsChannelDimensionsToBillingGross 渠道维度的历史行必须与计费明细的
// 毛输入对齐: 渠道列表、渠道详情、首页渠道榜与模型榜、上游账单核对读的都是这几张表,
// 旧口径(净读)会系统性低估缓存命中率高的渠道。
func TestMigration043AlignsChannelDimensionsToBillingGross(t *testing.T) {
	conn := migrate043Fixture(t)
	now := time.Now()
	day := now.Format("20060102")

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "chan-a"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-1"}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	channelModel := model.ChannelModel{ChannelID: channel.ID, Name: "gpt-x"}
	if err := conn.Create(&channelModel).Error; err != nil {
		t.Fatal(err)
	}

	// 存量五个位置都是旧口径: 只有净读 500。
	net := int64(500)
	if err := conn.Create(&model.StatsChannelDaily{
		ChannelID: channel.ID, Date: day,
		StatsMetrics: model.StatsMetrics{InputToken: net, OutputToken: 400},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsChannelModelDaily{
		ChannelModelID: channelModel.ID, Date: day,
		StatsMetrics: model.StatsMetrics{InputToken: net, OutputToken: 400},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).
		UpdateColumn("input_token", net).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
		UpdateColumn("input_token", net).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.ChannelKey{}).Where("id = ?", key.ID).
		UpdateColumn("input_token", net).Error; err != nil {
		t.Fatal(err)
	}

	// 一条明细: 毛输入 1000 = 净读 500 + 缓存读 300 + 缓存写 200。
	record := model.BillingRecord{
		RequestID: 1, UserID: 1, ChannelID: channel.ID, ModelName: "gpt-x",
		InputToken: net, OutputToken: 400, CacheReadToken: 300, CacheWriteToken: 200,
		Status: model.BillingStatusSettled,
	}
	record.CreatedAt = now
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration043_ChannelDimensionGrossTokens(conn); err != nil {
		t.Fatalf("migration 43: %v", err)
	}

	gross := net + 300 + 200 // 1000
	var daily model.StatsChannelDaily
	if err := conn.Where("channel_id = ? AND date = ?", channel.ID, day).First(&daily).Error; err != nil {
		t.Fatal(err)
	}
	if daily.InputToken != gross {
		t.Fatalf("channel daily input = %d, want gross %d", daily.InputToken, gross)
	}
	var modelDaily model.StatsChannelModelDaily
	if err := conn.Where("channel_model_id = ? AND date = ?", channelModel.ID, day).First(&modelDaily).Error; err != nil {
		t.Fatal(err)
	}
	if modelDaily.InputToken != gross {
		t.Fatalf("channel model daily input = %d, want gross %d", modelDaily.InputToken, gross)
	}
	var reloadedChannel model.Channel
	if err := conn.First(&reloadedChannel, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedChannel.InputToken != gross {
		t.Fatalf("channel input = %d, want gross %d", reloadedChannel.InputToken, gross)
	}
	var reloadedModel model.ChannelModel
	if err := conn.First(&reloadedModel, channelModel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedModel.InputToken != gross {
		t.Fatalf("channel model input = %d, want gross %d", reloadedModel.InputToken, gross)
	}
	var reloadedKey model.ChannelKey
	if err := conn.First(&reloadedKey, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedKey.InputToken != gross {
		t.Fatalf("channel key input = %d, want gross %d", reloadedKey.InputToken, gross)
	}
}

// TestMigration043NeverLowersExistingValues 取大而不取新: 有的渠道行本来就已是毛口径,
// 还有少量高于计费明细(早期日界漂移、明细被清理)。回填只补不回退, 否则历史峰值会被抹平。
func TestMigration043NeverLowersExistingValues(t *testing.T) {
	conn := migrate043Fixture(t)
	now := time.Now()
	day := now.Format("20060102")

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "chan-high"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	// 存量值高于明细毛输入(明细只有 1000)。
	existing := int64(5000)
	if err := conn.Create(&model.StatsChannelDaily{
		ChannelID: channel.ID, Date: day,
		StatsMetrics: model.StatsMetrics{InputToken: existing},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).
		UpdateColumn("input_token", existing).Error; err != nil {
		t.Fatal(err)
	}
	record := model.BillingRecord{
		RequestID: 1, ChannelID: channel.ID, ModelName: "gpt-x",
		InputToken: 500, CacheReadToken: 300, CacheWriteToken: 200,
		Status: model.BillingStatusSettled,
	}
	record.CreatedAt = now
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration043_ChannelDimensionGrossTokens(conn); err != nil {
		t.Fatalf("migration 43: %v", err)
	}
	var daily model.StatsChannelDaily
	if err := conn.Where("channel_id = ? AND date = ?", channel.ID, day).First(&daily).Error; err != nil {
		t.Fatal(err)
	}
	if daily.InputToken != existing {
		t.Fatalf("channel daily input = %d, want unchanged %d", daily.InputToken, existing)
	}
	var reloaded model.Channel
	if err := conn.First(&reloaded, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.InputToken != existing {
		t.Fatalf("channel input = %d, want unchanged %d", reloaded.InputToken, existing)
	}
}

// TestMigration043IsIdempotent 取大逻辑天然幂等: 同一库连跑两次结果必须一致。
func TestMigration043IsIdempotent(t *testing.T) {
	conn := migrate043Fixture(t)
	now := time.Now()
	day := now.Format("20060102")

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "chan-a"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsChannelDaily{
		ChannelID: channel.ID, Date: day,
		StatsMetrics: model.StatsMetrics{InputToken: 100},
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := model.BillingRecord{
		RequestID: 1, ChannelID: channel.ID, ModelName: "gpt-x",
		InputToken: 500, CacheReadToken: 300, CacheWriteToken: 200,
		Status: model.BillingStatusSettled,
	}
	record.CreatedAt = now
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration043_ChannelDimensionGrossTokens(conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := Migration043_ChannelDimensionGrossTokens(conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var daily model.StatsChannelDaily
	if err := conn.Where("channel_id = ? AND date = ?", channel.ID, day).First(&daily).Error; err != nil {
		t.Fatal(err)
	}
	if daily.InputToken != 1000 {
		t.Fatalf("channel daily input = %d after replay, want 1000", daily.InputToken)
	}
}

// TestMigration043SkipsUnmappedRows 渠道模型被改名/删除、渠道已不存在时只能跳过这些桶 ——
// 不许凭空造行, 也不许猜归属。空库与全无明细的库必须是无操作。
func TestMigration043SkipsUnmappedRows(t *testing.T) {
	conn := migrate043Fixture(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "chan-a"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	// 明细指向一个不存在的模型名与已删除的渠道: 两类桶都必须被跳过。
	for _, record := range []model.BillingRecord{
		{RequestID: 1, ChannelID: channel.ID, ModelName: "removed-model", InputToken: 700, Status: model.BillingStatusSettled},
		{RequestID: 2, ChannelID: 0, ModelName: "gpt-x", InputToken: 900, Status: model.BillingStatusSettled},
	} {
		if err := conn.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Migration043_ChannelDimensionGrossTokens(conn); err != nil {
		t.Fatalf("migration 43: %v", err)
	}
	var count int64
	if err := conn.Model(&model.StatsChannelDaily{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migration created %d phantom channel-daily rows, want 0", count)
	}
	var modelDailyCount int64
	if err := conn.Model(&model.StatsChannelModelDaily{}).Count(&modelDailyCount).Error; err != nil {
		t.Fatal(err)
	}
	if modelDailyCount != 0 {
		t.Fatalf("migration created %d phantom channel-model-daily rows, want 0", modelDailyCount)
	}
	var reloaded model.Channel
	if err := conn.First(&reloaded, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.InputToken != 700 {
		t.Fatalf("channel input = %d, want 700 from the mapped record only", reloaded.InputToken)
	}
}
