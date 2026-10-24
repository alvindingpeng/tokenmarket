package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// APIKey 客户端访问密钥。
//
// 明文只活一次: 创建时生成(或由调用方提交), 随创建响应返回一次, 之后任何接口都拿不到。
// 落库只存 SHA-256 指纹与掩码片段, 库里没有可逆的密钥副本:
//   - APIKey    明文, gorm:"-" 不参与任何读写, 只在创建响应里出现一次;
//   - KeyHash   指纹(hex SHA-256), 认证查找的索引列, json:"-" 让所有 JSON 响应都带不出去;
//   - KeyPrefix/KeyLast4 掩码片段, 列表与详情只给这两段, 由前端拼掩码。
//
// 备份必须带走指纹(恢复后 Key 才仍然可用), 但备份又不能因此把指纹塞进 API 响应,
// 故 dump 的 JSON 形状单独在 apikey_dump.go 里处理, 两种序列化互不干扰。
type APIKey struct {
	ID                int      `json:"id" gorm:"primaryKey"`
	UserID            uint     `json:"user_id" gorm:"index;not null;default:0"` // 归属用户; 计费与数据隔离的归属依据。
	Name              string   `json:"name" gorm:"not null"`
	APIKey            string   `json:"api_key,omitempty" gorm:"-"`                                      // 明文: 创建时一次返回, 从不落库, 也永不回显。
	KeyHash           string   `json:"-" gorm:"column:key_hash;size:64;index;not null;default:''"`      // 指纹, 认证查找用; 不进任何 JSON 响应。
	KeyPrefix         string   `json:"key_prefix" gorm:"column:key_prefix;size:32;not null;default:''"` // 掩码前缀。
	KeyLast4          string   `json:"key_last4" gorm:"column:key_last4;size:4;not null;default:''"`    // 掩码尾段。
	Enabled           bool     `json:"enabled" gorm:"default:true"`
	ExpireAt          int64    `json:"expire_at,omitempty"`
	MaxCost           float64  `json:"max_cost,omitempty"`
	SupportedModels   []string `json:"supported_models" gorm:"serializer:json"`            // 允许访问的分组名称, 空表示不限制; 以 JSON 数组存储, 读写两侧都无需再拆分隔符。
	RoutingPreference string   `json:"routing_preference" gorm:"not null;default:inherit"` // inherit|balanced|price|latency|success; 每个 Key 的选路偏好。
	// 配额限制: 0 表示无限制, 按每日重置。
	QuotaRequests     int64 `json:"quota_requests" gorm:"default:0"`      // 每日请求数配额。
	QuotaInputTokens  int64 `json:"quota_input_tokens" gorm:"default:0"`  // 每日输入 token 配额。
	QuotaOutputTokens int64 `json:"quota_output_tokens" gorm:"default:0"` // 每日输出 token 配额。
	QuotaResetHour    int   `json:"quota_reset_hour" gorm:"default:0"`    // 配额重置时刻(0-23), 默认 0 点。
}

// 掩码片段长度: 前缀够辨认是哪把 Key, 尾段对齐常见 Key 展示习惯, 两段都不足以还原明文。
const (
	apiKeyPrefixLen = 12
	apiKeySuffixLen = 4
)

// APIKeyFingerprint 返回 Key 的确定性指纹(hex SHA-256), 认证按它命中索引。
//
// 为什么是 SHA-256 而不是 bcrypt: 认证是「拿明文找记录」, 必须能按指纹直接查索引。
// bcrypt 每次盐不同, 同一把 Key 每回算出来都不一样, 只能全表逐行比对 —— 每个请求一次全表扫描,
// 这正是要避免的反模式。Key 是 48 位随机字符的高熵值, 没有字典爆破面, 也就不需要慢哈希;
// 指纹也不加盐: 加盐就无法用「明文 → 指纹」定位记录了。
func APIKeyFingerprint(plaintext string) string {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// APIKeySecretFields 把明文换算成落库三件套: 指纹 + 掩码前缀 + 掩码尾段。
// 短 Key 的前缀按长度收缩, 保证掩码不会整段还原明文。
func APIKeySecretFields(plaintext string) (hash, prefix, last4 string) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", "", ""
	}
	hash = APIKeyFingerprint(plaintext)
	// 前缀与尾段之间至少留两个暗字符: 短 Key 也不许被掩码整段还原。
	prefixLen := min(apiKeyPrefixLen, max(0, len(plaintext)-apiKeySuffixLen-2))
	return hash, plaintext[:prefixLen], plaintext[max(0, len(plaintext)-apiKeySuffixLen):]
}

// ApplyAPIKeySecret 由明文换算指纹与掩码; 指纹已存在时一律不碰。
//
// 「指纹不可变」就是禁止更新替换原 Key 的模型层保证: 无论谁走哪条写入路径,
// 已签发的 Key 都换不掉, 只能新建一把再删旧的 —— 权限与额度因此始终跟着原 Key。
func (k *APIKey) ApplyAPIKeySecret() error {
	if k.KeyHash != "" {
		return nil
	}
	if strings.TrimSpace(k.APIKey) == "" {
		return nil
	}
	k.KeyHash, k.KeyPrefix, k.KeyLast4 = APIKeySecretFields(k.APIKey)
	return nil
}

// BeforeCreate 新建行必须带可用凭据: 要么带明文(现场换算), 要么自带指纹(备份导入)。
// 两样都没有的行落库后永远认证不上, 直接拒绝, 不留这种垃圾行。
func (k *APIKey) BeforeCreate(tx *gorm.DB) error {
	if err := k.ApplyAPIKeySecret(); err != nil {
		return err
	}
	if k.KeyHash == "" {
		return fmt.Errorf("api key %q has no secret: plaintext or fingerprint required", k.Name)
	}
	return nil
}

// BeforeSave 覆盖 Save/Updates 路径: 同一套换算但不校验 ——
// 部分字段更新的结构体本来就只带变更字段, 不能要求它带 Key。
func (k *APIKey) BeforeSave(tx *gorm.DB) error {
	return k.ApplyAPIKeySecret()
}

// APIKeyUpdateRequest 是 Key 允许更新的字段; 指针区分「未提交」与「提交零值」,
// 未提交的字段一律保留原值 —— 改个名字不该把配额、额度、权限顺手清零。
//
// Key 本身不在更新范围: APIKey 字段只用于识别「替换密钥」的企图, 非空即由调用方拒绝。
type APIKeyUpdateRequest struct {
	ID                int       `json:"id"` // 待更新 Key 的主键。
	Name              *string   `json:"name,omitempty"`
	Enabled           *bool     `json:"enabled,omitempty"`
	ExpireAt          *int64    `json:"expire_at,omitempty"`
	MaxCost           *float64  `json:"max_cost,omitempty"`
	SupportedModels   *[]string `json:"supported_models,omitempty"`
	RoutingPreference *string   `json:"routing_preference,omitempty"`
	QuotaRequests     *int64    `json:"quota_requests,omitempty"`
	QuotaInputTokens  *int64    `json:"quota_input_tokens,omitempty"`
	QuotaOutputTokens *int64    `json:"quota_output_tokens,omitempty"`
	QuotaResetHour    *int      `json:"quota_reset_hour,omitempty"`
	APIKey            string    `json:"api_key,omitempty"` // 明文不接受更新: 非空即拒绝, Key 只能新建。
}
