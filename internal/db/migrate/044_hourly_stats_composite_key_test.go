package migrate

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// legacyHourly 复刻迁移 44 之前的表结构: 单列 hour 主键 + 独立的 date 列。
// 必须手工建表, 因为模型已经改成复合主键, AutoMigrate 不会再生成旧结构。
type legacyHourly struct {
	Hour int    `gorm:"primaryKey;autoIncrement:false"`
	Date string `gorm:"not null"`
	model.StatsMetrics
}

func (legacyHourly) TableName() string { return "stats_hourlies" }

// TestMigration044RebuildsCompositeKey 旧库升级后必须变成 (date, hour) 复合主键:
// 单列主键会让第二天第一次落库覆盖掉昨天同一小时的值, 小时曲线永远只剩今天。
func TestMigration044RebuildsCompositeKey(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&legacyHourly{}); err != nil {
		t.Fatalf("automigrate legacy: %v", err)
	}
	// 同一天两小时的正常行 + 一条越界的副本行(017 之前的自增主键缺陷)。
	rows := []legacyHourly{
		{Hour: 0, Date: "20261107", StatsMetrics: model.StatsMetrics{InputToken: 100}},
		{Hour: 5, Date: "20261107", StatsMetrics: model.StatsMetrics{InputToken: 200}},
		{Hour: 24, Date: "20261107", StatsMetrics: model.StatsMetrics{InputToken: 300}},
	}
	if err := conn.Create(&rows).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	if err := Migration044_HourlyStatsCompositeKey(conn); err != nil {
		t.Fatalf("migration 44: %v", err)
	}

	columns, ok := hourlyPrimaryKeyColumns(conn)
	if !ok {
		t.Fatal("cannot read primary key after rebuild")
	}
	if len(columns) != 2 || !containsFold(columns, "date") || !containsFold(columns, "hour") {
		t.Fatalf("primary key = %v, want (date, hour)", columns)
	}

	// 0-23 点的两行搬到新表, 越界的副本行被丢弃。
	var kept []model.StatsHourly
	if err := conn.Order("hour").Find(&kept).Error; err != nil {
		t.Fatalf("read rebuilt rows: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d rows, want 2", len(kept))
	}
	if kept[0].Hour != 0 || kept[0].InputToken != 100 || kept[1].Hour != 5 || kept[1].InputToken != 200 {
		t.Fatalf("rows after rebuild = %+v", kept)
	}
}

// TestMigration044KeepsEveryDayDistinct 迁移后同一天不同小时、不同天同一小时必须各成一桶;
// 这正是旧主键做不到的事(第二天写入会覆盖昨天)。
func TestMigration044KeepsEveryDayDistinct(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.StatsHourly{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := Migration044_HourlyStatsCompositeKey(conn); err != nil {
		t.Fatalf("migration 44 on new-shape table: %v", err)
	}
	for _, row := range []model.StatsHourly{
		{Hour: 3, Date: "20261107", StatsMetrics: model.StatsMetrics{InputToken: 10}},
		{Hour: 3, Date: "20261108", StatsMetrics: model.StatsMetrics{InputToken: 20}},
		{Hour: 4, Date: "20261108", StatsMetrics: model.StatsMetrics{InputToken: 30}},
	} {
		if err := conn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "date"}, {Name: "hour"}},
			UpdateAll: true,
		}).Create(&row).Error; err != nil {
			t.Fatalf("upsert %+v: %v", row, err)
		}
	}
	var count int64
	if err := conn.Model(&model.StatsHourly{}).Where("hour = 3").Count(&count).Error; err != nil {
		t.Fatalf("count hour=3: %v", err)
	}
	if count != 2 {
		t.Fatalf("hour=3 kept %d rows, want 2 (one per date)", count)
	}
}
