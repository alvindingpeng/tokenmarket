package migrate

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

func openMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// upsertHourly 复刻 StatsSaveDB 的落库写法: 按 hour 冲突覆盖。
func upsertHourly(t *testing.T, db *gorm.DB, rows ...model.StatsHourly) {
	t.Helper()
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hour"}}, UpdateAll: true}).Create(&rows).Error; err != nil {
		t.Fatalf("upsert hourly: %v", err)
	}
}

func countHourly(t *testing.T, db *gorm.DB, where string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.StatsHourly{}).Where(where).Count(&n).Error; err != nil {
		t.Fatalf("count hourly: %v", err)
	}
	return n
}

func mustHourlyColumnTypes(t *testing.T, db *gorm.DB) []gorm.ColumnType {
	t.Helper()
	types, err := db.Migrator().ColumnTypes(&model.StatsHourly{})
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	return types
}

// 主键不能再被当成自增列: 否则 Hour=0 会被 GORM 视为未设置而从 INSERT 里省略,
// 午夜桶永远落不进 hour=0, 每次保存反而自增出一条脏行。
func TestStatsHourlyModelDropsAutoIncrement(t *testing.T) {
	db := openMigrateTestDB(t)
	if err := db.AutoMigrate(&model.StatsHourly{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	for _, ct := range mustHourlyColumnTypes(t, db) {
		if ct.Name() != "hour" {
			continue
		}
		if ai, ok := ct.AutoIncrement(); ok && ai {
			t.Fatal("stats_hourlies.hour is still an auto-increment column")
		}
	}

	upsertHourly(t, db,
		model.StatsHourly{Hour: 0, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: 111}},
		model.StatsHourly{Hour: 1, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: 222}},
	)
	if n := countHourly(t, db, "hour = 0"); n != 1 {
		t.Fatalf("expected one row at hour=0, got %d", n)
	}
	if n := countHourly(t, db, "hour < 0 OR hour > 23"); n != 0 {
		t.Fatalf("expected no out-of-range rows, got %d", n)
	}

	// 同一天反复落库必须覆盖同一格, 而不是插新行: 生产上就是这里每天涨上百行。
	for i := 0; i < 5; i++ {
		upsertHourly(t, db, model.StatsHourly{Hour: 0, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: int64(1000 + i)}})
	}
	if n := countHourly(t, db, "hour = 0"); n != 1 {
		t.Fatalf("midnight bucket must update in place, got %d rows", n)
	}
	var midnight model.StatsHourly
	if err := db.Where("hour = 0").First(&midnight).Error; err != nil {
		t.Fatalf("load midnight row: %v", err)
	}
	if midnight.InputToken != 1004 {
		t.Fatalf("expected latest midnight value 1004, got %d", midnight.InputToken)
	}
}

// seedLegacyHourlyTable 按升级前的旧 DDL 建表: hour 是 AUTOINCREMENT, 表里没有 hour=0,
// 只有一串自增出来的脏行, 值随落库次数递增后冻结 —— 与生产实测的形态一致。
func seedLegacyHourlyTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		"CREATE TABLE stats_hourlies (hour integer PRIMARY KEY AUTOINCREMENT, date text NOT NULL, input_token integer, output_token integer, input_cost real, output_cost real, wait_time integer, request_success integer, request_failed integer)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (1, '20261010', 900, 5)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (10, '20261008', 100, 1)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (24, '20261010', 202199, 12)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (25, '20261010', 4410049, 69)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (26, '20261010', 4410049, 69)",
		"INSERT INTO stats_hourlies (hour, date, input_token, request_success) VALUES (131, '20261009', 777, 3)",
	}
	for _, s := range statements {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}
}

func TestRepairHourlyStatsRows(t *testing.T) {
	db := openMigrateTestDB(t)
	seedLegacyHourlyTable(t, db)

	if err := repairHourlyStatsRows(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if n := countHourly(t, db, "hour < 0 OR hour > 23"); n != 0 {
		t.Fatalf("expected garbage rows removed, got %d", n)
	}

	want := []struct {
		hour  int
		input int64
		date  string
	}{
		{0, 4410049, "20261010"},
		{1, 900, "20261010"},
		{10, 100, "20261008"},
	}
	for _, row := range want {
		var got model.StatsHourly
		if err := db.Where("hour = ?", row.hour).First(&got).Error; err != nil {
			t.Fatalf("row hour=%d missing: %v", row.hour, err)
		}
		if got.InputToken != row.input || got.Date != row.date {
			t.Fatalf("row hour=%d = (%d,%s), want (%d,%s)", row.hour, got.InputToken, got.Date, row.input, row.date)
		}
	}

	// 修复后再走一次落库路径: 午夜桶必须原地更新, 不再产生脏行。
	if err := db.AutoMigrate(&model.StatsHourly{}); err != nil {
		t.Fatalf("automigrate after repair: %v", err)
	}
	for i := 0; i < 3; i++ {
		upsertHourly(t, db, model.StatsHourly{Hour: 0, Date: "20261011", StatsMetrics: model.StatsMetrics{InputToken: int64(5000 + i)}})
	}
	if n := countHourly(t, db, "hour < 0 OR hour > 23"); n != 0 {
		t.Fatalf("out-of-range rows reappeared: %d", n)
	}
	var midnight model.StatsHourly
	if err := db.Where("hour = 0").First(&midnight).Error; err != nil {
		t.Fatalf("load midnight: %v", err)
	}
	if midnight.InputToken != 5002 || midnight.Date != "20261011" {
		t.Fatalf("midnight bucket wrong: %+v", midnight)
	}
}

func TestRepairHourlyStatsRowsIdempotentAndEmpty(t *testing.T) {
	db := openMigrateTestDB(t)
	if err := db.AutoMigrate(&model.StatsHourly{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	// 空表可直接跑, 不该报错。
	if err := repairHourlyStatsRows(db); err != nil {
		t.Fatalf("repair on empty table: %v", err)
	}

	rows := []model.StatsHourly{
		{Hour: 5, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: 40}},
		{Hour: 24, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: 900}},
		{Hour: 25, Date: "20261010", StatsMetrics: model.StatsMetrics{InputToken: 1200}},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repairHourlyStatsRows(db); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	first := countHourly(t, db, "1 = 1")
	if err := repairHourlyStatsRows(db); err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if second := countHourly(t, db, "1 = 1"); second != first {
		t.Fatalf("repair not idempotent: %d then %d rows", first, second)
	}
	var midnight model.StatsHourly
	if err := db.Where("hour = 0").First(&midnight).Error; err != nil {
		t.Fatalf("midnight row should be recovered: %v", err)
	}
	if midnight.InputToken != 1200 {
		t.Fatalf("expected newest copy 1200 at hour=0, got %d", midnight.InputToken)
	}
}
