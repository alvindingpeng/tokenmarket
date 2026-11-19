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

// EgressMetricsSave 保存全部出口指标与冷却, 覆盖语义(每个出口一行)。
func EgressMetricsSave(records []model.EgressMetricRecord) error {
	if len(records) == 0 {
		return nil
	}
	database := db.GetDB()
	now := time.Now()
	for i := range records {
		records[i].UpdatedAt = now
		if err := database.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "egress_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"ema_wait_ms", "ema_success", "samples", "cooldown_until", "updated_at"}),
		}).Create(&records[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

// EgressMetricsLoadAll 读取全部出口指标快照, 供启动恢复。
func EgressMetricsLoadAll() ([]model.EgressMetricRecord, error) {
	var records []model.EgressMetricRecord
	err := db.GetDB().Find(&records).Error
	return records, err
}

// EgressIDs 返回全部出口主键, 供清理已删除出口的残留成绩行。
func EgressIDs() ([]int, error) {
	ids := []int{}
	err := db.GetDB().Model(&model.ChannelEgress{}).Pluck("id", &ids).Error
	return ids, err
}

// EgressMetricsDeleteMissing 清掉已删除出口留下的残留行。
// 出口被删后它的成绩再无意义, 留着还会让"有数据/无数据"的判断在界面上出现幽灵出口。
func EgressMetricsDeleteMissing(existing []int) error {
	if len(existing) == 0 {
		// 一个出口都没有(全部删掉/全新库): 残留行一律清掉。
		// 不能靠 "NOT IN 空列表", gorm 会因为没有 WHERE 条件拒绝整表删除。
		return db.GetDB().Where("1 = 1").Delete(&model.EgressMetricRecord{}).Error
	}
	return db.GetDB().Where("egress_id NOT IN ?", existing).Delete(&model.EgressMetricRecord{}).Error
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
