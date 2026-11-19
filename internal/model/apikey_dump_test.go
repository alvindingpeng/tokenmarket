package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDBDumpAPIKeyJSONCarriesFingerprintNotPlaintext 备份必须带走指纹(恢复后 Key 才仍然可用),
// 但也绝不再写明文: 新备份只有 key_hash / key_prefix / key_last4。
func TestDBDumpAPIKeyJSONCarriesFingerprintNotPlaintext(t *testing.T) {
	dump := DBDump{
		Version: 5,
		APIKeys: []APIKey{{
			ID:        3,
			UserID:    9,
			Name:      "backup",
			APIKey:    "sk-plaintext-must-not-leak",
			KeyHash:   "cafebabe",
			KeyPrefix: "sk-plaintext",
			KeyLast4:  "leak",
		}},
		Settings: []Setting{{Key: SettingKeyMinBalance, Value: "1"}},
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "cafebabe") {
		t.Fatalf("backup must carry the fingerprint, or restored keys stop working: %s", body)
	}
	if strings.Contains(body, "sk-plaintext-must-not-leak") {
		t.Fatalf("backup must not write plaintext: %s", body)
	}

	var back DBDump
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.APIKeys) != 1 || back.APIKeys[0].KeyHash != "cafebabe" {
		t.Fatalf("fingerprint lost in round trip: %+v", back.APIKeys)
	}
	// 自定义编解码只替换 api_keys, 其余字段必须原样进出。
	if back.Version != 5 || len(back.Settings) != 1 || back.Settings[0].Key != SettingKeyMinBalance {
		t.Fatalf("other dump fields damaged by the api_keys codec: %+v", back)
	}
}

// TestDBDumpAcceptsLegacyPlaintextAPIKeys 老备份(改哈希前导出的)带的是明文:
// 导入时明文进内存字段, 由模型 hook 换算指纹落库, 原 Key 因此无感继续可用。
func TestDBDumpAcceptsLegacyPlaintextAPIKeys(t *testing.T) {
	legacy := []byte(`{
		"version": 5,
		"api_keys": [
			{"id": 1, "user_id": 2, "name": "legacy", "api_key": "sk-legacy-plaintext", "enabled": true, "supported_models": []}
		]
	}`)
	var dump DBDump
	if err := json.Unmarshal(legacy, &dump); err != nil {
		t.Fatal(err)
	}
	if len(dump.APIKeys) != 1 {
		t.Fatalf("legacy api_keys not decoded: %+v", dump.APIKeys)
	}
	got := dump.APIKeys[0]
	if got.APIKey != "sk-legacy-plaintext" {
		t.Fatalf("legacy plaintext not carried for migration: %q", got.APIKey)
	}
	if err := got.ApplyAPIKeySecret(); err != nil {
		t.Fatal(err)
	}
	if got.KeyHash != APIKeyFingerprint("sk-legacy-plaintext") {
		t.Fatalf("legacy key must hash to the deterministic fingerprint, got %q", got.KeyHash)
	}
}

// TestDBDumpWithoutAPIKeysStaysEmpty 没有 api_keys 的 dump 不该被编解码改成别的形状。
func TestDBDumpWithoutAPIKeysStaysEmpty(t *testing.T) {
	raw, err := json.Marshal(DBDump{Version: 5, Groups: []Group{{ID: 1, Name: "g"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "api_keys") {
		t.Fatalf("empty api_keys must stay omitted: %s", raw)
	}
	var back DBDump
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.APIKeys) != 0 || len(back.Groups) != 1 {
		t.Fatalf("round trip damaged: %+v", back)
	}
}
