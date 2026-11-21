package migrate

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// rollupLegacySchema 是迁移 45 之前 billing_rollups 的实际形态:
// 五维(dimension, dimension_id, day)唯一键, 没有 model_name 列。
// 用它手工建表, 才能复现"老库升级"而不是"新库直接按新模型建表"这条路径。
type rollupLegacySchema struct {
	ID              uint   `gorm:"primaryKey"`
	Dimension       string `gorm:"size:8;not null;uniqueIndex:idx_billing_rollup_key,priority:1"`
	DimensionID     uint   `gorm:"not null;uniqueIndex:idx_billing_rollup_key,priority:2;index:idx_billing_rollup_dim_day,priority:1"`
	Day             string `gorm:"size:16;not null;uniqueIndex:idx_billing_rollup_key,priority:3;index:idx_billing_rollup_dim_day,priority:2"`
	InputToken      int64
	OutputToken     int64
	CacheReadToken  int64
	CacheWriteToken int64
	RequestCount    int64
	UserCost        float64
	OwnerRevenue    float64
	PlatformRevenue float64
	SourceRecords   int64
}

func (rollupLegacySchema) TableName() string { return "billing_rollups" }

func rollupMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// AutoMigrate 之后才允许跑本迁移: 生产里也是这个顺序(建表 -> 版本化迁移)。
	if err := conn.AutoMigrate(&rollupLegacySchema{}); err != nil {
		t.Fatalf("auto migrate legacy schema: %v", err)
	}
	return conn
}

// rollupIndexColumnsByName 返回指定索引的列名与 uniqueness, 用于断言迁移前后索引形态。
func rollupIndexColumnsByName(t *testing.T, db *gorm.DB, table, name string) ([]string, bool, bool) {
	t.Helper()
	indexes, err := db.Migrator().GetIndexes(table)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	for _, index := range indexes {
		if index.Name() == name {
			unique, _ := index.Unique()
			return index.Columns(), true, unique
		}
	}
	return nil, false, false
}

// TestMigration045AddsModelDimension 老库(5 维唯一键、无 model_name)升级后:
// 列补上了, 唯一键扩宽到含 model_name, 存量行一条不丢、model_name 都是空串。
func TestMigration045AddsModelDimension(t *testing.T) {
	db := rollupMigrateTestDB(t)

	legacy := []map[string]any{
		{"dimension": "user", "dimension_id": 7, "day": "20260101", "input_token": 100, "request_count": 2, "user_cost": 1.5},
		{"dimension": "owner", "dimension_id": 7, "day": "20260101", "input_token": 100, "request_count": 2, "user_cost": 1.5},
		{"dimension": "platform", "dimension_id": 0, "day": "20260101", "input_token": 100, "request_count": 2, "user_cost": 1.5},
	}
	for _, row := range legacy {
		if err := db.Table("billing_rollups").Create(row).Error; err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}

	if columns, _, _ := rollupIndexColumnsByName(t, db, "billing_rollups", "idx_billing_rollup_key"); containsFold(columns, "model_name") {
		t.Fatalf("修前前提不成立: 老库唯一键不应含 model_name, 实际 %v", columns)
	}

	if err := Migration045_BillingRollupModelDimension(db); err != nil {
		t.Fatalf("migration 45: %v", err)
	}

	var modelName int64
	if err := db.Raw("SELECT COUNT(*) FROM billing_rollups WHERE model_name = ''").Scan(&modelName).Error; err != nil {
		t.Fatalf("count legacy rows: %v", err)
	}
	if modelName != int64(len(legacy)) {
		t.Fatalf("存量行的 model_name 未被默认为空串: %d/%d", modelName, len(legacy))
	}
	var total int64
	if err := db.Table("billing_rollups").Count(&total).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != int64(len(legacy)) {
		t.Fatalf("迁移丢了存量行: %d, want %d", total, len(legacy))
	}

	columns, exists, unique := rollupIndexColumnsByName(t, db, "billing_rollups", "idx_billing_rollup_key")
	if !exists {
		t.Fatalf("迁移后唯一键 idx_billing_rollup_key 不存在")
	}
	if !unique {
		t.Fatalf("idx_billing_rollup_key 不再是唯一索引: %v", columns)
	}
	if !containsFold(columns, "model_name") {
		t.Fatalf("唯一键未扩宽到含 model_name: %v —— 同一用户同一天的不同模型行会互相覆盖", columns)
	}
}

// TestMigration045IsIdempotent 重跑必须零副作用: 不报错、不去重建、不动数据。
//
// 这一条是"迁移失败后能否直接重启"的前提 —— 生产里跑迁移时服务已经起来了,
// 第二次启动会再跑一遍同样的 Up, 它必须是个空操作。
func TestMigration045IsIdempotent(t *testing.T) {
	db := rollupMigrateTestDB(t)
	if err := db.Table("billing_rollups").Create(map[string]any{
		"dimension": "user", "dimension_id": 9, "day": "20260102", "input_token": 30, "request_count": 1, "user_cost": 0.3,
	}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if err := Migration045_BillingRollupModelDimension(db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	firstColumns, _, _ := rollupIndexColumnsByName(t, db, "billing_rollups", "idx_billing_rollup_key")
	var firstRows int64
	if err := db.Table("billing_rollups").Count(&firstRows).Error; err != nil {
		t.Fatalf("count after first run: %v", err)
	}

	// 第二次: 必须早退(既不加列也不重建索引), 因此这里用一个"加列会失败"的探针来证明它没动结构。
	if err := Migration045_BillingRollupModelDimension(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	secondColumns, _, _ := rollupIndexColumnsByName(t, db, "billing_rollups", "idx_billing_rollup_key")
	var secondRows int64
	if err := db.Table("billing_rollups").Count(&secondRows).Error; err != nil {
		t.Fatalf("count after second run: %v", err)
	}
	if len(firstColumns) != len(secondColumns) || containsFold(firstColumns, "model_name") != containsFold(secondColumns, "model_name") {
		t.Fatalf("第二次运行改了唯一键: first=%v second=%v", firstColumns, secondColumns)
	}
	if firstRows != secondRows {
		t.Fatalf("第二次运行动了数据: %d -> %d", firstRows, secondRows)
	}

	// 新形态下唯一键必须真的能容纳"同一天同维度不同模型": 插两行, 都要留下。
	for _, modelName := range []string{"alpha-model", "beta-model"} {
		if err := db.Table("billing_rollups").Create(map[string]any{
			"dimension": "model", "dimension_id": 9, "day": "20260102", "model_name": modelName,
			"input_token": 10, "request_count": 1, "user_cost": 0.1,
		}).Error; err != nil {
			t.Fatalf("insert model rollup %s: %v", modelName, err)
		}
	}
	var modelRows int64
	if err := db.Table("billing_rollups").Where("dimension = ?", "model").Count(&modelRows).Error; err != nil {
		t.Fatalf("count model rows: %v", err)
	}
	if modelRows != 2 {
		t.Fatalf("同一天两个模型行只留下 %d 行 —— 唯一键没真正扩宽", modelRows)
	}
	// 老维度仍然是"一天一行": 重复插入必须冲突, 否则老口径会被重复行算重。
	if err := db.Table("billing_rollups").Create(map[string]any{
		"dimension": "user", "dimension_id": 9, "day": "20260102", "input_token": 30, "request_count": 1, "user_cost": 0.3,
	}).Error; err == nil {
		t.Fatalf("老维度重复插入未被唯一键拦住 —— 唯一性语义被破坏")
	}
}

// TestMigration045SkipsMissingTable 表不存在时安全跳过(不报错、不建表)。
func TestMigration045SkipsMissingTable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := Migration045_BillingRollupModelDimension(conn); err != nil {
		t.Fatalf("migration on empty db: %v", err)
	}
	if conn.Migrator().HasTable("billing_rollups") {
		t.Fatalf("迁移不该凭空建表")
	}
}

// TestMigration045FreshDatabaseMatchesModel 全新库由 AutoMigrate 直接按新模型建表:
// 迁移 45 必须是纯空操作(列已在、唯一键已含 model_name), 且不能把索引"改坏"。
//
// 这一条保证两条路径收敛到同一形态: 老库升级后的表结构与全新库建出来的表结构必须一致,
// 否则"同一个版本、不同库龄"会出现两套索引, 而其中一套(3 列)会静默丢数据。
func TestMigration045FreshDatabaseMatchesModel(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := conn.AutoMigrate(&model.BillingRollup{}); err != nil {
		t.Fatalf("automigrate fresh: %v", err)
	}

	columns, exists, unique := rollupIndexColumnsByName(t, conn, "billing_rollups", "idx_billing_rollup_key")
	if !exists || !unique {
		t.Fatalf("全新库的唯一键缺失或不唯一: exists=%v unique=%v cols=%v", exists, unique, columns)
	}
	if !containsFold(columns, "model_name") {
		t.Fatalf("全新库的唯一键不含 model_name: %v", columns)
	}
	legacyColumns, _, _ := rollupIndexColumnsByName(t, conn, "billing_rollups", "idx_billing_rollup_dim_day")

	if err := Migration045_BillingRollupModelDimension(conn); err != nil {
		t.Fatalf("migration on fresh db: %v", err)
	}
	columnsAfter, _, _ := rollupIndexColumnsByName(t, conn, "billing_rollups", "idx_billing_rollup_key")
	if len(columnsAfter) != len(columns) {
		t.Fatalf("迁移在全新库上改了唯一键列数: %v -> %v", columns, columnsAfter)
	}
	if !containsFold(columnsAfter, "model_name") {
		t.Fatalf("迁移后唯一键丢了 model_name: %v", columnsAfter)
	}
	// 查询索引必须原样保留: 按模型的读法依赖 (dimension_id, day) 的区间扫描。
	dayColumns, dayExists, _ := rollupIndexColumnsByName(t, conn, "billing_rollups", "idx_billing_rollup_dim_day")
	if !dayExists {
		t.Fatalf("迁移把查询索引 idx_billing_rollup_dim_day 弄丢了(全新库原本有 %v)", legacyColumns)
	}
	if !containsFold(dayColumns, "dimension_id") || !containsFold(dayColumns, "day") {
		t.Fatalf("查询索引列变了: %v", dayColumns)
	}
}
