package migrate

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestMigration041BackfillsCacheTokensIntoStatsTotal 修复后存量汇总必须补回缓存词元:
// 旧口径 stats_totals.input_token 只含净读, 缓存读/写之和存在 billing_records 里;
// 迁移后 input_token 应等于 毛输入, 使 总词元 = 输入 + 输出 与明细对齐。
func TestMigration041BackfillsCacheTokensIntoStatsTotal(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.StatsTotal{}, &model.BillingRecord{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// 旧口径存量: 净读 500(毛输入1000 - 缓存读300 - 缓存写200), 输出 500。
	if err := conn.Create(&model.StatsTotal{StatsMetrics: model.StatsMetrics{InputToken: 500, OutputToken: 500}}).Error; err != nil {
		t.Fatal(err)
	}
	record := model.BillingRecord{
		RequestID:       1,
		UserID:          1,
		ModelName:       "m",
		InputToken:      500,
		OutputToken:     500,
		CacheReadToken:  300,
		CacheWriteToken: 200,
		Status:          model.BillingStatusSettled,
	}
	if err := conn.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration041_StatsInputGrossTokens(conn); err != nil {
		t.Fatalf("migration 41: %v", err)
	}
	var total model.StatsTotal
	if err := conn.First(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total.InputToken != 1000 {
		t.Fatalf("input token = %d, want gross 1000 (500 + 300 + 200)", total.InputToken)
	}
	if total.OutputToken != 500 {
		t.Fatalf("output token = %d, want untouched 500", total.OutputToken)
	}
}

// TestMigration041SkipsEmptyOrCachelessDatabases 无明细库与零缓存库零操作。
func TestMigration041SkipsEmptyOrCachelessDatabases(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.StatsTotal{}, &model.BillingRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsTotal{StatsMetrics: model.StatsMetrics{InputToken: 100, OutputToken: 50}}).Error; err != nil {
		t.Fatal(err)
	}
	// 无任何计费明细。
	if err := Migration041_StatsInputGrossTokens(conn); err != nil {
		t.Fatalf("migration 41: %v", err)
	}
	var total model.StatsTotal
	if err := conn.First(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total.InputToken != 100 {
		t.Fatalf("input token = %d, want untouched 100", total.InputToken)
	}

	// 有明细但无缓存词元: 也不应改动。
	plain := model.BillingRecord{RequestID: 2, UserID: 1, ModelName: "m", InputToken: 10, OutputToken: 5, Status: model.BillingStatusSettled}
	if err := conn.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migration041_StatsInputGrossTokens(conn); err != nil {
		t.Fatalf("migration 41: %v", err)
	}
	if err := conn.First(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total.InputToken != 100 {
		t.Fatalf("input token = %d, want untouched 100 (cacheless records)", total.InputToken)
	}
}
