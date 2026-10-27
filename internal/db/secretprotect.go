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
func protectSecretMapWrites(tx *gorm.DB, isCreate bool) {
	if tx == nil || tx.Error != nil || tx.Statement == nil || tx.Statement.Dest == nil {
		return
	}
	if secretstore.Active() == nil {
		return
	}
	for _, row := range secretMapRowsOf(tx.Statement.Dest) {
		if err := protectSecretMapRow(tx, row, isCreate); err != nil {
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
func protectSecretMapRow(tx *gorm.DB, row map[string]interface{}, isCreate bool) error {
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
		return protectChannelKeyMapRow(parsedSchema, tx.Statement.Model, row, isCreate)
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

// protectChannelKeyMapRow channel_keys 全部秘密列(key 与 api_secret)的 map 写入。
// 归属绑定需要 (channel_id, name): map 里改了 name/channel_id 却不重给 key 时,
// 存量密文的绑定会失效, 这种写入直接拒绝。
func protectChannelKeyMapRow(parsedSchema *schema.Schema, modelValue interface{}, row map[string]interface{}, isCreate bool) error {
	keyColumn := secretMapColumn(parsedSchema, row, secretChannelKeyColumn)
	apiSecretColumn := secretMapColumn(parsedSchema, row, secretChannelKeyAPISecretColumn)
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

	// 全部秘密列(key 与 api_secret)同一套处理: map 写入绕过字段序列化器, 少封一列就有一列明文落库。
	// 封存绑定带列名(见 secretstore.ChannelKeyColumnBinding), 两列密文互换位置验不过。
	type secretWrite struct{ mapKey, column string }
	secretWrites := make([]secretWrite, 0, 2)
	if keyColumn != "" && row[keyColumn] != nil {
		secretWrites = append(secretWrites, secretWrite{mapKey: keyColumn, column: secretChannelKeyColumn})
	}
	if apiSecretColumn != "" && row[apiSecretColumn] != nil {
		secretWrites = append(secretWrites, secretWrite{mapKey: apiSecretColumn, column: secretChannelKeyAPISecretColumn})
	}
	if !isCreate && (nameColumn != "" || channelIDColumn != "") && secretstore.Active() != nil && len(secretWrites) < 2 {
		// 改归属(name/channel_id)会让存量密文的 (channel_id, name) 绑定失效: 每一列秘密都必须
		// 随行重给、按新归属重封。只给一列就改名/搬移, 另一列留在旧绑定上, 之后读不开 —— 直接拒绝。
		// 插入没有存量密文, 不受此约束。
		return fmt.Errorf("refusing to move/rename channel key without re-supplying every secret (key and api_secret): stored ciphertext is bound to (channel_id, name)")
	}
	if len(secretWrites) == 0 {
		return nil
	}
	if channelID == 0 {
		return fmt.Errorf("refusing to write channel_keys secrets without channel_id: the secret envelope binds to row ownership")
	}
	for _, write := range secretWrites {
		value, err := secretFieldString(row[write.mapKey])
		if err != nil {
			return fmt.Errorf("channel key %d/%q: %w", channelID, name, err)
		}
		sealed, err := sealSecretValue(secretstore.ChannelKeyColumnBinding(channelID, name, write.column), value)
		if err != nil {
			return fmt.Errorf("channel key %d/%q: %w", channelID, name, err)
		}
		row[write.mapKey] = sealed
	}
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
