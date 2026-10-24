package op

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func apiKeyTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "apikey.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	apiKeyCache.Clear()
	apiKeyFingerprintMap.Clear()
}

// TestAPIKeyCreateStoresFingerprintOnly 落库与缓存都只有指纹与掩码; 明文只留在创建入参里,
// 供创建响应一次性返回, 之后任何读取路径都拿不回明文。
func TestAPIKeyCreateStoresFingerprintOnly(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "hash-me", UserID: 3}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}
	plaintext := key.APIKey
	if plaintext == "" {
		t.Fatal("create must hand the generated plaintext back for the one-time response")
	}
	if key.KeyHash != model.APIKeyFingerprint(plaintext) {
		t.Fatalf("key_hash = %q, want %q", key.KeyHash, model.APIKeyFingerprint(plaintext))
	}

	// 缓存读回: 不含明文。
	cached, err := APIKeyGet(key.ID, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cached.APIKey != "" {
		t.Fatalf("cache carries plaintext: %q", cached.APIKey)
	}
	if cached.KeyHash != key.KeyHash {
		t.Fatalf("cache lost fingerprint: %q", cached.KeyHash)
	}

	// 库里也没有明文列可存。
	if db.GetDB().Migrator().HasColumn(&model.APIKey{}, "api_key") {
		t.Fatal("api_key plaintext column must not exist in the schema")
	}

	// 认证查找: 明文 → 指纹 → 命中。
	found, err := APIKeyGetByAPIKey(plaintext, ctx)
	if err != nil || found.ID != key.ID {
		t.Fatalf("lookup by plaintext failed: id=%d err=%v", found.ID, err)
	}
	if _, err := APIKeyGetByAPIKey("sk-wrong", ctx); err == nil {
		t.Fatal("wrong plaintext must not resolve any key")
	}
}

// TestAPIKeyCreateForbidsCustomPlaintext 新建一律服务端生成, 自定义明文直接拒。
// 自定义的 password 类低熵值一旦有指纹就能被字典爆破, 所以不给任何自定义入口。
func TestAPIKeyCreateForbidsCustomPlaintext(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	for _, custom := range []string{"password", "sk-custom", strings.Repeat("a", 48)} {
		key := model.APIKey{Name: "custom", APIKey: custom}
		if err := APIKeyCreate(&key, ctx); err == nil {
			t.Fatalf("custom plaintext %q must be rejected", custom)
		}
	}
}

// TestAPIKeyCreateAlwaysGeneratesHighEntropy 新建的 Key 必须是服务端高熵随机串, 且每把都不同。
func TestAPIKeyCreateAlwaysGeneratesHighEntropy(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	seen := make(map[string]bool)
	for i := 0; i < 3; i++ {
		key := model.APIKey{Name: "gen", UserID: 2}
		if err := APIKeyCreate(&key, ctx); err != nil {
			t.Fatal(err)
		}
		generated := key.APIKey
		if !strings.HasPrefix(generated, "sk-") || len(generated) < 48 {
			t.Fatalf("generated key too weak: %q", generated)
		}
		for _, r := range generated {
			if r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				continue
			}
			t.Fatalf("generated key has unexpected charset: %q", generated)
		}
		if seen[generated] {
			t.Fatal("generated keys must never repeat")
		}
		seen[generated] = true
	}
}

// TestAPIKeyCreateRejectsDuplicateFingerprint 同一把 Key 不许建第二行。
// 指纹重复是 apiKeyInsert 的护栏: 生成路径撞车概率为零, 但导入/内部调用仍可能撞。
func TestAPIKeyCreateRejectsDuplicateFingerprint(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	plaintext := "sk-switchyard-duplicate"
	first := model.APIKey{Name: "first", APIKey: plaintext}
	if err := apiKeyInsert(&first, ctx); err != nil {
		t.Fatal(err)
	}
	second := model.APIKey{Name: "second", APIKey: plaintext}
	if err := apiKeyInsert(&second, ctx); err == nil {
		t.Fatal("duplicate key must be rejected")
	}
}

// TestAPIKeyInsertRejectsReplayCredentialPrefix 重放凭据有特权语义, 不能自造同前缀的真 Key。
func TestAPIKeyInsertRejectsReplayCredentialPrefix(t *testing.T) {
	apiKeyTestDB(t)
	key := model.APIKey{Name: "spoof", APIKey: apiReplayCredentialPrefix + "1-2-deadbeef"}
	if err := apiKeyInsert(&key, context.Background()); err == nil {
		t.Fatal("key with replay credential prefix must be rejected")
	}
}

// TestAPIKeyListNeverCarriesPlaintext 列表只给 prefix/last4 掩码, 不给明文也不给指纹。
func TestAPIKeyListNeverCarriesPlaintext(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "list", UserID: 11}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	keys, err := APIKeyList(model.Scope{ID: 11}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("list size = %d", len(keys))
	}
	got := keys[0]
	if got.APIKey != "" {
		t.Fatalf("list leaked plaintext: %q", got.APIKey)
	}
	if got.KeyPrefix == "" || got.KeyLast4 == "" {
		t.Fatalf("mask fields missing: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "key_hash") || strings.Contains(string(raw), got.KeyHash) {
		t.Fatalf("fingerprint leaked into list JSON: %s", raw)
	}
}

// TestAPIKeyUpdatePreservesKeyAndUnsubmittedFields 更新改名不能换 Key, 也不能清空没提交的权限与额度。
func TestAPIKeyUpdatePreservesKeyAndUnsubmittedFields(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{
		Name: "before", UserID: 21,
		MaxCost: 9.5, SupportedModels: []string{"gpt"}, QuotaRequests: 4,
		QuotaInputTokens: 40, QuotaOutputTokens: 5, QuotaResetHour: 3,
	}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	plaintext := key.APIKey
	name := "after"
	updated, err := APIKeyUpdate(key.ID, &model.APIKeyUpdateRequest{ID: key.ID, Name: &name},
		model.Scope{ID: 21}, ctx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "after" {
		t.Fatalf("name = %q", updated.Name)
	}
	if updated.KeyHash != key.KeyHash || updated.KeyPrefix != key.KeyPrefix || updated.KeyLast4 != key.KeyLast4 {
		t.Fatalf("key material changed by update: %+v", updated)
	}
	if updated.MaxCost != 9.5 || updated.QuotaRequests != 4 || updated.QuotaInputTokens != 40 ||
		updated.QuotaOutputTokens != 5 || updated.QuotaResetHour != 3 {
		t.Fatalf("unsubmitted fields were cleared: %+v", updated)
	}
	if len(updated.SupportedModels) != 1 || updated.SupportedModels[0] != "gpt" {
		t.Fatalf("permissions lost: %+v", updated.SupportedModels)
	}
	// 原 Key 认证照旧。
	if _, err := APIKeyGetByAPIKey(plaintext, ctx); err != nil {
		t.Fatalf("old key stopped working after rename: %v", err)
	}

	// 显式提交的值(含 0)才生效。
	zero := int64(0)
	updated, err = APIKeyUpdate(key.ID, &model.APIKeyUpdateRequest{ID: key.ID, QuotaRequests: &zero},
		model.Scope{ID: 21}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if updated.QuotaRequests != 0 {
		t.Fatalf("explicit zero not applied: %d", updated.QuotaRequests)
	}
}

// TestAPIKeyUpdateRejectsKeyReplacement 禁止更新替换原 Key: 提交明文即拒绝。
func TestAPIKeyUpdateRejectsKeyReplacement(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "keep", UserID: 31}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	_, err := APIKeyUpdate(key.ID, &model.APIKeyUpdateRequest{ID: key.ID, APIKey: "sk-replacement"},
		model.Scope{ID: 31}, ctx)
	if err == nil {
		t.Fatal("replacing the key through update must fail")
	}
	if _, err := APIKeyGetByAPIKey("sk-replacement", ctx); err == nil {
		t.Fatal("replacement key must not authenticate")
	}
	if _, err := APIKeyGetByAPIKey(key.APIKey, ctx); err != nil {
		t.Fatalf("original key must keep working: %v", err)
	}
}

// TestAPIKeyUpdateEnforcesOwnership 非归属用户改不动别人的 Key。
func TestAPIKeyUpdateEnforcesOwnership(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "owned", UserID: 41}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	name := "stolen"
	if _, err := APIKeyUpdate(key.ID, &model.APIKeyUpdateRequest{ID: key.ID, Name: &name},
		model.Scope{ID: 42}, ctx); err == nil {
		t.Fatal("other user's key must not be updatable")
	}
}

// TestAPIKeyDeleteDropsFingerprintIndex 删除后指纹索引必须一起消失, 否则旧 Key 还能命中缓存。
func TestAPIKeyDeleteDropsFingerprintIndex(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "gone", UserID: 51}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	if err := APIKeyDelete(key.ID, model.Scope{ID: 51}, ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := apiKeyFingerprintMap.Get(key.KeyHash); ok {
		t.Fatal("fingerprint index survived delete")
	}
	if _, err := APIKeyGetByAPIKey(key.APIKey, ctx); err == nil {
		t.Fatal("deleted key must not authenticate")
	}
}

// TestAPIKeyRefreshCacheClearsStaleIndexes 换库/直删行后重建缓存不能残留旧索引。
func TestAPIKeyRefreshCacheClearsStaleIndexes(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "stale", UserID: 61}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Where("id = ?", key.ID).Delete(&model.APIKey{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := apiKeyRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := APIKeyGetByAPIKey(key.APIKey, ctx); err == nil {
		t.Fatal("stale cache entry must be gone after refresh")
	}
	if _, ok := apiKeyFingerprintMap.Get(key.KeyHash); ok {
		t.Fatal("stale fingerprint index survived refresh")
	}
}

// TestAPIKeyBackupImportMigratesLegacyPlaintext 老备份带明文: 导入换算指纹后原 Key 无感可用。
func TestAPIKeyBackupImportMigratesLegacyPlaintext(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	// Version 5 的旧备份(明文 Key 时代)必须仍可导入: 明文现场换算指纹, 原 Key 无感可用。
	legacy := []byte(`{
		"version": 5,
		"api_keys": [
			{"id": 71, "user_id": 7, "name": "legacy", "api_key": "sk-legacy-backup", "enabled": true, "supported_models": []}
		]
	}`)
	var dump model.DBDump
	if err := json.Unmarshal(legacy, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(ctx, &dump); err != nil {
		t.Fatalf("import legacy dump: %v", err)
	}
	found, err := APIKeyGetByAPIKey("sk-legacy-backup", ctx)
	if err != nil || found.ID != 71 {
		t.Fatalf("legacy key must keep working after import: id=%d err=%v", found.ID, err)
	}
	var stored struct {
		KeyHash string `gorm:"column:key_hash"`
	}
	if err := db.GetDB().Table("api_keys").Select("key_hash").Where("id = ?", 71).Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.KeyHash != model.APIKeyFingerprint("sk-legacy-backup") {
		t.Fatalf("import stored %q, want fingerprint", stored.KeyHash)
	}
	if db.GetDB().Migrator().HasColumn(&model.APIKey{}, "api_key") {
		t.Fatal("legacy plaintext column must not be created by import")
	}
}

// TestAPIKeyLegacyWeakKeyStillWorks 存量低熵自定义 Key 不强制轮换: 旧备份里的明文换算指纹后原样可用。
// 「新建封死自定义」只管新 Key; 老 Key 的弱强度是历史遗留, 由使用者自行择期轮换。
func TestAPIKeyLegacyWeakKeyStillWorks(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	legacy := []byte(`{
		"version": 5,
		"api_keys": [
			{"id": 5, "user_id": 1, "name": "legacy-weak", "api_key": "password", "enabled": true, "supported_models": []}
		]
	}`)
	var dump model.DBDump
	if err := json.Unmarshal(legacy, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(ctx, &dump); err != nil {
		t.Fatalf("import legacy weak key: %v", err)
	}
	found, err := APIKeyGetByAPIKey("password", ctx)
	if err != nil || found.ID != 5 {
		t.Fatalf("legacy weak key must keep working without rotation: id=%d err=%v", found.ID, err)
	}
}

// TestAPIKeyBackupRoundTripKeepsKeysWorking 备份导出带指纹不带明文, 恢复后 Key 照常认证。
func TestAPIKeyBackupRoundTripKeepsKeysWorking(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	key := model.APIKey{Name: "backup", UserID: 8}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	plaintext := key.APIKey
	dump, err := DBExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 新导出必须是新版本(6, key_hash 时代); 回退版本号等于把指纹格式藏进旧语义里。
	if dump.Version != 6 {
		t.Fatalf("export version = %d, want 6 (key_hash era)", dump.Version)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, key.KeyHash) {
		t.Fatalf("export lost the fingerprint: %s", body)
	}
	if strings.Contains(body, plaintext) {
		t.Fatalf("export leaked plaintext: %s", body)
	}

	// 恢复到干净库: 指纹随备份走, Key 无需任何客户端改动即可继续用。
	apiKeyTestDB(t)
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(ctx, &restored); err != nil {
		t.Fatalf("import restored dump: %v", err)
	}
	found, err := APIKeyGetByAPIKey(plaintext, ctx)
	if err != nil || found.Name != "backup" {
		t.Fatalf("restored key must authenticate: %+v err=%v", found, err)
	}
}

// TestAPIKeyReplayCredentialRoundTrip 重放凭据: 绑定 Key ID 与过期时间, 篡改一律拒绝。
func TestAPIKeyReplayCredentialRoundTrip(t *testing.T) {
	apiKeyTestDB(t)
	ctx := context.Background()
	if err := SettingUpsert(model.SettingKeyAuthSecret, "replay-test-secret"); err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{Name: "replay", UserID: 9}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatal(err)
	}
	target := key.APIKey

	token, err := APIKeyIssueReplayToken(key.ID, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if strings.Contains(token, target) {
		t.Fatal("replay credential must not carry the key plaintext")
	}
	resolved, err := APIKeyResolveCredential(token, ctx)
	if err != nil || resolved.ID != key.ID {
		t.Fatalf("replay credential must resolve to the key: id=%d err=%v", resolved.ID, err)
	}
	// 普通 Key 凭据走原路径。
	if _, err := APIKeyResolveCredential(target, ctx); err != nil {
		t.Fatalf("plaintext credential path broken: %v", err)
	}

	// 篡改签名。
	tampered := token[:len(token)-2] + "00"
	if _, err := APIKeyResolveCredential(tampered, ctx); err == nil {
		t.Fatal("tampered replay credential must be rejected")
	}
	// 过期凭据。
	expiredPayload := apiReplayCredentialPrefix + strconv.Itoa(key.ID) + "-" + strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	expired := expiredPayload + "-" + apiReplayCredentialSign("replay-test-secret", expiredPayload)
	if _, err := APIKeyResolveCredential(expired, ctx); err == nil {
		t.Fatal("expired replay credential must be rejected")
	}
	// 别的 key 的 ID 冒用。
	forgedPayload := apiReplayCredentialPrefix + strconv.Itoa(key.ID+1) + "-" + strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	forged := forgedPayload + "-" + apiReplayCredentialSign("other-secret", forgedPayload)
	if _, err := APIKeyResolveCredential(forged, ctx); err == nil {
		t.Fatal("forged replay credential must be rejected")
	}
}
