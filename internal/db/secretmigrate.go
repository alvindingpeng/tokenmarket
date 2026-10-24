package db

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
)

// 既有明文的原子幂等迁移: 把 settings / channel_keys 里遗留的明文凭据包进信封。
//
//   - 幂等: 已是信封的行只验不改(顺带校验当前主密钥能开这些信封), 重复执行零写入;
//   - 全量验证: settings.value 与 channel_keys.key 里出现的每一个信封都会被验开 —— 包括落在
//     非密钥设置行里的信封。错误的主密钥在启动的这一步就暴露, 不必等有流量才发现;
//   - 原子: 全部 UPDATE 收在一个事务里, 中途失败不留半加密状态;
//   - fail closed: 任何信封打不开(主密钥配错/被换行搬运)都在写入前报错, 启动拒绝继续。
//
// 走原始 SQL 读写而不经过 gorm 模型: 序列化器会把读到的值解开成明文、写入时再加密,
// 拿它做迁移会把已加密行反复重新加密, 也会让库里到底是什么变得不可见。
type SecretMigrationStats struct {
	SettingsEncrypted    int `json:"settings_encrypted"`     // 本次加密的密钥类设置行。
	ChannelKeysEncrypted int `json:"channel_keys_encrypted"` // 本次加密的渠道凭据行。
	AlreadyEncrypted     int `json:"already_encrypted"`      // 已是信封并验开通过的行。
	EmptySkipped         int `json:"empty_skipped"`          // 空值行: 无秘密可言, 保持空串。
}

// Total 返回本次实际加密的行数。
func (s SecretMigrationStats) Total() int {
	return s.SettingsEncrypted + s.ChannelKeysEncrypted
}

// MigrateSecretRows 迁移(或 dryRun 演练)存量明文凭据。主密钥未初始化直接报错。
func MigrateSecretRows(ctx context.Context, dryRun bool) (SecretMigrationStats, error) {
	store, err := secretstore.Require()
	if err != nil {
		return SecretMigrationStats{}, err
	}
	conn := db.WithContext(ctx)
	var stats SecretMigrationStats

	var settingRows []struct {
		Key   string
		Value string
	}
	if err := conn.Raw("SELECT key, value FROM settings").Scan(&settingRows).Error; err != nil {
		return stats, fmt.Errorf("read settings for secret migration: %w", err)
	}
	type settingUpdate struct{ key, value string }
	settingUpdates := make([]settingUpdate, 0)
	for _, row := range settingRows {
		if !model.SettingNeedsAtRestEncryption(model.SettingKey(row.Key)) {
			// 非密钥设置项不加密, 但值里若出现信封(被搬移的密文/历史脏数据/换钥残留)必须验:
			// 启动要把"库里每个信封都能被当前主密钥打开"验证完整, 错误 KEK 不等流量来暴露。
			if secretstore.IsEnvelope(row.Value) {
				if _, err := store.Open(secretstore.SettingBinding(row.Key), row.Value); err != nil {
					return stats, fmt.Errorf("setting %q: %w", row.Key, err)
				}
				stats.AlreadyEncrypted++
			}
			continue
		}
		sealed, encrypted, err := migrateSecretValue(store, secretstore.SettingBinding(row.Key), row.Value, &stats)
		if err != nil {
			return stats, fmt.Errorf("setting %q: %w", row.Key, err)
		}
		if encrypted {
			settingUpdates = append(settingUpdates, settingUpdate{key: row.Key, value: sealed})
		}
	}

	var keyRows []struct {
		ID        int
		ChannelID int
		Name      string
		Key       string
	}
	if err := conn.Raw("SELECT id, channel_id, name, key FROM channel_keys").Scan(&keyRows).Error; err != nil {
		return stats, fmt.Errorf("read channel keys for secret migration: %w", err)
	}
	type keyUpdate struct {
		id    int
		value string
	}
	keyUpdates := make([]keyUpdate, 0)
	for _, row := range keyRows {
		if row.ChannelID == 0 {
			// 归属缺失的行没法绑 AAD: 与其写进弱绑定密文, 不如把问题摆出来。
			return stats, fmt.Errorf("channel key row %d has no channel_id ownership, refusing to migrate", row.ID)
		}
		sealed, encrypted, err := migrateSecretValue(store, secretstore.ChannelKeyBinding(row.ChannelID, row.Name), row.Key, &stats)
		if err != nil {
			return stats, fmt.Errorf("channel key %d/%q: %w", row.ChannelID, row.Name, err)
		}
		if encrypted {
			keyUpdates = append(keyUpdates, keyUpdate{id: row.ID, value: sealed})
		}
	}

	if dryRun || (len(settingUpdates) == 0 && len(keyUpdates) == 0) {
		return stats, nil
	}
	err = conn.Transaction(func(tx *gorm.DB) error {
		for _, update := range settingUpdates {
			if err := tx.Exec("UPDATE settings SET value = ? WHERE key = ?", update.value, update.key).Error; err != nil {
				return err
			}
		}
		for _, update := range keyUpdates {
			if err := tx.Exec("UPDATE channel_keys SET key = ? WHERE id = ?", update.value, update.id).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("apply secret migration: %w", err)
	}
	return stats, nil
}

// migrateSecretValue 判定单个取值的迁移动作: 空值跳过, 信封验开计数, 明文加密计数。
func migrateSecretValue(store *secretstore.MasterKey, binding secretstore.Binding, value string, stats *SecretMigrationStats) (sealed string, encrypted bool, err error) {
	if value == "" {
		stats.EmptySkipped++
		return "", false, nil
	}
	if secretstore.IsEnvelope(value) {
		if _, err := store.Open(binding, value); err != nil {
			// 主密钥配错、keyID 对不上、或密文被搬到别的行, 都在这里被拦下。
			return "", false, err
		}
		stats.AlreadyEncrypted++
		return "", false, nil
	}
	sealed, err = store.Seal(binding, value)
	if err != nil {
		return "", false, err
	}
	switch binding.Table {
	case secretSettingTable:
		stats.SettingsEncrypted++
	default:
		stats.ChannelKeysEncrypted++
	}
	return sealed, true, nil
}
