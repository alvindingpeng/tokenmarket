package migrate

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestMigration044AfterAutoMigrateIndex 复刻真实升级顺序: AutoMigrate 先跑(它会按新模型给旧表补上
// date 索引), 迁移 44 后跑。索引名在 SQLite 里是库级唯一的, 旧表改名后同名索引仍然占用该名字,
// 若建新表时再建一次就会撞名 —— 这条用例正是为了钉住这个顺序陷阱。
func TestMigration044AfterAutoMigrateIndex(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&legacyHourly{}); err != nil {
		t.Fatalf("automigrate legacy: %v", err)
	}
	if err := conn.Create(&legacyHourly{Hour: 2, Date: "20261107", StatsMetrics: model.StatsMetrics{InputToken: 42}}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 与 db.InitDB 相同的顺序: AutoMigrate -> AfterAutoMigrate。
	if err := conn.AutoMigrate(&model.StatsHourly{}); err != nil {
		t.Fatalf("automigrate new model: %v", err)
	}
	if err := Migration044_HourlyStatsCompositeKey(conn); err != nil {
		t.Fatalf("migration 44: %v", err)
	}
	var row model.StatsHourly
	if err := conn.Where("date = ? AND hour = ?", "20261107", 2).First(&row).Error; err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row.InputToken != 42 {
		t.Fatalf("input = %d, want 42", row.InputToken)
	}
}
