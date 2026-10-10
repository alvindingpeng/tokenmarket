package migrate

import (
	"errors"
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 17,
		Up:      repairHourlyStatsRows,
	})
}

// repairHourlyStatsRows 收拾小时统计表里由数据库自增主键产生的脏行。
//
// 模型把 Hour 声明成整数主键时, GORM 默认按自增列处理, 于是 Hour=0(午夜桶)被当成"主键未设置"
// 而从 INSERT 的列清单里省略, 数据库自 assigns 一个新 rowid。结果每次落库都往表里插一条
// hour>23 的脏行, 而 hour=0 这一格永远没有数据。脏行的值就是当时午夜桶的累计值,
// 同一天里反复落库会留下一串递增后冻结的副本。
//
// 这张表按设计每个小时只留一行(hour 作主键, date 标记最后一次写入日), 历史按天走势在
// stats_dailies 里, 所以只需把最新一天的午夜累计值搬回 hour=0, 其余脏行全部删除——
// 它们与日统计、总计统计重复计数, 留着只会让任何对这张表求和的口径失真。
func repairHourlyStatsRows(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.StatsHourly{}) {
		return nil
	}

	out_of_range := "hour < 0 OR hour > 23"
	var garbage int64
	if err := db.Model(&model.StatsHourly{}).Where(out_of_range).Count(&garbage).Error; err != nil {
		return fmt.Errorf("failed to count out-of-range hourly rows: %w", err)
	}
	if garbage == 0 {
		return nil
	}

	// 自增 rowid 单调递增, 同一天的副本越新值越接近该天午时的最终累计;
	// 日期字典序即时间序, 取"最近一天的最后一条"作为要搬回 hour=0 的那条。
	type hourlyKey struct {
		Hour int
		Date string
	}
	var newest hourlyKey
	err := db.Model(&model.StatsHourly{}).Select("hour, date").
		Where(out_of_range).
		Order("date DESC, hour DESC").
		First(&newest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to locate newest out-of-range hourly row: %w", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&model.StatsHourly{}).Where("hour = 0").Count(&existing).Error; err != nil {
			return fmt.Errorf("failed to probe hourly slot 0: %w", err)
		}
		if existing == 0 {
			if err := tx.Model(&model.StatsHourly{}).
				Where("hour = ?", newest.Hour).
				Update("hour", 0).Error; err != nil {
				return fmt.Errorf("failed to move hourly row %d to slot 0: %w", newest.Hour, err)
			}
		}
		if err := tx.Where(out_of_range).Delete(&model.StatsHourly{}).Error; err != nil {
			return fmt.Errorf("failed to delete out-of-range hourly rows: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	log.Infof("migrate 17: cleaned %d out-of-range hourly stats rows", garbage)
	return nil
}
