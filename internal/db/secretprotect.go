package db

import (
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// map 写入兜底: gorm 对 map[string]any 的 Updates/Create 不走字段序列化器,
// 直接把 map 值塞进 SQL 参数。没有这层拦截, SettingUpsert 之类的 map 写入会把明文凭据落库。
// 拦截后按行归属加密封存; 归属推不出来时直接报错, 不允许写入弱绑定或明文的密文列。
func protectSecretMapWrites(tx *gorm.DB) {
	if tx == nil || tx.Error != nil || tx.Statement == nil || tx.Statement.Dest == nil {
		return
	}
	if secretstore.Active() == nil {
		return
	}
	for _, row := range secretMapRowsOf(tx.Statement.Dest) {
		if err := protectSecretMapRow(tx, row); err != nil {
			_ = tx.AddError(err)
			return
		}
	}
}

// secretMapRowsOf 取出 Dest 里的 map 行(gorm 支持 map / *map / []map / *[]map 四种写入形态)。
func secretMapRowsOf(dest interface{}) []map[string]interface{} {
	switch value := dest.(type) {
	case map[string]interface{}:
		return []map[string]interface{}{value}
	case *map[string]interface{}:
		if value == nil {
			return nil
		}
		return []map[string]interface{}{*value}
	case []map[string]interface{}:
		return value
	case *[]map[string]interface{}:
		if value == nil {
			return nil
		}
		return *value
	default:
		return nil
	}
}

// protectSecretMapRow 处理一行 map 写入: 只关心密文列出现在 SET 里的情况。
func protectSecretMapRow(tx *gorm.DB, row map[string]interface{}) error {
	var (
		table        string
		parsedSchema *schema.Schema
	)
	if tx.Statement.Schema != nil {
		parsedSchema = tx.Statement.Schema
		table = parsedSchema.Table
	} else {
		table = tx.Statement.Table
	}
	switch table {
	case secretSettingTable:
		return protectSettingMapRow(parsedSchema, tx.Statement.Model, row)
	case secretChannelKeyTable:
		return protectChannelKeyMapRow(parsedSchema, tx.Statement.Model, row)
	default:
		return nil
	}
}

// protectSettingMapRow settings.value 的 map 写入。
// 归属绑定需要设置键: map 里带 key 列则以它为准, 否则取 Model 上的主键; 都取不到就拒绝写入。
func protectSettingMapRow(parsedSchema *schema.Schema, modelValue interface{}, row map[string]interface{}) error {
	valueKey := secretMapColumn(parsedSchema, row, secretSettingValueColumn)
	if valueKey == "" || row[valueKey] == nil {
		return nil
	}
	key := ""
	if rawKey := secretMapColumn(parsedSchema, row, "key"); rawKey != "" {
		if value, err := secretFieldString(row[rawKey]); err == nil {
			key = value
		}
	}
	if key == "" {
		switch setting := modelValue.(type) {
		case *model.Setting:
			key = string(setting.Key)
		case model.Setting:
			key = string(setting.Key)
		}
	}
	if key == "" {
		return fmt.Errorf("refusing to write settings.value without the setting key: the secret envelope binds to row ownership")
	}
	if !model.SettingNeedsAtRestEncryption(model.SettingKey(key)) {
		return nil
	}
	value, err := secretFieldString(row[valueKey])
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	sealed, err := sealSecretValue(secretstore.SettingBinding(key), value)
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	row[valueKey] = sealed
	return nil
}

// protectChannelKeyMapRow channel_keys.key 的 map 写入。
// 归属绑定需要 (channel_id, name): map 里改了 name/channel_id 却不重给 key 时,
// 存量密文的绑定会失效, 这种写入直接拒绝。
func protectChannelKeyMapRow(parsedSchema *schema.Schema, modelValue interface{}, row map[string]interface{}) error {
	keyColumn := secretMapColumn(parsedSchema, row, secretChannelKeyColumn)
	nameColumn := secretMapColumn(parsedSchema, row, "name")
	channelIDColumn := secretMapColumn(parsedSchema, row, "channel_id")

	var (
		channelID int
		name      string
	)
	if channelIDColumn != "" {
		if value, ok := secretMapInt(row[channelIDColumn]); ok {
			channelID = value
		}
	}
	if nameColumn != "" {
		if value, err := secretFieldString(row[nameColumn]); err == nil {
			name = value
		}
	}
	switch key := modelValue.(type) {
	case *model.ChannelKey:
		if channelID == 0 {
			channelID = key.ChannelID
		}
		if nameColumn == "" {
			name = key.Name
		}
	case model.ChannelKey:
		if channelID == 0 {
			channelID = key.ChannelID
		}
		if nameColumn == "" {
			name = key.Name
		}
	}

	if keyColumn == "" || row[keyColumn] == nil {
		if (nameColumn != "" || channelIDColumn != "") && secretstore.Active() != nil {
			return fmt.Errorf("refusing to move/rename channel key without re-supplying the key: stored ciphertext is bound to (channel_id, name)")
		}
		return nil
	}
	if channelID == 0 {
		return fmt.Errorf("refusing to write channel_keys.key without channel_id: the secret envelope binds to row ownership")
	}
	value, err := secretFieldString(row[keyColumn])
	if err != nil {
		return fmt.Errorf("channel key %d/%q: %w", channelID, name, err)
	}
	sealed, err := sealSecretValue(secretstore.ChannelKeyBinding(channelID, name), value)
	if err != nil {
		return fmt.Errorf("channel key %d/%q: %w", channelID, name, err)
	}
	row[keyColumn] = sealed
	return nil
}

// secretMapInt 把 map 里的整型取值收敛成 int(gorm map 写入里 channel_id 可能是 int/int64/字符串)。
func secretMapInt(fieldValue interface{}) (int, bool) {
	switch value := fieldValue.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case uint:
		return int(value), true
	case uint64:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		var parsed int
		if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// secretMapColumn 返回 map 里对应目标列的键名。schema 存在时按字段名/列名解析(兼容 "Value"/"value"),
// 裸表(db.Table)写入按列名匹配。
func secretMapColumn(parsedSchema *schema.Schema, row map[string]interface{}, columnName string) string {
	for key := range row {
		if parsedSchema != nil {
			if field := parsedSchema.LookUpField(key); field != nil && field.DBName == columnName {
				return key
			}
		} else if strings.EqualFold(key, columnName) {
			return key
		}
	}
	return ""
}
