package op

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

// apiKeyCache 主键 → Key; apiKeyFingerprintMap 指纹 → 主键。
// 认证是拿明文换指纹再查映射, O(1) 命中, 不做任何逐行比对。
var apiKeyCache = cache.New[int, model.APIKey](16)
var apiKeyFingerprintMap = cache.New[string, int](16)

// APIKeyCreate 新建 Key: 一律由服务端生成高熵随机串, 不接受任何自定义明文。
//
// 为什么必须生成而不是「自定义但校验强度」: 指纹是无盐 SHA-256, 这种查找方案只配高熵随机凭据 ——
// 自定义的 password 类低熵值一旦落了指纹(库里、备份里都有), 就能被字典爆破还原。
// 新建这条路因此彻底封死自定义: 调用方塞了明文直接报错, 不静默覆盖。
// 存量 Key 不受影响: 旧库迁移与旧备份导入走换算路径, 弱 Key 原样可用, 不强制轮换。
//
// 明文只在出参里走一遭: 落库前换算成指纹, 缓存里也不留明文。
func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	if key == nil {
		return fmt.Errorf("api key is required")
	}
	if strings.TrimSpace(key.APIKey) != "" {
		return fmt.Errorf("custom api keys are not accepted: the key is generated server-side")
	}
	plaintext, err := GenerateAPIKey()
	if err != nil {
		return fmt.Errorf("failed to generate api key: %w", err)
	}
	key.APIKey = plaintext
	return apiKeyInsert(key, ctx)
}

// GenerateAPIKey 生成服务端签发的高熵 Key: "sk-" + 应用名 + 48 位 base62(crypto/rand, 约 283 bit)。
// 生成收敛在 op 层, 是「新建必随机」这条不变量的唯一出口, 不依赖调用方自觉。
func GenerateAPIKey() (string, error) {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	max := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b), nil
}

// apiKeyInsert 用 key.APIKey 换算指纹并落库。仅供 APIKeyCreate 与测试使用 ——
// 任何面向用户的入口都必须先过 APIKeyCreate 的「必生成」策略。
func apiKeyInsert(key *model.APIKey, ctx context.Context) error {
	if key == nil {
		return fmt.Errorf("api key is required")
	}
	plaintext := strings.TrimSpace(key.APIKey)
	if plaintext != "" && strings.HasPrefix(plaintext, apiReplayCredentialPrefix) {
		// 重放凭据有特权语义, 不能让人自造一把同前缀的真 Key 冒充。
		return fmt.Errorf("api key must not start with %q", apiReplayCredentialPrefix)
	}
	if err := key.ApplyAPIKeySecret(); err != nil {
		return err
	}
	if key.KeyHash == "" {
		return fmt.Errorf("api key secret is required")
	}
	// 同一把 Key 不许建第二行: 指纹是查找索引, 重复会让归属与计费都变得不确定。
	// 以库为准判断, 缓存只是加速层, 不能拿它当唯一凭据。
	var dup int64
	if err := db.GetDB().WithContext(ctx).Model(&model.APIKey{}).
		Where("key_hash = ?", key.KeyHash).Count(&dup).Error; err != nil {
		return fmt.Errorf("failed to check api key: %w", err)
	}
	if dup > 0 {
		return fmt.Errorf("api key already exists")
	}
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	cached := *key
	cached.APIKey = "" // 缓存不落明文: 明文只有创建响应这一次出场。
	apiKeyCache.Set(key.ID, cached)
	apiKeyFingerprintMap.Set(key.KeyHash, key.ID)
	return nil
}

// APIKeyUpdate 更新 Key 的可编辑字段。未提交的字段保留原值, 权限与额度不会被顺手清零。
//
// Key 本身不可更新: 请求里带明文即拒绝; 即便混进来, 模型层的指纹不可变规则也换不掉原 Key。
func APIKeyUpdate(id int, req *model.APIKeyUpdateRequest, scope model.Scope, ctx context.Context) (*model.APIKey, error) {
	if req == nil {
		return nil, fmt.Errorf("update request is required")
	}
	if strings.TrimSpace(req.APIKey) != "" {
		return nil, fmt.Errorf("api key cannot be replaced; create a new key instead")
	}
	existing, ok := apiKeyCache.Get(id)
	// 密钥是用户级凭据, 严格按归属隔离: 管理员也只可改自己的。
	if !ok || existing.UserID != scope.ID {
		return nil, fmt.Errorf("API key not found")
	}

	updated := existing
	selectFields := make([]string, 0, 10)
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("api key name is required")
		}
		updated.Name = name
		selectFields = append(selectFields, "name")
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
		selectFields = append(selectFields, "enabled")
	}
	if req.ExpireAt != nil {
		updated.ExpireAt = *req.ExpireAt
		selectFields = append(selectFields, "expire_at")
	}
	if req.MaxCost != nil {
		updated.MaxCost = *req.MaxCost
		selectFields = append(selectFields, "max_cost")
	}
	if req.SupportedModels != nil {
		updated.SupportedModels = *req.SupportedModels
		selectFields = append(selectFields, "supported_models")
	}
	if req.RoutingPreference != nil {
		preference := *req.RoutingPreference
		if preference == "" {
			preference = model.RoutingPreferenceInherit
		}
		if !model.IsValidRoutingPreference(preference) {
			return nil, fmt.Errorf("invalid routing_preference")
		}
		updated.RoutingPreference = preference
		selectFields = append(selectFields, "routing_preference")
	}
	if req.QuotaRequests != nil {
		updated.QuotaRequests = *req.QuotaRequests
		selectFields = append(selectFields, "quota_requests")
	}
	if req.QuotaInputTokens != nil {
		updated.QuotaInputTokens = *req.QuotaInputTokens
		selectFields = append(selectFields, "quota_input_tokens")
	}
	if req.QuotaOutputTokens != nil {
		updated.QuotaOutputTokens = *req.QuotaOutputTokens
		selectFields = append(selectFields, "quota_output_tokens")
	}
	if req.QuotaResetHour != nil {
		updated.QuotaResetHour = *req.QuotaResetHour
		selectFields = append(selectFields, "quota_reset_hour")
	}

	if len(selectFields) > 0 {
		// 只写提交了的列: key_hash / key_prefix / key_last4 永远不进更新列集合。
		writes := model.APIKey{ID: id}
		writes.Name = updated.Name
		writes.Enabled = updated.Enabled
		writes.ExpireAt = updated.ExpireAt
		writes.MaxCost = updated.MaxCost
		writes.SupportedModels = updated.SupportedModels
		writes.RoutingPreference = updated.RoutingPreference
		writes.QuotaRequests = updated.QuotaRequests
		writes.QuotaInputTokens = updated.QuotaInputTokens
		writes.QuotaOutputTokens = updated.QuotaOutputTokens
		writes.QuotaResetHour = updated.QuotaResetHour
		if err := db.GetDB().WithContext(ctx).Model(&model.APIKey{}).
			Where("id = ?", id).Select(selectFields).Updates(&writes).Error; err != nil {
			return nil, fmt.Errorf("failed to update API key: %w", err)
		}
	}
	updated.APIKey = ""
	apiKeyCache.Set(id, updated)
	return &updated, nil
}

// APIKeyList 返回访问者自有的 API Key, 按主键升序定序。
// 密钥严格按归属隔离, 管理员也只见自己的; 设置页不提供排序开关,
// 而缓存遍历顺序随机, 故顺序须由此处定稿。
func APIKeyList(scope model.Scope, ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for _, apiKey := range apiKeyCache.GetAll() {
		if apiKey.UserID != scope.ID {
			continue
		}
		apiKey.APIKey = ""
		keys = append(keys, apiKey)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys, nil
}

// APIKeyGet 按主键取 API Key, 不做归属校验(转发链路内部使用)。
func APIKeyGet(id int, ctx context.Context) (model.APIKey, error) {
	if apiKey, ok := apiKeyCache.Get(id); ok {
		return apiKey, nil
	}
	// 缓存缺失时回落数据库: 重启窗口、异步写入都不该让认证突然失效。
	var apiKey model.APIKey
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&apiKey).Error; err != nil {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	apiKeyCache.Set(apiKey.ID, apiKey)
	if apiKey.KeyHash != "" {
		apiKeyFingerprintMap.Set(apiKey.KeyHash, apiKey.ID)
	}
	return apiKey, nil
}

// APIKeyGetScoped 按主键取 API Key 并严格校验归属, 非归属按不存在处理。
func APIKeyGetScoped(id int, scope model.Scope, ctx context.Context) (model.APIKey, error) {
	apiKey, err := APIKeyGet(id, ctx)
	if err != nil || apiKey.UserID != scope.ID {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

// APIKeyGetByAPIKey 按明文找 Key: 明文 → 指纹 → 命中索引。
// 不做逐行 bcrypt 比对: 那种做法每次认证都要全表扫描, 高熵随机 Key 用确定性指纹即可。
func APIKeyGetByAPIKey(plaintext string, ctx context.Context) (model.APIKey, error) {
	fingerprint := model.APIKeyFingerprint(plaintext)
	if fingerprint == "" {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	if id, ok := apiKeyFingerprintMap.Get(fingerprint); ok {
		return APIKeyGet(id, ctx)
	}
	var apiKey model.APIKey
	if err := db.GetDB().WithContext(ctx).
		Where("key_hash = ?", fingerprint).Order("id").First(&apiKey).Error; err != nil {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	apiKeyCache.Set(apiKey.ID, apiKey)
	apiKeyFingerprintMap.Set(fingerprint, apiKey.ID)
	return apiKey, nil
}

func APIKeyDelete(id int, scope model.Scope, ctx context.Context) error {
	existing, ok := apiKeyCache.Get(id)
	if !ok || existing.UserID != scope.ID {
		return fmt.Errorf("API key not found")
	}
	k := model.APIKey{
		ID: id,
	}
	if err := StatsAPIKeyDel(id); err != nil {
		return fmt.Errorf("failed to delete stats API key: %v", err)
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	apiKeyCache.Del(k.ID)
	if existing.KeyHash != "" {
		apiKeyFingerprintMap.Del(existing.KeyHash)
	}
	return nil
}

// apiKeyRefreshCache 从库内重建 Key 缓存; 先清后建, 换库/删行后不残留旧索引。
func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	apiKeyCache.Clear()
	apiKeyFingerprintMap.Clear()
	for _, apiKey := range apiKeys {
		apiKey.APIKey = ""
		apiKeyCache.Set(apiKey.ID, apiKey)
		if apiKey.KeyHash != "" {
			apiKeyFingerprintMap.Set(apiKey.KeyHash, apiKey.ID)
		}
	}
	return nil
}

// 重放凭据前缀: 与真实 Key 的 sk- 前缀明显区分, 见到它就知道这是短命的自调用令牌。
const apiReplayCredentialPrefix = "skr-"

// apiReplayCredentialTTL 重放凭据有效期: 覆盖单次重放最长 10 分钟的转发超时, 又不留长尾。
const apiReplayCredentialTTL = 15 * time.Minute

// APIKeyIssueReplayToken 签发短命重放凭据。
//
// 重放要用指定 Key 重新走一遍转发链路, 自调用必须过 APIKeyAuth; 但库里只剩指纹,
// 明文无法找回, 所以改用签名凭据顶替: 绑定 key ID 与过期时间, 用全局签名密钥做 HMAC-SHA256,
// 只有本进程能签, 且只在回环自调用里出现, 不经任何接口回给客户端。
func APIKeyIssueReplayToken(keyID int, ttl time.Duration) (string, error) {
	if keyID <= 0 {
		return "", fmt.Errorf("invalid api key id")
	}
	if ttl <= 0 {
		ttl = apiReplayCredentialTTL
	}
	secret, err := SettingGetString(model.SettingKeyAuthSecret)
	if err != nil || secret == "" {
		return "", fmt.Errorf("auth secret is not ready")
	}
	exp := time.Now().Add(ttl).Unix()
	payload := apiReplayCredentialPrefix + strconv.Itoa(keyID) + "-" + strconv.FormatInt(exp, 10)
	return payload + "-" + apiReplayCredentialSign(secret, payload), nil
}

func apiReplayCredentialSign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// APIKeyGetByReplayCredential 验签并解析重放凭据, 签名或有效期不对一律拒绝。
func APIKeyGetByReplayCredential(credential string, ctx context.Context) (model.APIKey, error) {
	parts := strings.Split(credential, "-")
	if len(parts) != 4 || parts[0] != strings.TrimSuffix(apiReplayCredentialPrefix, "-") {
		return model.APIKey{}, fmt.Errorf("invalid replay credential")
	}
	keyID, err := strconv.Atoi(parts[1])
	if err != nil || keyID <= 0 {
		return model.APIKey{}, fmt.Errorf("invalid replay credential")
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || exp < time.Now().Unix() {
		return model.APIKey{}, fmt.Errorf("replay credential expired")
	}
	secret, err := SettingGetString(model.SettingKeyAuthSecret)
	if err != nil || secret == "" {
		return model.APIKey{}, fmt.Errorf("auth secret is not ready")
	}
	want := apiReplayCredentialSign(secret, apiReplayCredentialPrefix+strconv.Itoa(keyID)+"-"+parts[2])
	if !hmac.Equal([]byte(want), []byte(parts[3])) {
		return model.APIKey{}, fmt.Errorf("invalid replay credential")
	}
	return APIKeyGet(keyID, ctx)
}

// APIKeyResolveCredential 解析请求头里的凭据: 常规 Key 按指纹查, 重放凭据验签后按 ID 取。
func APIKeyResolveCredential(credential string, ctx context.Context) (model.APIKey, error) {
	if strings.HasPrefix(credential, apiReplayCredentialPrefix) {
		return APIKeyGetByReplayCredential(credential, ctx)
	}
	return APIKeyGetByAPIKey(credential, ctx)
}
