package migrate

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 15,
		Up:      resetStampedScoreWeights,
	})
}

// resetStampedScoreWeights 把存量分组里与旧内置默认一致的 综合评分 权重(40/30/30)改写为 0/0/0,
// 使其回归"未自定义, 运行时跟随系统默认设置"的语义。管理员的系统默认配比存于设置表,
// 分组内只要有一项与旧默认不同即视为用户自行修改, 保持原值不动。逐行按 JSON 改写, 重跑安全。
func resetStampedScoreWeights(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("groups") {
		return nil
	}

	type stamped struct {
		ID          int    `gorm:"column:id"`
		RelayConfig string `gorm:"column:relay_config"`
	}
	var rows []stamped
	if err := db.Raw("SELECT id, relay_config FROM groups WHERE mode = 'score'").Scan(&rows).Error; err != nil {
		return fmt.Errorf("failed to load score groups: %w", err)
	}

	type scoreWeights struct {
		ScorePriceWeight   int `json:"score_price_weight"`
		ScoreLatencyWeight int `json:"score_latency_weight"`
		ScoreSuccessWeight int `json:"score_success_weight"`
	}
	for _, row := range rows {
		if len(row.RelayConfig) == 0 {
			continue
		}
		var weights scoreWeights
		if err := json.Unmarshal([]byte(row.RelayConfig), &weights); err != nil {
			// JSON 解析失败交由模型层正常读取路径处理, 这里跳过不阻塞升级。
			continue
		}
		if weights.ScorePriceWeight != 40 || weights.ScoreLatencyWeight != 30 || weights.ScoreSuccessWeight != 30 {
			continue
		}
		var config map[string]any
		if err := json.Unmarshal([]byte(row.RelayConfig), &config); err != nil {
			continue
		}
		config["score_price_weight"] = 0
		config["score_latency_weight"] = 0
		config["score_success_weight"] = 0
		updated, err := json.Marshal(config)
		if err != nil {
			continue
		}
		if err := db.Exec("UPDATE groups SET relay_config = ? WHERE id = ?", string(updated), row.ID).Error; err != nil {
			return fmt.Errorf("failed to reset score weights for group %d: %w", row.ID, err)
		}
	}
	return nil
}
