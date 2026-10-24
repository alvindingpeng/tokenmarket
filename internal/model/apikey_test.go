package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func apiKeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := conn.AutoMigrate(&APIKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// TestAPIKeyFingerprintIsDeterministic 钉住认证查找的前提: 同一把 Key 永远换算出同一个指纹。
// 指纹一旦不稳, 认证就只能全表逐行比对 —— 这正是要杜绝的 bcrypt 式反模式。
func TestAPIKeyFingerprintIsDeterministic(t *testing.T) {
	key := "sk-switchyard-" + strings.Repeat("a", 48)
	first := APIKeyFingerprint(key)
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 (hex sha256)", len(first))
	}
	if again := APIKeyFingerprint(key); again != first {
		t.Fatalf("fingerprint not deterministic: %q vs %q", first, again)
	}
	// 前后空白不改变指纹: 头部凭据常带换行/空格, 不该因此认证不上。
	if trimmed := APIKeyFingerprint("  " + key + "\n"); trimmed != first {
		t.Fatalf("whitespace changed fingerprint: %q vs %q", trimmed, first)
	}
	if other := APIKeyFingerprint(key + "x"); other == first {
		t.Fatal("different keys must not collide")
	}
	if empty := APIKeyFingerprint("   "); empty != "" {
		t.Fatalf("blank key fingerprint = %q, want empty", empty)
	}
}

// TestAPIKeySecretFieldsMaskKeepsSecret 掩码片段不能还原明文: 长短 Key 都只露边缘几段。
func TestAPIKeySecretFieldsMaskKeepsSecret(t *testing.T) {
	key := "sk-switchyard-" + strings.Repeat("b", 48)
	hash, prefix, last4 := APIKeySecretFields(key)
	if hash != APIKeyFingerprint(key) {
		t.Fatal("secret fields must use the same fingerprint as lookup")
	}
	if prefix != key[:12] {
		t.Fatalf("prefix = %q, want first 12 chars", prefix)
	}
	if last4 != key[len(key)-4:] {
		t.Fatalf("last4 = %q, want final 4 chars", last4)
	}

	// 短 Key: 前缀收缩, 掩码两段之间仍留暗段, 不能整串还原。
	short := "12345678"
	_, shortPrefix, shortLast4 := APIKeySecretFields(short)
	if len(shortPrefix)+len(shortLast4) > len(short)-2 {
		t.Fatalf("mask of short key leaks everything: prefix=%q last4=%q", shortPrefix, shortLast4)
	}
}

// TestAPIKeyApplySecretIsImmutable 指纹不可变: 有了指纹再带明文也不许换 Key。
// 这是「禁止更新替换原 Key」的模型层保证 —— 走哪条写入路径都绕不过去。
func TestAPIKeyApplySecretIsImmutable(t *testing.T) {
	key := APIKey{Name: "immutable", APIKey: "sk-original"}
	if err := key.ApplyAPIKeySecret(); err != nil {
		t.Fatal(err)
	}
	firstHash := key.KeyHash
	if firstHash == "" {
		t.Fatal("plaintext must derive a fingerprint")
	}
	key.APIKey = "sk-replacement"
	if err := key.ApplyAPIKeySecret(); err != nil {
		t.Fatal(err)
	}
	if key.KeyHash != firstHash {
		t.Fatalf("fingerprint changed on update: %q -> %q", firstHash, key.KeyHash)
	}
}

// TestAPIKeyCreateDerivesFingerprintAndNeverStoresPlaintext 落库只有指纹与掩码, 明文列根本不存在。
func TestAPIKeyCreateDerivesFingerprintAndNeverStoresPlaintext(t *testing.T) {
	conn := apiKeyTestDB(t)
	plaintext := "sk-switchyard-" + strings.Repeat("c", 48)
	key := APIKey{Name: "create", APIKey: plaintext, UserID: 7}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if key.KeyHash != APIKeyFingerprint(plaintext) {
		t.Fatalf("stored fingerprint = %q, want %q", key.KeyHash, APIKeyFingerprint(plaintext))
	}
	if key.KeyPrefix == "" || key.KeyLast4 == "" {
		t.Fatalf("mask fields missing: prefix=%q last4=%q", key.KeyPrefix, key.KeyLast4)
	}

	var loaded APIKey
	if err := conn.First(&loaded, key.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.APIKey != "" {
		t.Fatalf("plaintext persisted to database: %q", loaded.APIKey)
	}
	if loaded.KeyHash != key.KeyHash {
		t.Fatalf("fingerprint lost on reload: %q vs %q", loaded.KeyHash, key.KeyHash)
	}
}

// TestAPIKeyCreateRejectsRowWithoutSecret 没有明文也没有指纹的行落库后永远认证不上, 直接拒绝。
func TestAPIKeyCreateRejectsRowWithoutSecret(t *testing.T) {
	conn := apiKeyTestDB(t)
	if err := conn.Create(&APIKey{Name: "broken"}).Error; err == nil {
		t.Fatal("row without plaintext or fingerprint must be rejected")
	}
}

// TestAPIKeyUpdatesCannotSwapFingerprint 混进来的明文不会改掉已签发 Key 的指纹。
func TestAPIKeyUpdatesCannotSwapFingerprint(t *testing.T) {
	conn := apiKeyTestDB(t)
	key := APIKey{Name: "update", APIKey: "sk-keep-me"}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	key.APIKey = "sk-sneaky-replacement"
	key.Name = "renamed"
	if err := conn.Model(&APIKey{}).Where("id = ?", key.ID).
		Select("name").Updates(&key).Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	var loaded APIKey
	if err := conn.First(&loaded, key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.KeyHash != APIKeyFingerprint("sk-keep-me") {
		t.Fatalf("fingerprint replaced through update: %q", loaded.KeyHash)
	}
	if loaded.Name != "renamed" {
		t.Fatalf("name = %q, want renamed", loaded.Name)
	}
}

// TestAPIKeyJSONNeverExposesFingerprint 指纹字段是 json:"-": 任何 API 响应都带不出去。
// 列表只给 prefix/last4 掩码; 明文只随创建响应出现一次。
func TestAPIKeyJSONNeverExposesFingerprint(t *testing.T) {
	key := APIKey{
		Name:    "json",
		APIKey:  "sk-shown-once",
		KeyHash: "deadbeef",
	}
	if err := key.ApplyAPIKeySecret(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "key_hash") || strings.Contains(body, "deadbeef") {
		t.Fatalf("fingerprint leaked into JSON: %s", body)
	}
	if !strings.Contains(body, "\"key_prefix\"") || !strings.Contains(body, "\"key_last4\"") {
		t.Fatalf("mask fields missing from JSON: %s", body)
	}
	if !strings.Contains(body, "sk-shown-once") {
		t.Fatalf("create response must carry the plaintext once: %s", body)
	}

	// 列表/详情等后续响应: 明文字段为空时整段省略。
	key.APIKey = ""
	raw, err = json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "api_key") {
		t.Fatalf("plaintext field must be omitted when empty: %s", raw)
	}
}
