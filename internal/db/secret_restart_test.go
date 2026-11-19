package db

import (
	"encoding/hex"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"path/filepath"
	"testing"
)

func TestEncryptedCredentialsSurviveDatabaseAndMasterKeyReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	kek := testKEK(19)
	initKey := func() {
		t.Helper()
		if err := secretstore.Init(secretstore.KEKConfig{Inline: hex.EncodeToString(kek)}); err != nil {
			t.Fatal("key init failed")
		}
	}
	initKey()
	defer secretstore.Reset()
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	createTestChannel(t, 91)
	setting := model.Setting{Key: model.SettingKeyAuthSecret, Value: "restart-signing-secret"}
	if err := GetDB().Create(&setting).Error; err != nil {
		t.Fatal(err)
	}
	channel := model.ChannelKey{ChannelID: 91, ChannelKeyConfig: model.ChannelKeyConfig{Name: "restart", Key: "restart-upstream-secret"}}
	if err := GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	api := model.APIKey{Name: "restart-api", APIKey: "restart-legacy-api-secret"}
	if err := GetDB().Create(&api).Error; err != nil {
		t.Fatal(err)
	}
	envelope := rawSettingValue(t, model.SettingKeyAuthSecret)
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	secretstore.Reset()
	initKey()
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	defer Close()
	stats, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total() != 0 || stats.AlreadyEncrypted != 2 {
		t.Fatal("restart must verify without rewriting")
	}
	var got model.Setting
	if err := GetDB().Where("key = ?", model.SettingKeyAuthSecret).First(&got).Error; err != nil || got.Value != setting.Value {
		t.Fatal("signing secret changed after reload")
	}
	var ch model.ChannelKey
	if err := GetDB().First(&ch, channel.ID).Error; err != nil || ch.Key != channel.Key {
		t.Fatal("channel credential changed after reload")
	}
	var found model.APIKey
	if err := GetDB().Where("key_hash = ?", model.APIKeyFingerprint(api.APIKey)).First(&found).Error; err != nil || found.ID != api.ID || found.APIKey != "" {
		t.Fatal("API fingerprint lookup changed after reload")
	}
	if rawSettingValue(t, model.SettingKeyAuthSecret) != envelope {
		t.Fatal("restart rewrote envelope")
	}
}
