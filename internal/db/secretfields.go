package db

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
)

// 静态凭据的落库加密(DB 存储集成)。
//
// 字段序列化器(模型 tag serializer:switchyard_secret_*)注册在 internal/model/secretcodec.go:
// 只要 gorm 解析模型 schema 就需要认识 tag, 注册必须随 model 走(含不能 import db 的 internal/db/migrate)。
// 这里只保留 db 侧的 map 写入兜底:
//
//	before create/update 回调: 兜住 map 更新与 map 插入 —— gorm 的 map 写入路径绕过字段序列化器,
//	不拦截就会把明文直接写库。map 写入携带不了行归属时直接报错(fail closed), 不降级成弱绑定。
//
// 未初始化主密钥时(secretstore.Active() == nil)保持透传: 单测与离线工具无钥可用;
// 生产入口(cmd start 与 secret 命令)必须先初始化主密钥, 缺失/非法一律拒绝启动。
const (
	secretSettingTable    = "settings"
	secretChannelKeyTable = "channel_keys"

	secretSettingValueColumn = "value"
	secretChannelKeyColumn   = "key"
	// channel_keys 里带 serializer 的秘密列都要被 map 写入兜底覆盖; 少一列就有一列明文落库。
	secretChannelKeyAPISecretColumn = "api_secret"
)

// registerSecretFieldProtection 挂接 map 写入兜底回调; 在 InitDB 打开连接后调用一次。
// 插入与更新分开挂: 插入没有存量密文, 不受"改归属必须重给每一列秘密"约束。
func registerSecretFieldProtection(connector *gorm.DB) {
	_ = connector.Callback().Create().Before("gorm:create").Register("switchyard:protect_secret_create", func(tx *gorm.DB) { protectSecretMapWrites(tx, true) })
	_ = connector.Callback().Update().Before("gorm:update").Register("switchyard:protect_secret_update", func(tx *gorm.DB) { protectSecretMapWrites(tx, false) })
}

// 小包装: map 写入回调与字段序列化器共用 model 里的同一套封存策略(幂等、空值透传、无钥透传)。

func secretFieldString(fieldValue interface{}) (string, error) {
	return model.SecretFieldString(fieldValue)
}

func sealSecretValue(binding secretstore.Binding, value string) (string, error) {
	return model.SealSecretValue(binding, value)
}
