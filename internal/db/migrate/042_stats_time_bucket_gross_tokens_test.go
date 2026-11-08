package migrate

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestMigration042BackfillsTimeBucketCacheTokens 趋势图/热力图/API Key页的历史行
// 也必须补回缓存词元: 按明细 created_at(本地时区)落入对应日期桶、小时桶与 API Key 行。
func TestMigration042BackfillsTimeBucketCacheTokens(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{}, &model.BillingRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	now := time.Now()
	day := now.Format("20060102")
	hour := now.Hour()
	// 存量时间桶(旧口径, input 只有净读)。
	if err := conn.Create(&model.StatsDaily{Date: day, StatsMetrics: model.StatsMetrics{InputToken: 500, OutputToken: 500}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsHourly{Hour: hour, Date: day, StatsMetrics: model.StatsMetrics{InputToken: 500, OutputToken: 500}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsAPIKey{APIKeyID: 7, StatsMetrics: model.StatsMetrics{InputToken: 500, OutputToken: 500}}).Error; err != nil {
		t.Fatal(err)
	}
	// 一条带缓存读写的明细: 毛输入 1000 = 净读 500 + 缓存读 300 + 缓存写 200。
	record := model.BillingRecord{
		RequestID: 1, UserID: 1, APIKeyID: 7, ModelName: "m",
		InputToken: 500, OutputToken: 500, CacheReadToken: 300, CacheWriteToken: 200,
		Status: model.BillingStatusSettled,
	}
	record.CreatedAt = now
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration042_StatsTimeBucketGrossTokens(conn); err != nil {
		t.Fatalf("migration 42: %v", err)
	}
	var daily model.StatsDaily
	if err := conn.Where("date = ?", day).First(&daily).Error; err != nil {
		t.Fatal(err)
	}
	var hourly model.StatsHourly
	if err := conn.Where("date = ? AND hour = ?", day, hour).First(&hourly).Error; err != nil {
		t.Fatal(err)
	}
	var apiKey model.StatsAPIKey
	if err := conn.Where("api_key_id = ?", 7).First(&apiKey).Error; err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]int64{"daily": daily.InputToken, "hourly": hourly.InputToken, "api_key": apiKey.InputToken} {
		if got != 1000 {
			t.Fatalf("%s input token = %d, want gross 1000 (500 + 300 + 200)", name, got)
		}
	}
}

// TestMigration042SkipsMissingBuckets 明细时刻没有对应统计行时跳过, 不凭空造行。
func TestMigration042SkipsMissingBuckets(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{}, &model.BillingRecord{}); err != nil {
		t.Fatal(err)
	}
	record := model.BillingRecord{
		RequestID: 2, UserID: 1, APIKeyID: 8, ModelName: "m",
		InputToken: 100, OutputToken: 50, CacheReadToken: 30, CacheWriteToken: 20,
		Status: model.BillingStatusSettled,
	}
	record.CreatedAt = time.Now()
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	// 不建任何统计行。
	if err := Migration042_StatsTimeBucketGrossTokens(conn); err != nil {
		t.Fatalf("migration 42: %v", err)
	}
	var dailyCount, hourlyCount, apiKeyCount int64
	_ = conn.Model(&model.StatsDaily{}).Count(&dailyCount)
	_ = conn.Model(&model.StatsHourly{}).Count(&hourlyCount)
	_ = conn.Model(&model.StatsAPIKey{}).Count(&apiKeyCount)
	if dailyCount != 0 || hourlyCount != 0 || apiKeyCount != 0 {
		t.Fatalf("expected no rows created, got daily=%d hourly=%d apikey=%d", dailyCount, hourlyCount, apiKeyCount)
	}
}
