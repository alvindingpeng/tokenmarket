package db

import (
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

// setupSecretDB 起一个带主密钥的独立库; 每个测试一份, 互不干扰。
func setupSecretDB(t *testing.T, kek []byte) {
	t.Helper()
	secretstore.Reset()
	t.Cleanup(secretstore.Reset)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: hex.EncodeToString(kek)}); err != nil {
		t.Fatalf("init secret store: %v", err)
	}
	if err := InitDB("sqlite", filepath.Join(t.TempDir(), "secret.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
}

func testKEK(tag byte) []byte {
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i) ^ tag
	}
	return kek
}

// createTestChannel 建一条归属行: channel_keys 有外键, 凭据行必须先有渠道。
func createTestChannel(t *testing.T, id int) {
	t.Helper()
	channel := model.Channel{ID: id, UserID: 0, ChannelConfig: model.ChannelConfig{Name: "ch-" + strconv.Itoa(id)}}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
}

func rawSettingValue(t *testing.T, key model.SettingKey) string {
	t.Helper()
	var value string
	if err := db.Raw("SELECT value FROM settings WHERE key = ?", string(key)).Scan(&value).Error; err != nil {
		t.Fatalf("raw read setting %s: %v", key, err)
	}
	return value
}

func rawChannelKeyValue(t *testing.T, id int) string {
	t.Helper()
	var value string
	if err := db.Raw("SELECT key FROM channel_keys WHERE id = ?", id).Scan(&value).Error; err != nil {
		t.Fatalf("raw read channel key %d: %v", id, err)
	}
	return value
}

func TestSettingSecretSealedAtRest(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "sk-epay-live"}).Error; err != nil {
		t.Fatalf("create secret setting: %v", err)
	}
	if err := db.Create(&model.Setting{Key: model.SettingKeySiteName, Value: "plain-name"}).Error; err != nil {
		t.Fatalf("create plain setting: %v", err)
	}

	stored := rawSettingValue(t, model.SettingKeyEpayKey)
	if !secretstore.IsEnvelope(stored) {
		t.Fatalf("secret setting stored in plaintext: %q", stored)
	}
	if strings.Contains(stored, "sk-epay-live") {
		t.Fatalf("secret leaked into envelope text: %q", stored)
	}
	if plain := rawSettingValue(t, model.SettingKeySiteName); plain != "plain-name" {
		t.Fatalf("non-secret setting must stay plaintext, got %q", plain)
	}

	// 运行时读明文: 扫描结果必须是解密后的原值。
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read secret setting: %v", err)
	}
	if got.Value != "sk-epay-live" {
		t.Fatalf("read back = %q, want %q", got.Value, "sk-epay-live")
	}
}

func TestInternalSettingsSealedAtRest(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	if err := db.Create(&model.Setting{Key: model.SettingKeyAuthSecret, Value: "jwt-signing-secret"}).Error; err != nil {
		t.Fatalf("create auth secret: %v", err)
	}
	if err := db.Create(&model.Setting{Key: model.SettingKeyMetricsToken, Value: "metrics-token"}).Error; err != nil {
		t.Fatalf("create metrics token: %v", err)
	}
	for _, key := range []model.SettingKey{model.SettingKeyAuthSecret, model.SettingKeyMetricsToken} {
		if !secretstore.IsEnvelope(rawSettingValue(t, key)) {
			t.Fatalf("internal setting %s stored in plaintext", key)
		}
	}
}

func TestSettingMapUpdateSealedAndReadable(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "first"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	// SettingSetString 形态: Update("Value", ...) 走 map 更新路径, 绕过字段序列化器。
	if err := db.Model(&model.Setting{Key: model.SettingKeyEpayKey}).
		Update("Value", "second").Error; err != nil {
		t.Fatalf("map update: %v", err)
	}
	stored := rawSettingValue(t, model.SettingKeyEpayKey)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "second") {
		t.Fatalf("map update wrote plaintext: %q", stored)
	}

	// SettingUpsert 形态: Model 带主键 + Where + Updates(map)。
	if err := db.Model(&model.Setting{Key: model.SettingKeyEpayKey}).
		Where("key = ?", model.SettingKeyEpayKey).
		Updates(map[string]any{"value": "third"}).Error; err != nil {
		t.Fatalf("upsert-style map update: %v", err)
	}
	if stored := rawSettingValue(t, model.SettingKeyEpayKey); !secretstore.IsEnvelope(stored) {
		t.Fatalf("upsert-style map update wrote plaintext: %q", stored)
	}

	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Value != "third" {
		t.Fatalf("read back = %q, want %q", got.Value, "third")
	}
}

func TestMapUpdateWithoutOwnershipFailsClosed(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "keep-me"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	// Model 不带行身份: 回调无法把密文绑到设置键, 必须拒绝而不是写入弱绑定密文。
	err := db.Model(&model.Setting{}).Where("key = ?", model.SettingKeyEpayKey).
		Updates(map[string]any{"value": "leaked"}).Error
	if err == nil {
		t.Fatal("map update without ownership identity succeeded, want fail-closed error")
	}
	if stored := rawSettingValue(t, model.SettingKeyEpayKey); strings.Contains(stored, "leaked") {
		t.Fatalf("failed update still changed stored value: %q", stored)
	}
}

func TestChannelKeySealedAtRest(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "default", Key: "sk-upstream-1", Enabled: true}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	stored := rawChannelKeyValue(t, row.ID)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "sk-upstream-1") {
		t.Fatalf("channel key stored in plaintext: %q", stored)
	}

	// map 更新(带归属身份)后仍是密文, 读回明文。
	identity := model.ChannelKey{ID: row.ID, ChannelID: row.ChannelID,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: row.Name}}
	if err := db.Model(&identity).Where("id = ?", row.ID).
		Updates(map[string]any{"key": "sk-upstream-2", "enabled": true}).Error; err != nil {
		t.Fatalf("map update channel key: %v", err)
	}
	if stored := rawChannelKeyValue(t, row.ID); !secretstore.IsEnvelope(stored) {
		t.Fatalf("map update wrote plaintext: %q", stored)
	}

	var got model.ChannelKey
	if err := db.Where("id = ?", row.ID).First(&got).Error; err != nil {
		t.Fatalf("read channel key: %v", err)
	}
	if got.Key != "sk-upstream-2" || got.Name != "default" || got.ChannelID != 7 {
		t.Fatalf("read back = %+v", got)
	}

	// 改名却不重给 key: 存量密文绑定会失效, 写入必须被拒绝。
	err := db.Model(&model.ChannelKey{ID: row.ID}).Where("id = ?", row.ID).
		Updates(map[string]any{"name": "renamed"}).Error
	if err == nil {
		t.Fatal("rename without re-supplying key succeeded, want fail-closed error")
	}
}

func TestEnvelopeRelocationRejected(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "epay-secret"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Create(&model.Setting{Key: model.SettingKeyStripeSecretKey, Value: "stripe-secret"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	// 手工把 epay 的密文搬到 stripe 行: 解密必须失败而不是读出别人的秘密。
	if err := db.Exec("UPDATE settings SET value = (SELECT value FROM settings WHERE key = ?) WHERE key = ?",
		model.SettingKeyEpayKey, model.SettingKeyStripeSecretKey).Error; err != nil {
		t.Fatalf("relocate ciphertext: %v", err)
	}
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyStripeSecretKey).First(&got).Error; err == nil {
		t.Fatalf("relocated ciphertext decrypted to %q, want ownership mismatch error", got.Value)
	}

	createTestChannel(t, 7)
	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "a", Key: "key-a", Enabled: true}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}
	if err := db.Exec("INSERT INTO channel_keys (channel_id, name, key, enabled) VALUES (7, 'b', (SELECT key FROM channel_keys WHERE id = ?), 1)", row.ID).Error; err != nil {
		t.Fatalf("relocate channel key ciphertext: %v", err)
	}
	var keys []model.ChannelKey
	if err := db.Where("channel_id = ?", 7).Find(&keys).Error; err == nil {
		t.Fatalf("channel key scan accepted relocated ciphertext (%d rows), want ownership mismatch error", len(keys))
	}
}

func TestLegacyPlaintextReadableBeforeMigration(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	// 既有部署的明文行(迁移前): 读侧透传, 运行时不报错。
	if err := db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", model.SettingKeyEpayKey, "legacy-plain").Error; err != nil {
		t.Fatalf("seed plaintext: %v", err)
	}
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read legacy plaintext: %v", err)
	}
	if got.Value != "legacy-plain" {
		t.Fatalf("read back = %q, want %q", got.Value, "legacy-plain")
	}
}

func TestMigrateLegacyPlaintextIsAtomicAndIdempotent(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	seeds := []struct {
		key   model.SettingKey
		value string
	}{
		{model.SettingKeyEpayKey, "legacy-epay"},
		{model.SettingKeyStripeSecretKey, "legacy-stripe"},
		{model.SettingKeyGithubToken, ""},
		{model.SettingKeySiteName, "plain-name"},
	}
	for _, seed := range seeds {
		if err := db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", string(seed.key), seed.value).Error; err != nil {
			t.Fatalf("seed plaintext: %v", err)
		}
	}
	createTestChannel(t, 7)
	if err := db.Exec("INSERT INTO channel_keys (channel_id, name, key, enabled) VALUES (7, 'default', 'legacy-upstream', 1)").Error; err != nil {
		t.Fatalf("seed channel key: %v", err)
	}

	// dryRun 只报告不落笔。
	dry, err := MigrateSecretRows(t.Context(), true)
	if err != nil {
		t.Fatalf("dry-run migrate: %v", err)
	}
	if dry.Total() != 3 || dry.EmptySkipped != 1 {
		t.Fatalf("dry-run stats = %+v, want 3 to encrypt and 1 empty skipped", dry)
	}
	if !strings.Contains(rawSettingValue(t, model.SettingKeyEpayKey), "legacy-epay") {
		t.Fatal("dry-run must not modify stored values")
	}

	stats, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if stats.Total() != 3 || stats.AlreadyEncrypted != 0 {
		t.Fatalf("migrate stats = %+v", stats)
	}
	for _, key := range []model.SettingKey{model.SettingKeyEpayKey, model.SettingKeyStripeSecretKey} {
		if !secretstore.IsEnvelope(rawSettingValue(t, key)) {
			t.Fatalf("setting %s still plaintext after migration", key)
		}
	}
	if plain := rawSettingValue(t, model.SettingKeySiteName); plain != "plain-name" {
		t.Fatalf("non-secret setting changed by migration: %q", plain)
	}
	if empty := rawSettingValue(t, model.SettingKeyGithubToken); empty != "" {
		t.Fatalf("empty secret must stay empty, got %q", empty)
	}

	// 幂等: 重跑零加密, 存量信封字节不变。
	before := rawSettingValue(t, model.SettingKeyEpayKey)
	again, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
	if again.Total() != 0 || again.AlreadyEncrypted != 3 {
		t.Fatalf("re-run stats = %+v, want zero writes and 3 verified envelopes", again)
	}
	if rawSettingValue(t, model.SettingKeyEpayKey) != before {
		t.Fatal("idempotent migration rewrote an already-encrypted row")
	}

	// 迁移后读侧照旧明文。
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read after migration: %v", err)
	}
	if got.Value != "legacy-epay" {
		t.Fatalf("read back = %q, want %q", got.Value, "legacy-epay")
	}
}

func TestMigrateEncryptsInternalKeys(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	// 内部键(auth_secret/metrics_token)是实际凭据: auth_secret 明文泄漏=可伪造 admin JWT 绕过 step-up,
	// 必须与密钥类设置项同等落库加密(IsSecretSetting || IsInternalSetting 同一口径), 迁移同样覆盖。
	for _, seed := range []struct {
		key   model.SettingKey
		value string
	}{
		{model.SettingKeyAuthSecret, "legacy-jwt-signing-secret"},
		{model.SettingKeyMetricsToken, "legacy-metrics-token"},
	} {
		if err := db.Exec("INSERT INTO settings (key, value) VALUES (?, ?)", string(seed.key), seed.value).Error; err != nil {
			t.Fatalf("seed %s: %v", seed.key, err)
		}
	}

	stats, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if stats.SettingsEncrypted != 2 {
		t.Fatalf("migrate stats = %+v, want both internal keys encrypted", stats)
	}
	for _, seed := range []struct {
		key   model.SettingKey
		value string
	}{
		{model.SettingKeyAuthSecret, "legacy-jwt-signing-secret"},
		{model.SettingKeyMetricsToken, "legacy-metrics-token"},
	} {
		stored := rawSettingValue(t, seed.key)
		if !secretstore.IsEnvelope(stored) || strings.Contains(stored, seed.value) {
			t.Fatalf("internal key %s not sealed after migration: %q", seed.key, stored)
		}
		var got model.Setting
		if err := db.Where("key = ?", seed.key).First(&got).Error; err != nil {
			t.Fatalf("read %s: %v", seed.key, err)
		}
		if got.Value != seed.value {
			t.Fatalf("%s read back = %q, want %q", seed.key, got.Value, seed.value)
		}
	}
}

func TestMigrateVerifiesEveryEnvelopeEvenOutsideSecretKeys(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	// 一条被搬移到非密钥设置行的信封: 启动迁移必须逐条验开、发现归属不符就拒绝,
	// 而不是因为"这行不是密钥项"跳过 —— 错误 KEK/搬移密文不能等有流量才发现。
	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "epay-secret"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Exec("INSERT INTO settings (key, value) VALUES ('site_name', (SELECT value FROM settings WHERE key = ?))",
		model.SettingKeyEpayKey).Error; err != nil {
		t.Fatalf("seed relocated envelope: %v", err)
	}
	if _, err := MigrateSecretRows(t.Context(), false); err == nil {
		t.Fatal("migration accepted a relocated envelope outside secret keys, want fail-closed error")
	}
}

func TestWrongMasterKeyFailsClosed(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: "sk-live"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	// 换一把错的主密钥: 读与迁移都必须报错, 不允许解出垃圾或静默放行。
	secretstore.Reset()
	if err := secretstore.Init(secretstore.KEKConfig{Inline: hex.EncodeToString(testKEK(2))}); err != nil {
		t.Fatalf("init wrong kek: %v", err)
	}
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err == nil {
		t.Fatalf("read with wrong master key returned %q, want error", got.Value)
	}
	if _, err := MigrateSecretRows(t.Context(), false); err == nil {
		t.Fatal("migration with wrong master key succeeded, want fail-closed error")
	}
}

func TestRawMapWritesAreProtected(t *testing.T) {
	setupSecretDB(t, testKEK(1))

	// db.Table 裸表 map 插入同样绕序列化器: 有行归属就能加密。
	if err := db.Table(secretSettingTable).Create(map[string]any{
		"key": string(model.SettingKeyEpayKey), "value": "map-inserted",
	}).Error; err != nil {
		t.Fatalf("raw map create: %v", err)
	}
	if stored := rawSettingValue(t, model.SettingKeyEpayKey); !secretstore.IsEnvelope(stored) {
		t.Fatalf("raw map create wrote plaintext: %q", stored)
	}
	var got model.Setting
	if err := db.Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Value != "map-inserted" {
		t.Fatalf("read back = %q, want %q", got.Value, "map-inserted")
	}

	// 没有行归属的 map 更新 fail closed。
	if err := db.Table(secretSettingTable).Where("key = ?", model.SettingKeyEpayKey).
		Update("value", "orphan-update").Error; err == nil {
		t.Fatal("raw map update without ownership succeeded, want fail-closed error")
	}
}

func TestEmptySecretValueStaysEmpty(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	if err := db.Create(&model.Setting{Key: model.SettingKeyEpayKey, Value: ""}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if stored := rawSettingValue(t, model.SettingKeyEpayKey); stored != "" {
		t.Fatalf("empty secret stored as %q, want empty string", stored)
	}
}
