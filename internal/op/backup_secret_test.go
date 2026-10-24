package op

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

func backupTestKEK(tag byte) []byte {
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i) ^ tag
	}
	return kek
}

// backupSecretTestDB 起一个带指定主密钥的独立库。
func backupSecretTestDB(t *testing.T, kek []byte) *secretstore.MasterKey {
	t.Helper()
	secretstore.Reset()
	t.Cleanup(secretstore.Reset)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: hex.EncodeToString(kek)}); err != nil {
		t.Fatalf("init secret store: %v", err)
	}
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "backup-secret.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return secretstore.Active()
}

func rawSetting(t *testing.T, key model.SettingKey) string {
	t.Helper()
	var value string
	if err := db.GetDB().Raw("SELECT value FROM settings WHERE key = ?", string(key)).Scan(&value).Error; err != nil {
		t.Fatalf("raw read setting %s: %v", key, err)
	}
	return value
}

func rawChannelKey(t *testing.T, id int) string {
	t.Helper()
	var value string
	if err := db.GetDB().Raw("SELECT key FROM channel_keys WHERE id = ?", id).Scan(&value).Error; err != nil {
		t.Fatalf("raw read channel key %d: %v", id, err)
	}
	return value
}

// TestBackupRoundTripAcrossInstances 验证备份导出/导入的跨实例语义:
// 导出是解密后的明文快照(跨实例恢复的前提, 也是裸 JSON 的泄密面),
// 导入按目标实例的主密钥重新加密封存, 两个实例的 KEK 可以不同。
func TestBackupRoundTripAcrossInstances(t *testing.T) {
	sourceKey := backupSecretTestDB(t, backupTestKEK(1))

	if err := SettingUpsert(model.SettingKeyEpayKey, "sk-epay-live"); err != nil {
		t.Fatalf("upsert epay key: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyStripeSecretKey, "sk-stripe-live"); err != nil {
		t.Fatalf("upsert stripe key: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyAuthSecret, "jwt-signing-secret"); err != nil {
		t.Fatalf("upsert auth secret: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyMetricsToken, "metrics-token"); err != nil {
		t.Fatalf("upsert metrics token: %v", err)
	}
	if err := SettingUpsert(model.SettingKeySiteName, "plain-name"); err != nil {
		t.Fatalf("upsert site name: %v", err)
	}
	channel := model.Channel{ID: 11, ChannelConfig: model.ChannelConfig{Name: "src-channel"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	keyRow := model.ChannelKey{ID: 21, ChannelID: 11,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: "default", Key: "sk-upstream-live", Enabled: true}}
	if err := db.GetDB().Create(&keyRow).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	// 源库: 密文落库(SettingUpsert 的 map 更新与建行兜底都在这条路径上)。
	for _, key := range []model.SettingKey{model.SettingKeyEpayKey, model.SettingKeyStripeSecretKey, model.SettingKeyAuthSecret, model.SettingKeyMetricsToken} {
		if stored := rawSetting(t, key); !secretstore.IsEnvelope(stored) || strings.Contains(stored, "live") || strings.Contains(stored, "secret") || strings.Contains(stored, "token") {
			t.Fatalf("setting %s not sealed at rest: %q", key, stored)
		}
	}
	if stored := rawChannelKey(t, 21); !secretstore.IsEnvelope(stored) || strings.Contains(stored, "sk-upstream-live") {
		t.Fatalf("channel key not sealed at rest: %q", stored)
	}

	// 导出: 明文快照 + 内部键(auth_secret / metrics_token)不随备份外传。
	dump, err := DBExportAll(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	exported := map[model.SettingKey]string{}
	for _, setting := range dump.Settings {
		exported[setting.Key] = setting.Value
		if model.IsInternalSetting(setting.Key) {
			t.Fatalf("internal setting %s leaked into backup dump", setting.Key)
		}
	}
	if exported[model.SettingKeyEpayKey] != "sk-epay-live" || exported[model.SettingKeyStripeSecretKey] != "sk-stripe-live" {
		t.Fatalf("export must carry plaintext secrets for cross-instance restore, got %+v", exported)
	}
	if exported[model.SettingKeySiteName] != "plain-name" {
		t.Fatalf("export mangled plain setting: %q", exported[model.SettingKeySiteName])
	}
	if len(dump.ChannelKeys) != 1 || dump.ChannelKeys[0].Key != "sk-upstream-live" {
		t.Fatalf("export must carry plaintext channel keys, got %+v", dump.ChannelKeys)
	}

	// 目标实例: 另一把主密钥, 导入后用目标 KEK 重新加密。
	// 渠道行不在本用例范围(导入侧会按接口同一套规范化校验 base_url 等字段),
	// 目标库预置同 ID 渠道行以满足外键, 导入只走 settings / channel_keys 的再加密路径。
	dump.Channels = nil
	_ = db.Close()
	targetKey := backupSecretTestDB(t, backupTestKEK(2))
	if targetKey.KeyID() == sourceKey.KeyID() {
		t.Fatal("test setup: both instances derived the same key ID")
	}
	targetChannel := model.Channel{ID: 11, ChannelConfig: model.ChannelConfig{Name: "dst-channel"}}
	if err := db.GetDB().Create(&targetChannel).Error; err != nil {
		t.Fatalf("create target channel: %v", err)
	}
	if _, err := DBImportIncremental(context.Background(), dump); err != nil {
		t.Fatalf("import: %v", err)
	}

	stored := rawSetting(t, model.SettingKeyEpayKey)
	if !secretstore.IsEnvelope(stored) || !strings.Contains(stored, targetKey.KeyID()) {
		t.Fatalf("imported secret not sealed under target key ID %q: %q", targetKey.KeyID(), stored)
	}
	if strings.Contains(stored, sourceKey.KeyID()) {
		t.Fatalf("imported secret kept the source key ID: %q", stored)
	}
	if stored := rawChannelKey(t, 21); !secretstore.IsEnvelope(stored) || !strings.Contains(stored, targetKey.KeyID()) {
		t.Fatalf("imported channel key not sealed under target key ID: %q", stored)
	}

	// 读侧照旧明文: 运行时语义与主密钥无关。
	var got model.Setting
	if err := db.GetDB().Where("key = ?", model.SettingKeyEpayKey).First(&got).Error; err != nil {
		t.Fatalf("read imported setting: %v", err)
	}
	if got.Value != "sk-epay-live" {
		t.Fatalf("read back = %q, want %q", got.Value, "sk-epay-live")
	}
	var gotKey model.ChannelKey
	if err := db.GetDB().Where("id = ?", 21).First(&gotKey).Error; err != nil {
		t.Fatalf("read imported channel key: %v", err)
	}
	if gotKey.Key != "sk-upstream-live" {
		t.Fatalf("channel key read back = %q, want %q", gotKey.Key, "sk-upstream-live")
	}
}

// TestBackupExportHidesInternalSettings 只盯导出过滤: 内部键不进备份(与导入侧 IsInternalSetting 过滤对齐)。
func TestBackupExportHidesInternalSettings(t *testing.T) {
	backupSecretTestDB(t, backupTestKEK(1))
	if err := SettingUpsert(model.SettingKeyAuthSecret, "jwt-signing-secret"); err != nil {
		t.Fatalf("upsert auth secret: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyMetricsToken, "metrics-token"); err != nil {
		t.Fatalf("upsert metrics token: %v", err)
	}
	dump, err := DBExportAll(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, setting := range dump.Settings {
		if setting.Key == model.SettingKeyAuthSecret || setting.Key == model.SettingKeyMetricsToken {
			t.Fatalf("internal setting %s exported", setting.Key)
		}
	}
}
