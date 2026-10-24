package model

import (
	"context"
	"fmt"
	"reflect"

	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm/schema"
)

// 静态凭据的字段序列化器与封存策略(随 model 注册)。
//
// 为什么注册在 model 而不是 db: 只要 gorm 解析这两个模型的 schema(建表、迁移、以及任何没 import db
// 的包 —— 例如 internal/db/migrate 出于包环不能 import db), 就必须认识 serializer tag。注册若藏在
// db 的 init 里, 那些包会在运行时炸 "invalid serializer type"。模型自带序列化器, 谁 import model
// 谁就能解析 schema; 无钥测试可继续读写历史明文, 但遇到信封必须有钥才能解密。
//
// 职责边界: 运行时读明文(缓存与调用方拿到的都是明文), 落库写密文(信封格式见 secretstore)。
// 未初始化主密钥时仅历史明文透传; 信封解密一律拒绝, 避免误把密文用于转发;
// 生产入口(cmd start 与 secret 命令)必须先初始化主密钥, 缺失/非法一律拒绝启动。
const (
	secretSettingSerializer    = "switchyard_secret_setting"
	secretChannelKeySerializer = "switchyard_secret_channel_key"
)

func init() {
	schema.RegisterSerializer(secretSettingSerializer, settingSecretCodec{})
	schema.RegisterSerializer(secretChannelKeySerializer, channelKeySecretCodec{})
}

// settingSecretCodec 处理 settings.value: 密钥类设置项(IsSecretSetting/IsInternalSetting)加密封存。
type settingSecretCodec struct{}

func (settingSecretCodec) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue interface{}) (interface{}, error) {
	row, ok := settingRowOf(dst)
	if !ok {
		return nil, fmt.Errorf("secret setting write: unexpected row type %T", dst.Interface())
	}
	value, err := secretFieldString(fieldValue)
	if err != nil {
		return nil, fmt.Errorf("secret setting %q: %w", row.Key, err)
	}
	if !SettingNeedsAtRestEncryption(row.Key) {
		return value, nil
	}
	return sealSecretValue(secretstore.SettingBinding(string(row.Key)), value)
}

func (settingSecretCodec) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) error {
	stored, err := secretFieldString(dbValue)
	if err != nil {
		return fmt.Errorf("secret setting read: %w", err)
	}
	row, ok := settingRowOf(dst)
	if !ok {
		return fmt.Errorf("secret setting read: unexpected row type %T", dst.Interface())
	}
	value, err := openSecretValue(secretstore.SettingBinding(string(row.Key)), stored)
	if err != nil {
		return fmt.Errorf("setting %q: %w", row.Key, err)
	}
	field.ReflectValueOf(ctx, dst).SetString(value)
	return nil
}

// channelKeySecretCodec 处理 channel_keys.key: 渠道凭据全部按秘密加密封存。
type channelKeySecretCodec struct{}

func (channelKeySecretCodec) Value(ctx context.Context, field *schema.Field, dst reflect.Value, fieldValue interface{}) (interface{}, error) {
	row, ok := channelKeyRowOf(dst)
	if !ok {
		return nil, fmt.Errorf("channel key write: unexpected row type %T", dst.Interface())
	}
	value, err := secretFieldString(fieldValue)
	if err != nil {
		return nil, fmt.Errorf("channel key %d: %w", row.ChannelID, err)
	}
	return sealSecretValue(secretstore.ChannelKeyBinding(row.ChannelID, row.Name), value)
}

func (channelKeySecretCodec) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) error {
	stored, err := secretFieldString(dbValue)
	if err != nil {
		return fmt.Errorf("channel key read: %w", err)
	}
	row, ok := channelKeyRowOf(dst)
	if !ok {
		return fmt.Errorf("channel key read: unexpected row type %T", dst.Interface())
	}
	if secretstore.IsEnvelope(stored) && row.ChannelID == 0 {
		// 归属列还没扫进来(或行本身缺归属)就无法校验密文归属, 拒绝解密而不是猜。
		return fmt.Errorf("channel key read: missing ownership identity (channel_id)")
	}
	value, err := openSecretValue(secretstore.ChannelKeyBinding(row.ChannelID, row.Name), stored)
	if err != nil {
		return fmt.Errorf("channel key %d/%q: %w", row.ChannelID, row.Name, err)
	}
	field.ReflectValueOf(ctx, dst).SetString(value)
	return nil
}

// settingRowOf 从序列化器拿到的行值里取出 Setting(兼容值/指针)。
func settingRowOf(dst reflect.Value) (Setting, bool) {
	value := reflect.Indirect(dst)
	if !value.IsValid() || value.Type() != reflect.TypeOf(Setting{}) {
		return Setting{}, false
	}
	row, ok := value.Interface().(Setting)
	return row, ok
}

// channelKeyRowOf 从序列化器拿到的行值里取出 ChannelKey(兼容值/指针)。
func channelKeyRowOf(dst reflect.Value) (ChannelKey, bool) {
	value := reflect.Indirect(dst)
	if !value.IsValid() || value.Type() != reflect.TypeOf(ChannelKey{}) {
		return ChannelKey{}, false
	}
	row, ok := value.Interface().(ChannelKey)
	return row, ok
}

// secretFieldString 把字段值收敛成字符串; nil 视为空串, 其它类型拒绝(如 gorm.Expr)。
func secretFieldString(fieldValue interface{}) (string, error) {
	switch value := fieldValue.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	default:
		return "", fmt.Errorf("secret field must be a string, got %T", fieldValue)
	}
}

// sealSecretValue 明文 -> 信封。空值无秘密可言, 保持空串(不产生无意义的信封行)。
// 已是信封的值原样返回: 迁移与重复写入幂等, 也避免把信封当明文二次加密后解出错误凭据。
func sealSecretValue(binding secretstore.Binding, value string) (string, error) {
	store := secretstore.Active()
	if store == nil || value == "" || secretstore.IsEnvelope(value) {
		return value, nil
	}
	return store.Seal(binding, value)
}

// openSecretValue 信封 -> 明文。历史明文(迁移前)原样透传以保持读兼容;
// 信封必须解开且归属绑定一致, 否则报错(fail closed)。
func openSecretValue(binding secretstore.Binding, value string) (string, error) {
	store := secretstore.Active()
	if value == "" || !secretstore.IsEnvelope(value) {
		return value, nil
	}
	if store == nil {
		return "", fmt.Errorf("cannot open secret envelope: master key is not initialized")
	}
	return store.Open(binding, value)
}

// SecretFieldString 是 secretFieldString 的导出形态: db 的 map 写入回调收敛字段值时复用。
func SecretFieldString(fieldValue interface{}) (string, error) {
	return secretFieldString(fieldValue)
}

// SealSecretValue 是 sealSecretValue 的导出形态: db 的 map 写入回调与字段序列化器
// 必须共用同一套封存策略(幂等、空值透传、无钥透传), 否则两条写入路径会出现两种密文语义。
func SealSecretValue(binding secretstore.Binding, value string) (string, error) {
	return sealSecretValue(binding, value)
}

// OpenSecretValue 是 openSecretValue 的导出形态, 语义同上。
func OpenSecretValue(binding secretstore.Binding, value string) (string, error) {
	return openSecretValue(binding, value)
}
