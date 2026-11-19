package migrate

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 41, Up: Migration041_StatsInputGrossTokens})
}

// Migration041_StatsInputGrossTokens 统计词元口径修复: stats_* 表的 input_token 从
// "净输入读桶"改为"毛输入"(prompt, 含缓存读/写)。
//
// 背景: 旧口径下 usageMetrics 只把 prompt 扣掉缓存读/写后的净读量计入 input_token,
// 缓存命中请求的缓存词元在统计表里凭空消失, 首页"总词元 = 输入 + 输出"被低估。
// 新口径与计费明细(user_stats 的 total_tokens = input + output + cache_read + cache_write)
// 及上游账单 total_tokens 对齐; 修复后的写入见 relay/state.go usageMetrics 与
// handlers.userBillableStats。
//
// 回填: stats_totals 是全局唯一汇总行, 直接把 billing_records 的缓存词元总和补进去。
// 其余统计表不做回填——daily 只有日期刻度、hourly 的 Date 是"最后写入日"而非数据日、
// api_key 明细在历史行上可能缺列, 都没有安全拆分依据; 这些刻度的缓存词元从修复上线后
// 的新请求开始计入, 只少补不多补。
//
// 恰好一次: 本迁移由 migration_records 保证每库只执行一次, UPDATE 不需要自判幂等。
// 无 billing_records、无 stats_totals 的空库零操作。
func Migration041_StatsInputGrossTokens(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.StatsTotal{}) || !db.Migrator().HasTable("billing_records") {
		return nil
	}
	var records int64
	if err := db.Table("billing_records").Count(&records).Error; err != nil {
		return fmt.Errorf("count billing_records: %w", err)
	}
	if records == 0 {
		return nil
	}
	var cached int64
	if err := db.Table("billing_records").
		Select("COALESCE(SUM(cache_read_token + cache_write_token), 0) AS cached_tokens").
		Scan(&cached).Error; err != nil {
		return fmt.Errorf("sum cache tokens: %w", err)
	}
	if cached == 0 {
		return nil
	}
	result := db.Model(&model.StatsTotal{}).Where("1 = 1").
		UpdateColumn("input_token", gorm.Expr("input_token + ?", cached))
	if result.Error != nil {
		return fmt.Errorf("backfill stats_totals: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Infof("migrate 41: backfilled %d cache tokens into stats_totals", cached)
	}
	return nil
}
