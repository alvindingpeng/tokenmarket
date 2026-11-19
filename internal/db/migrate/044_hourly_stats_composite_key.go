package migrate

import (
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 44, Up: Migration044_HourlyStatsCompositeKey})
}

// Migration044_HourlyStatsCompositeKey 把 stats_hourlies 的主键从单列 hour 改成 (date, hour)。
//
// 为什么必须重建表: 旧表主键只有 hour, 每天同一小时的桶共用一行, 第二天第一次落库就把昨天那一小时
// 覆盖掉了 —— 小时曲线永远只剩"今天", 既无法回看昨天某一小时, 也无法和日统计对账。
// GORM 的 AutoMigrate 不会改主键(SQLite 下实测只保留 PRIMARY KEY (hour)), 只能显式重建。
//
// 做法: 先按方言判断主键是否已是复合键, 已是的库直接跳过(幂等, 中途失败重跑也安全);
// 否则把旧表改名, 按新模型建表, 复制 0-23 点的有效行, 删除旧表。
// 旧表里 hour 越界的行是迁移 017 那个自增主键缺陷留下的副本, 这里一并丢弃 —— 它们与
// 午夜桶重复计数, 留着只会让任何对这张表求和的口径失真。
func Migration044_HourlyStatsCompositeKey(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("stats_hourlies") {
		return nil
	}

	const table = "stats_hourlies"
	const legacy = "stats_hourlies__legacy_key"

	// 已经有 (date, hour) 复合主键: 全新库由 AutoMigrate 直接建好, 重跑也是这条路径。
	if columns, ok := hourlyPrimaryKeyColumns(db); ok && len(columns) == 2 &&
		containsFold(columns, "date") && containsFold(columns, "hour") {
		return nil
	}

	// 上轮重建失败留下的临时表先清掉, 否则改名会撞名。
	if db.Migrator().HasTable(legacy) {
		if err := db.Migrator().DropTable(legacy); err != nil {
			return fmt.Errorf("drop stale %s: %w", legacy, err)
		}
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(fmt.Sprintf("ALTER TABLE %q RENAME TO %q", table, legacy)).Error; err != nil {
			return fmt.Errorf("rename %s: %w", table, err)
		}
		// 改名会把索引一起带过去, 索引名却不变(SQLite/Postgres 的索引名是库级唯一的);
		// 新表还叫 stats_hourlies 并要建同名索引, 先不删就会报 "index ... already exists"。
		if err := dropTableIndexes(tx, legacy); err != nil {
			return err
		}
		if err := tx.Migrator().CreateTable(&hourlyStatsTable{}); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
		// 只搬 0-23 点的行; 越界行是 017 之前自增主键缺陷的副本, 见函数注释。
		if err := tx.Exec(fmt.Sprintf(
			"INSERT INTO %q (date, hour, input_token, output_token, input_cost, output_cost, wait_time, request_success, request_failed) "+
				"SELECT date, hour, input_token, output_token, input_cost, output_cost, wait_time, request_success, request_failed "+
				"FROM %q WHERE hour >= 0 AND hour <= 23", table, legacy)).Error; err != nil {
			return fmt.Errorf("copy %s rows: %w", table, err)
		}
		if err := tx.Migrator().DropTable(legacy); err != nil {
			return fmt.Errorf("drop legacy %s: %w", legacy, err)
		}
		return nil
	}); err != nil {
		return err
	}

	var rows int64
	if err := db.Model(&model.StatsHourly{}).Count(&rows).Error; err != nil {
		return fmt.Errorf("count %s after rebuild: %w", table, err)
	}
	log.Infof("migrate 44: stats_hourlies rebuilt with (date, hour) primary key, %d rows", rows)
	return nil
}

// hourlyStatsTable 只用于建表: 与 model.StatsHourly 同结构同表名, 但显式声明复合主键,
// 避免重建表时再次落到 "PRIMARY KEY (hour)"。
type hourlyStatsTable struct {
	Date string `gorm:"primaryKey;not null;index"`
	Hour int    `gorm:"primaryKey;autoIncrement:false"`
	model.StatsMetrics
}

func (hourlyStatsTable) TableName() string { return "stats_hourlies" }

// hourlyPrimaryKeyColumns 返回表主键由哪些列组成; 无法判定时返回 ok=false,
// 由调用方按"需要重建"处理(重建本身幂等, 多跑一次没有副作用)。
func hourlyPrimaryKeyColumns(db *gorm.DB) ([]string, bool) {
	switch db.Dialector.Name() {
	case "sqlite":
		var info []struct {
			Name string
			Pk   int
		}
		if err := db.Raw(`PRAGMA table_info("stats_hourlies")`).Scan(&info).Error; err != nil {
			return nil, false
		}
		columns := make([]string, 0, 2)
		for _, column := range info {
			if column.Pk > 0 {
				columns = append(columns, column.Name)
			}
		}
		return columns, true
	case "postgres":
		var columns []string
		if err := db.Raw(`SELECT a.attname FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indrelid = 'stats_hourlies'::regclass AND i.indisprimary`).Scan(&columns).Error; err != nil {
			return nil, false
		}
		return columns, true
	case "mysql":
		var columns []string
		if err := db.Raw(`SELECT column_name FROM information_schema.key_column_usage
			WHERE table_schema = DATABASE() AND table_name = 'stats_hourlies' AND constraint_name = 'PRIMARY'`).
			Scan(&columns).Error; err != nil {
			return nil, false
		}
		return columns, true
	}
	return nil, false
}

// dropTableIndexes 删除表上所有显式索引(主键不在其列)。
// 重建表的顺序是"旧表改名 -> 建新表", 旧表的索引会跟着改名但仍然占着原来的索引名,
// 于是建新表时同名索引冲突。必须先按名字删干净。
func dropTableIndexes(db *gorm.DB, table string) error {
	var names []string
	switch db.Dialector.Name() {
	case "sqlite":
		var info []struct {
			Name string
		}
		if err := db.Raw(fmt.Sprintf("PRAGMA index_list(%q)", table)).Scan(&info).Error; err != nil {
			return fmt.Errorf("list indexes of %s: %w", table, err)
		}
		for _, index := range info {
			names = append(names, index.Name)
		}
	case "postgres":
		if err := db.Raw("SELECT indexname FROM pg_indexes WHERE tablename = ?", table).Scan(&names).Error; err != nil {
			return fmt.Errorf("list indexes of %s: %w", table, err)
		}
	case "mysql":
		// MySQL 的索引只在表内唯一, 但仍然要先删, 否则新表建同名索引会重复。
		var info []struct {
			KeyName string `gorm:"column:Key_name"`
		}
		if err := db.Raw(fmt.Sprintf("SHOW INDEX FROM %q", table)).Scan(&info).Error; err != nil {
			return fmt.Errorf("list indexes of %s: %w", table, err)
		}
		for _, index := range info {
			if !strings.EqualFold(index.KeyName, "PRIMARY") {
				names = append(names, index.KeyName)
			}
		}
	default:
		return nil
	}
	for _, name := range names {
		statement := fmt.Sprintf("DROP INDEX IF EXISTS %q", name)
		if db.Dialector.Name() == "mysql" {
			statement = fmt.Sprintf("ALTER TABLE %q DROP INDEX %q", table, name)
		}
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("drop index %s on %s: %w", name, table, err)
		}
	}
	return nil
}

// containsFold 判断列名集合里是否有目标列(方言返回大小写不同, 不能直接比)。
func containsFold(columns []string, target string) bool {
	for _, column := range columns {
		if strings.EqualFold(column, target) {
			return true
		}
	}
	return false
}
