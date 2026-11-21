package migrate

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 45, Up: Migration045_BillingRollupModelDimension})
}

// rollupKeyIndex 是汇总表唯一键的名字, 由 model.BillingRollup 的 gorm tag 指定。
const rollupKeyIndex = "idx_billing_rollup_key"

// Migration045_BillingRollupModelDimension 给 billing_rollups 增加第 6 个维度 model。
//
// 要解决的问题: billing_rollups 原本只有 user/owner/channel/apikey/platform 五个维度, 没有 model。
// 明细按留存期(默认 180 天)清理后, 超过留存期的"按模型"用量与金额就永久消失 —— 而同一区间的
// 总金额因为有 user 维度兜底仍然有数。用户看到的是"总金额有, 拆分没有", 且没有任何字段能解释。
//
// 本迁移只做**结构**变更(补列 + 扩宽唯一键), 不搬数据、不回填:
//   - 新列 model_name 默认空串, 老的五类维度行天然就是这个值, 不需要 UPDATE;
//   - 唯一键从 (dimension, dimension_id, day) 扩成 (dimension, dimension_id, day, model_name)。
//     对老维度而言 model_name 恒为空串, 因此唯一性语义与原来完全等价, 不可能因为扩容而"多出一行";
//   - 迁移之前已经固化的日子没有 model 维度行, 这段历史**确实补不回来**(明细当时就被删了)。
//     读侧必须把它当"缺失"并如实报覆盖边界, 绝不能填 0 或按比例摊到现存模型上。
//
// 幂等: 先按实时列/索引判断是否已是新形态, 已是则直接返回(重跑零副作用)。
// 列这一半 AutoMigrate 会补(SQLite 走 ALTER TABLE ADD COLUMN), 索引这一半它不会改 ——
// GORM 只建"缺的索引", 同名索引已存在就跳过, 所以 3 列的旧唯一键会原样留着, 必须在这里显式重建。
// 不重建的后果: 同一用户同一天的两个模型行撞在同一个唯一键上, 后写的覆盖先写的(或直接报冲突),
// 按模型的历史仍然是残的。
func Migration045_BillingRollupModelDimension(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	const table = "billing_rollups"
	if !db.Migrator().HasTable(table) {
		// 表不存在 = 该库还没跑到建表这一步, 或这张表根本不该由本版本管: 安全跳过, 不制造错误。
		return nil
	}

	columns, err := rollupColumns(db)
	if err != nil {
		return err
	}
	if !containsFold(columns, "model_name") {
		// 长度 128 与模型 tag 一致; NOT NULL DEFAULT '' 让存量行不必回填。
		// 各方言都支持这一句(SQLite/MySQL/Postgres 均允许 ADD COLUMN 带常量默认值)。
		statement := fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s VARCHAR(128) NOT NULL DEFAULT ''", table, "model_name")
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("add %s.model_name: %w", table, err)
		}
		log.Infof("migrate 45: added column %s.model_name", table)
	}

	rebuilt, err := ensureRollupKeyIncludesModel(db, table)
	if err != nil {
		return err
	}
	if rebuilt {
		log.Infof("migrate 45: rebuilt unique index %s to include model_name", rollupKeyIndex)
	}

	var rows int64
	if err := db.Model(&model.BillingRollup{}).Count(&rows).Error; err != nil {
		return fmt.Errorf("count %s after migration: %w", table, err)
	}
	log.Infof("migrate 45: %s ready (model dimension), %d existing rows kept model-agnostic", table, rows)
	return nil
}

// ensureRollupKeyIncludesModel 让唯一索引 idx_billing_rollup_key 的列集合包含 model_name,
// 返回是否真的重建过(供调用方判断"这次迁移有没有做结构变更")。
//
// 判断方式用"列集合包含"而不是"列数恰为 4": 各数据库对索引列顺序与大小写表述不同,
// 只要已经含 model_name 就已满足目标(4 列的完整形态由模型 tag 保证在全新库上直接生成)。
func ensureRollupKeyIncludesModel(db *gorm.DB, table string) (bool, error) {
	columns, exists, err := rollupIndexColumns(db, table, rollupKeyIndex)
	if err != nil {
		return false, err
	}
	if exists && containsFold(columns, "model_name") {
		return false, nil
	}

	// 整个重建过程放进一个事务: 中途失败会留下"没有唯一索引"的表, 那段时间里汇总写入
	// 可能产生重复行 —— 而重复行会让终身口径(用户累计消费/可提现额度)直接虚高。
	if err := db.Transaction(func(tx *gorm.DB) error {
		if exists {
			// 按方言删索引: MySQL 的索引归属表, 用 ALTER TABLE ... DROP INDEX。
			if err := dropRollupIndex(tx, table, rollupKeyIndex); err != nil {
				return err
			}
		}
		if err := tx.Exec(fmt.Sprintf(
			"CREATE UNIQUE INDEX %s ON %q (dimension, dimension_id, day, model_name)",
			rollupKeyIndex, table)).Error; err != nil {
			return fmt.Errorf("create %s: %w", rollupKeyIndex, err)
		}
		return nil
	}); err != nil {
		return false, fmt.Errorf("rebuild %s: %w", rollupKeyIndex, err)
	}
	return true, nil
}

// dropRollupIndex 删除指定索引(SQLite/Postgres 用 DROP INDEX, MySQL 走 ALTER TABLE)。
func dropRollupIndex(db *gorm.DB, table, name string) error {
	statement := fmt.Sprintf("DROP INDEX IF EXISTS %q", name)
	if db.Dialector.Name() == "mysql" {
		statement = fmt.Sprintf("ALTER TABLE %q DROP INDEX %q", table, name)
	}
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("drop index %s on %s: %w", name, table, err)
	}
	return nil
}

// rollupColumns 返回 billing_rollups 的物理列名(GORM 的 ColumnTypes 已跨方言处理)。
func rollupColumns(db *gorm.DB) ([]string, error) {
	types, err := db.Migrator().ColumnTypes(&model.BillingRollup{})
	if err != nil {
		return nil, fmt.Errorf("read billing_rollups columns: %w", err)
	}
	names := make([]string, 0, len(types))
	for _, columnType := range types {
		names = append(names, columnType.Name())
	}
	return names, nil
}

// rollupIndexColumns 返回指定索引的列名; 索引不存在时 exists=false。
// GORM 的 GetIndexes 已跨方言实现(SQLite 读 PRAGMA, MySQL 读 SHOW INDEX, Postgres 读 pg_indexes)。
func rollupIndexColumns(db *gorm.DB, table, name string) ([]string, bool, error) {
	indexes, err := db.Migrator().GetIndexes(&model.BillingRollup{})
	if err != nil {
		return nil, false, fmt.Errorf("read %s indexes: %w", table, err)
	}
	for _, index := range indexes {
		if index.Name() != name {
			continue
		}
		return index.Columns(), true, nil
	}
	return nil, false, nil
}
