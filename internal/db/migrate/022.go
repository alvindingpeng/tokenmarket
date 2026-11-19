package migrate

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 22,
		Up:      migrateAPIKeyPlaintextToHash,
	})
}

// migrateAPIKeyPlaintextToHash 把 api_keys 存量明文 Key 一次性换算成 SHA-256 指纹, 并删掉明文列。
//
// 这是「旧 Key 无感迁移」: 指纹由原明文确定性换算, 原 Key 原样继续可用, 权限、额度、统计关联
// (stats_api_key.api_key_id)全部随行保留, 客户端一行配置都不用改。
//
// SQLite 上回填与删列在同一事务内完成; 删列前校验指纹、明文一致性及重复归属。
// 任一错误完整回滚, 删列后重跑无副作用。MySQL DDL 会隐式提交, 不应将 SQLite
// 的原子回滚保证外推到 MySQL; 本次生产部署目标为 SQLite。
func migrateAPIKeyPlaintextToHash(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("api_keys") {
		// 老库还没有 api_keys: 新装直接走新表结构, 没有明文可迁。
		return nil
	}
	if !hasPhysicalColumn(db, "api_keys", "api_key") {
		// 明文列已删: 要么迁过, 要么本就是新装库。
		return nil
	}
	if !hasPhysicalColumn(db, "api_keys", "key_hash") {
		// 指纹列由 AutoMigrate 建出; 少了它说明没按顺序跑, 停下来别把明文删了。
		return fmt.Errorf("api_keys.key_hash is missing: run after automigrate")
	}

	type apiKeyRow struct {
		ID      int    `gorm:"column:id"`
		APIKey  string `gorm:"column:api_key"`  // 待删除的明文列。
		KeyHash string `gorm:"column:key_hash"` // 已迁过的行指纹非空。
	}
	return db.Transaction(func(db *gorm.DB) error {
		rows := make([]apiKeyRow, 0)
		if err := db.Table("api_keys").Select("id, api_key, key_hash").Find(&rows).Error; err != nil {
			return fmt.Errorf("failed to read api_keys: %w", err)
		}

		migrated := 0
		seen := make(map[string]int)
		for _, row := range rows {
			plaintext := strings.TrimSpace(row.APIKey)
			hash, prefix, last4 := model.APIKeySecretFields(plaintext)
			if plaintext == "" {
				hash = row.KeyHash
			}
			decoded, err := hex.DecodeString(hash)
			if err != nil || len(decoded) != 32 || hash != strings.ToLower(hash) {
				return fmt.Errorf("api key %d has no valid fingerprint", row.ID)
			}
			if row.KeyHash != "" && row.KeyHash != hash {
				return fmt.Errorf("api key %d fingerprint conflicts with legacy credential", row.ID)
			}
			if previous, exists := seen[hash]; exists {
				return fmt.Errorf("api key %d duplicates credential owned by row %d", row.ID, previous)
			}
			seen[hash] = row.ID
			if plaintext == "" {
				continue
			}
			if err := db.Exec(
				"UPDATE api_keys SET key_hash = ?, key_prefix = ?, key_last4 = ? WHERE id = ?",
				hash, prefix, last4, row.ID,
			).Error; err != nil {
				return fmt.Errorf("failed to migrate api_key %d: %w", row.ID, err)
			}
			migrated++
		}

		// 全部换算完成后才删明文列: 中途失败的话列还在, 下次接着迁。
		if err := dropColumnIfExists(db, &model.APIKey{}, "api_keys", "api_key"); err != nil {
			return fmt.Errorf("failed to drop api_keys.api_key: %w", err)
		}
		if migrated > 0 {
			fmt.Printf("[migrate] hashed %d legacy api keys and dropped the plaintext column\n", migrated)
		}
		return nil
	})
}
