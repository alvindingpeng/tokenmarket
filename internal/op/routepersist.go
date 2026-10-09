package op

import (
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm/clause"
)

// RouteSnapshotSave 保存一个分组的路由状态、冷却与成员指标快照; 快照是覆盖语义。
func RouteSnapshotSave(state model.RouteStateRecord, cooldowns []model.RouteCooldownRecord, metrics []model.RouteMetricRecord) error {
	database := db.GetDB()
	now := time.Now()
	state.UpdatedAt = now
	if err := database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"current_item_id", "probe_item_id", "affinity_until", "affinity_armed", "updated_at"}),
	}).Create(&state).Error; err != nil {
		return err
	}
	if err := database.Where("group_id = ?", state.GroupID).Delete(&model.RouteCooldownRecord{}).Error; err != nil {
		return err
	}
	for i := range cooldowns {
		if err := database.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "group_id"}, {Name: "item_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"cooldown_until"}),
		}).Create(&cooldowns[i]).Error; err != nil {
			return err
		}
	}
	for i := range metrics {
		metrics[i].UpdatedAt = now
		if err := database.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "group_id"}, {Name: "item_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"ema_wait_ms", "ema_success", "samples", "updated_at"}),
		}).Create(&metrics[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

// RouteSnapshotLoadAll 读取全部路由状态、冷却与指标快照, 供启动恢复。
func RouteSnapshotLoadAll() ([]model.RouteStateRecord, []model.RouteCooldownRecord, []model.RouteMetricRecord, error) {
	database := db.GetDB()
	var states []model.RouteStateRecord
	if err := database.Find(&states).Error; err != nil {
		return nil, nil, nil, err
	}
	var cooldowns []model.RouteCooldownRecord
	if err := database.Find(&cooldowns).Error; err != nil {
		return nil, nil, nil, err
	}
	var metrics []model.RouteMetricRecord
	if err := database.Find(&metrics).Error; err != nil {
		return nil, nil, nil, err
	}
	return states, cooldowns, metrics, nil
}
