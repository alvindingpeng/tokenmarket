package migrate

import (
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 35, Up: Migration035_ChannelObservation})
}

// Migration035_ChannelObservation 为已过审但从未进入观察期的存量渠道补开观察窗口。
// 按 reviewed_at 起算，保留既有流量的质量评估口径，不擅自判定 trusted。
func Migration035_ChannelObservation(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") || !db.Migrator().HasColumn("channels", "trust_level") || !db.Migrator().HasColumn("channels", "observation_start") || !db.Migrator().HasColumn("channels", "observation_until") {
		return nil
	}
	type channelRow struct {
		ID       int
		Reviewed *time.Time
	}
	var rows []channelRow
	if err := db.Table("channels").Select("id, reviewed_at").Where("review_status = ? AND trust_level = ? AND observation_start IS NULL", model.ChannelReviewApproved, model.ChannelTrustObserving).Find(&rows).Error; err != nil {
		return fmt.Errorf("load channels without observation: %w", err)
	}
	for _, row := range rows {
		start := time.Now()
		if row.Reviewed != nil && !row.Reviewed.IsZero() {
			start = *row.Reviewed
		}
		until := start.AddDate(0, 0, 7)
		if err := db.Table("channels").Where("id = ?", row.ID).Updates(map[string]any{"observation_start": start, "observation_until": until}).Error; err != nil {
			return fmt.Errorf("start observation for channel %d: %w", row.ID, err)
		}
	}
	return nil
}
