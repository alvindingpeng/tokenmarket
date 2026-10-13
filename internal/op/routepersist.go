package op

import (
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
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

// RouteMetricItemChannelModels 返回分组成员 ID 到渠道模型 ID 的映射(group_items ⋈ channel_grants)。
// 评分指标按渠道模型全局聚合, 而 route_metrics 只按 (分组, 成员) 落库, 恢复与重置都需要这层映射
// 才能把成员观测归并到正确的渠道模型。
func RouteMetricItemChannelModels() (map[int]int, error) {
	type row struct {
		ItemID         int
		ChannelModelID int
	}
	var rows []row
	if err := db.GetDB().Table("group_items").
		Select("group_items.id AS item_id, channel_grants.channel_model_id AS channel_model_id").
		Joins("JOIN channel_grants ON channel_grants.id = group_items.channel_grant_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]int, len(rows))
	for _, r := range rows {
		out[r.ItemID] = r.ChannelModelID
	}
	return out, nil
}

// RouteMetricDelete 删除单个成员的指标快照(运维重置用)。
func RouteMetricDelete(groupID, itemID int) error {
	return db.GetDB().Where("group_id = ? AND item_id = ?", groupID, itemID).Delete(&model.RouteMetricRecord{}).Error
}

// RouteMetricDeleteByChannelModel 删除某渠道模型下全部成员的指标快照(运维重置用), 返回删除行数。
func RouteMetricDeleteByChannelModel(channelModelID int) (int64, error) {
	res := db.GetDB().Where("item_id IN (?)",
		db.GetDB().Table("group_items").Select("group_items.id").
			Joins("JOIN channel_grants ON channel_grants.id = group_items.channel_grant_id").
			Where("channel_grants.channel_model_id = ?", channelModelID),
	).Delete(&model.RouteMetricRecord{})
	return res.RowsAffected, res.Error
}
