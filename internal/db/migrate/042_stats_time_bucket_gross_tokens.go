package migrate

import (
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 42, Up: Migration042_StatsTimeBucketGrossTokens})
}

// Migration042_StatsTimeBucketGrossTokens 回填每日/小时/API Key统计中旧口径遗漏的缓存词元。
//
// 迁移 041 只补了全局总计 stats_totals; 首页的趋势图与每日调用热力图读取 stats_dailies /
// stats_hourlies / stats_api_keys 三个时间桶, 历史行的 input_token 仍是"净读"口径, 总词元
// 依旧低估。这里按 billing_records 逐行把 缓存读+写 加回到明细创建时刻(本地时区)对应的
// 日期桶 / 小时桶 / API Key 行上。
//
// 边界处理:
//   - stats_dailies / stats_hourlies 没有对应日期或小时行时跳过(GORM 影响 0 行),
//     不凭空造行 —— 空桶补值会让"当日请求次数为 0 但词元不为 0"的自相矛盾出现。
//   - billing_records.api_key_id 为 0 的历史行(早期数据)无法归属到 API Key, 跳过。
//   - APIKeyID 列是 int, 0 表示未记录; 后续新请求由 usageMetrics 新口径直接写入正确值。
//
// 恰好一次: 由 migration_records 保证, 重放请求由迁移框架挡住。幂等性无需自证。
func Migration042_StatsTimeBucketGrossTokens(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("billing_records") {
		return nil
	}
	rows := []struct {
		APIKeyID        int
		InputToken      int64
		CacheReadToken  int64
		CacheWriteToken int64
		CreatedAt       time.Time
	}{}
	if err := db.Table("billing_records").
		Select("api_key_id, input_token, cache_read_token, cache_write_token, created_at").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("load billing records: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	daily := map[string]int64{}
	hourly := map[string]int64{}
	apiKey := map[int]int64{}
	for _, r := range rows {
		cache := r.CacheReadToken + r.CacheWriteToken
		if cache == 0 {
			continue
		}
		local := r.CreatedAt.Local()
		date := local.Format("20060102")
		daily[date] += cache
		hourly[date+"/"+fmt.Sprint(local.Hour())] += cache
		if r.APIKeyID > 0 {
			apiKey[r.APIKeyID] += cache
		}
	}
	var total int64
	for date, cache := range daily {
		res := db.Model(&model.StatsDaily{}).Where("date = ?", date).
			UpdateColumn("input_token", gorm.Expr("input_token + ?", cache))
		if res.Error != nil {
			return fmt.Errorf("backfill daily %s: %w", date, res.Error)
		}
		total += res.RowsAffected * cache
	}
	for key, cache := range hourly {
		var date string
		var hour int
		if _, err := fmt.Sscanf(key, "%8s/%d", &date, &hour); err != nil {
			continue
		}
		res := db.Model(&model.StatsHourly{}).Where("date = ? AND hour = ?", date, hour).
			UpdateColumn("input_token", gorm.Expr("input_token + ?", cache))
		if res.Error != nil {
			return fmt.Errorf("backfill hourly %s/%d: %w", date, hour, res.Error)
		}
		total += res.RowsAffected * cache
	}
	for id, cache := range apiKey {
		res := db.Model(&model.StatsAPIKey{}).Where("api_key_id = ?", id).
			UpdateColumn("input_token", gorm.Expr("input_token + ?", cache))
		if res.Error != nil {
			return fmt.Errorf("backfill api key %d: %w", id, res.Error)
		}
		total += res.RowsAffected * cache
	}
	if total > 0 {
		log.Infof("migrate 42: backfilled %d cache tokens into daily/hourly/api-key stats", total)
	}
	return nil
}
