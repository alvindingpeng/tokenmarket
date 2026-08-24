package migrate

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 8,
		Up:      migrateChannelModels,
	})
}

// channelModelKey 用渠道和名称唯一定位一条渠道模型记录。
type channelModelKey struct {
	ChannelID int    // 所属渠道主键。
	Name      string // 渠道模型名称。
}

// migrateChannelModels 将旧渠道模型和分组项转换为渠道模型外键结构，并清理旧表。
func migrateChannelModels(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := migrateLegacyChannelStats(tx); err != nil {
			return err
		}
		if !tx.Migrator().HasTable("channel_models") {
			if err := tx.AutoMigrate(&model.ChannelModel{}); err != nil {
				return fmt.Errorf("failed to create channel_models: %w", err)
			}
		}

		type legacyChannel struct {
			ID          int            // 渠道主键。
			AutoSync    bool           // 是否自动同步模型。
			Models      sql.NullString `gorm:"column:model"`        // 旧渠道模型列表。
			CustomModel sql.NullString `gorm:"column:custom_model"` // 旧手动模型列表。
		}
		channels := make([]legacyChannel, 0)
		channelFields := []string{"id"}
		if tx.Migrator().HasColumn(&model.Channel{}, "auto_sync") {
			channelFields = append(channelFields, "auto_sync")
		}
		if hasPhysicalColumn(tx, "channels", "model") {
			channelFields = append(channelFields, "model")
		}
		if hasPhysicalColumn(tx, "channels", "custom_model") {
			channelFields = append(channelFields, "custom_model")
		}
		if err := tx.Table("channels").Select(channelFields).Order("id ASC").Find(&channels).Error; err != nil {
			return fmt.Errorf("failed to read legacy channels: %w", err)
		}

		channelAutoSync := make(map[int]bool, len(channels))
		for _, channel := range channels {
			channelAutoSync[channel.ID] = channel.AutoSync
		}

		channelModels := make([]model.ChannelModel, 0)
		if err := tx.Order("id ASC").Find(&channelModels).Error; err != nil {
			return fmt.Errorf("failed to read channel_models: %w", err)
		}
		modelsByKey := make(map[channelModelKey]*model.ChannelModel, len(channelModels))
		existingModelKeys := make(map[channelModelKey]struct{}, len(channelModels))
		modelOrder := make([]channelModelKey, 0)
		for i := range channelModels {
			key := channelModelKey{ChannelID: channelModels[i].ChannelID, Name: channelModels[i].Name}
			modelsByKey[key] = &channelModels[i]
			existingModelKeys[key] = struct{}{}
			modelOrder = append(modelOrder, key)
		}

		// 同名模型只保留一行，手动来源优先于自动来源。
		addModel := func(channelID int, name string, source model.ChannelModelSource) {
			name = strings.TrimSpace(name)
			if channelID == 0 || name == "" {
				return
			}
			key := channelModelKey{ChannelID: channelID, Name: name}
			current, ok := modelsByKey[key]
			if !ok {
				current = &model.ChannelModel{ChannelID: channelID, Name: name, Source: source}
				modelsByKey[key] = current
				modelOrder = append(modelOrder, key)
			} else if source == model.ChannelModelSourceManual {
				current.Source = model.ChannelModelSourceManual
			}
		}

		for _, channel := range channels {
			if channel.Models.Valid {
				for _, name := range strings.Split(channel.Models.String, ",") {
					addModel(channel.ID, name, model.ChannelModelSourceAuto)
				}
			}
			if channel.CustomModel.Valid {
				for _, name := range strings.Split(channel.CustomModel.String, ",") {
					addModel(channel.ID, name, model.ChannelModelSourceManual)
				}
			}
		}

		if tx.Migrator().HasTable("group_items") &&
			hasPhysicalColumn(tx, "group_items", "channel_id") &&
			hasPhysicalColumn(tx, "group_items", "model_name") {
			type legacyGroupItemName struct {
				ChannelID int    // 旧渠道主键。
				ModelName string // 旧模型名称。
			}
			legacyNames := make([]legacyGroupItemName, 0)
			if err := tx.Table("group_items").Select("channel_id, model_name").Order("id ASC").Find(&legacyNames).Error; err != nil {
				return fmt.Errorf("failed to read legacy group item models: %w", err)
			}
			for _, item := range legacyNames {
				key := channelModelKey{ChannelID: item.ChannelID, Name: strings.TrimSpace(item.ModelName)}
				if _, exists := modelsByKey[key]; exists {
					continue
				}
				source := model.ChannelModelSourceManual
				if channelAutoSync[item.ChannelID] {
					source = model.ChannelModelSourceAuto
				}
				addModel(item.ChannelID, item.ModelName, source)
			}
		}

		for _, key := range modelOrder {
			channelModel := modelsByKey[key]
			if _, exists := existingModelKeys[key]; !exists {
				continue
			}
			if err := tx.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).Updates(channelModel).Error; err != nil {
				return fmt.Errorf("failed to update channel_model %d: %w", channelModel.ID, err)
			}
		}
		for _, key := range modelOrder {
			channelModel := modelsByKey[key]
			if _, exists := existingModelKeys[key]; exists {
				continue
			}
			if err := tx.Create(channelModel).Error; err != nil {
				return fmt.Errorf("failed to create channel_model %s: %w", channelModel.Name, err)
			}
		}

		if err := migrateLegacyGroupItems(tx, modelsByKey); err != nil {
			return err
		}
		for _, column := range []string{"model", "custom_model", "auto_group"} {
			if err := dropColumnIfExists(tx, &model.Channel{}, "channels", column); err != nil {
				return err
			}
		}
		// 渠道统计已并入 channels，未实际使用的模型统计直接丢弃。
		for _, table := range []string{"stats_models", "stats_channels"} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Migrator().DropTable(table); err != nil {
					return fmt.Errorf("failed to drop %s: %w", table, err)
				}
			}
		}
		return nil
	})
}

// migrateLegacyChannelStats 将旧渠道统计原值迁移到 channels 的内嵌统计字段。
func migrateLegacyChannelStats(db *gorm.DB) error {
	for _, field := range []string{"InputToken", "OutputToken", "InputCost", "OutputCost", "WaitTime", "RequestSuccess", "RequestFailed"} {
		if db.Migrator().HasColumn(&model.Channel{}, field) {
			continue
		}
		if err := db.Migrator().AddColumn(&model.Channel{}, field); err != nil {
			return fmt.Errorf("failed to add channels stats column %s: %w", field, err)
		}
	}
	if !db.Migrator().HasTable("stats_channels") {
		return nil
	}

	type legacyChannelStats struct {
		ChannelID int `gorm:"column:channel_id"` // 所属渠道主键。
		model.StatsMetrics
	}
	stats := make([]legacyChannelStats, 0)
	if err := db.Table("stats_channels").Order("channel_id ASC").Find(&stats).Error; err != nil {
		return fmt.Errorf("failed to read stats_channels: %w", err)
	}
	for _, channelStats := range stats {
		if err := db.Model(&model.Channel{}).Where("id = ?", channelStats.ChannelID).Updates(map[string]interface{}{
			"input_token":     channelStats.InputToken,
			"output_token":    channelStats.OutputToken,
			"input_cost":      channelStats.InputCost,
			"output_cost":     channelStats.OutputCost,
			"wait_time":       channelStats.WaitTime,
			"request_success": channelStats.RequestSuccess,
			"request_failed":  channelStats.RequestFailed,
		}).Error; err != nil {
			return fmt.Errorf("failed to migrate stats for channel_id=%d: %w", channelStats.ChannelID, err)
		}
	}
	return nil
}

// migrateLegacyGroupItems 将旧分组项重建为只保存渠道模型外键的表。
func migrateLegacyGroupItems(db *gorm.DB, modelsByKey map[channelModelKey]*model.ChannelModel) error {
	if !db.Migrator().HasTable("group_items") ||
		!hasPhysicalColumn(db, "group_items", "channel_id") ||
		!hasPhysicalColumn(db, "group_items", "model_name") {
		return nil
	}

	type legacyGroupItem struct {
		ID        int    // 分组项主键。
		GroupID   int    // 所属分组主键。
		ChannelID int    // 旧渠道主键。
		ModelName string // 旧模型名称。
		Priority  int    // 展示和故障转移顺序。
	}
	legacyItems := make([]legacyGroupItem, 0)
	if err := db.Table("group_items").Order("id ASC").Find(&legacyItems).Error; err != nil {
		return fmt.Errorf("failed to read group_items: %w", err)
	}

	items := make([]model.GroupItem, 0, len(legacyItems))
	invalidIDs := make([]int, 0)
	seen := make(map[[2]int]struct{}, len(legacyItems))
	for _, item := range legacyItems {
		key := channelModelKey{ChannelID: item.ChannelID, Name: strings.TrimSpace(item.ModelName)}
		channelModel, ok := modelsByKey[key]
		if !ok {
			invalidIDs = append(invalidIDs, item.ID)
			continue
		}
		itemKey := [2]int{item.GroupID, channelModel.ID}
		if _, exists := seen[itemKey]; exists {
			invalidIDs = append(invalidIDs, item.ID)
			continue
		}
		seen[itemKey] = struct{}{}
		items = append(items, model.GroupItem{
			ID:             item.ID,
			GroupID:        item.GroupID,
			ChannelModelID: channelModel.ID,
			Priority:       item.Priority,
		})
	}

	if err := db.Migrator().DropTable(&model.GroupItem{}); err != nil {
		return fmt.Errorf("failed to drop legacy group_items: %w", err)
	}
	if err := db.AutoMigrate(&model.GroupItem{}); err != nil {
		return fmt.Errorf("failed to create group_items: %w", err)
	}
	if len(items) > 0 {
		if err := db.Create(&items).Error; err != nil {
			return fmt.Errorf("failed to create migrated group_items: %w", err)
		}
	}
	if len(invalidIDs) > 0 && db.Migrator().HasTable("groups") {
		if err := db.Model(&model.Group{}).Where("active_item_id IN ?", invalidIDs).Update("active_item_id", 0).Error; err != nil {
			return fmt.Errorf("failed to clear invalid active items: %w", err)
		}
	}
	if db.Migrator().HasTable("groups") {
		if err := db.Exec("UPDATE groups SET active_item_id = 0 WHERE active_item_id <> 0 AND NOT EXISTS (SELECT 1 FROM group_items WHERE group_items.id = groups.active_item_id AND group_items.group_id = groups.id)").Error; err != nil {
			return fmt.Errorf("failed to clear stale active items: %w", err)
		}
	}
	return nil
}
